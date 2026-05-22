// Package sessionbatch implements batched session activity updates to reduce
// SQLite write-lock acquisitions.
package sessionbatch

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// update is a pending session activity update.
type update struct {
	at   time.Time
	hash string
}

// BatchUpdater is the interface for batch session activity updates.
type BatchUpdater interface {
	BatchUpdateSessionActivity(ctx context.Context, tokenHashes []string, now time.Time) error
}

// Batcher collects session activity updates and flushes them in batches
// to reduce SQLite write-lock acquisitions.
type Batcher struct {
	db   BatchUpdater
	ctx  context.Context
	ch   chan update
	done chan struct{}
	wg   sync.WaitGroup
}

// New creates and starts a Batcher that flushes pending updates every
// 500ms or when 16 updates accumulate.
func New(ctx context.Context, db BatchUpdater) *Batcher {
	b := &Batcher{
		ch:   make(chan update, 64),
		done: make(chan struct{}),
		db:   db,
		ctx:  ctx,
	}
	b.wg.Go(b.run)
	return b
}

// Send enqueues a session activity update. Non-blocking; drops if buffer full.
func (b *Batcher) Send(hash string, at time.Time) {
	select {
	case b.ch <- update{hash: hash, at: at}:
	default:
		// Buffer full — drop silently. Activity updates are best-effort.
	}
}

// Stop signals the batcher to flush remaining updates and exit.
func (b *Batcher) Stop() {
	close(b.done)
	b.wg.Wait()
}

func (b *Batcher) run() {
	const (
		maxBatch  = 16
		flushFreq = 500 * time.Millisecond
	)

	ticker := time.NewTicker(flushFreq)
	defer ticker.Stop()

	buf := make([]update, 0, maxBatch)

	flush := func() {
		if len(buf) == 0 {
			return
		}
		hashes := make([]string, len(buf))
		latest := buf[0].at
		for i, u := range buf {
			hashes[i] = u.hash
			if u.at.After(latest) {
				latest = u.at
			}
		}
		ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
		defer cancel()
		if err := b.db.BatchUpdateSessionActivity(ctx, hashes, latest); err != nil {
			slog.Debug("session activity batch update failed", "error", err, "count", len(hashes))
		}
		buf = buf[:0]
	}

	for {
		select {
		case u, ok := <-b.ch:
			if !ok {
				flush()
				return
			}
			buf = append(buf, u)
			if len(buf) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-b.done:
			// Drain remaining.
			for {
				select {
				case u := <-b.ch:
					buf = append(buf, u)
				default:
					flush()
					return
				}
			}
		}
	}
}
