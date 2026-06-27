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

const (
	// maxBatch is the number of pending updates that forces an immediate flush.
	maxBatch = 16
	// flushFreq is how often a partial batch is flushed when the size
	// threshold has not been reached.
	flushFreq = 500 * time.Millisecond
	// batchTimeout bounds a single batch write to the database.
	batchTimeout = 5 * time.Second
	// chanBuffer is the capacity of the pending-update channel; sends beyond it
	// are dropped, since activity updates are best-effort.
	chanBuffer = 64
)

// New creates and starts a Batcher that flushes pending updates every
// 500ms or when 16 updates accumulate.
func New(ctx context.Context, db BatchUpdater) *Batcher {
	b := &Batcher{
		ch:   make(chan update, chanBuffer),
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
	ticker := time.NewTicker(flushFreq)
	defer ticker.Stop()

	f := &flusher{db: b.db, buf: make([]update, 0, maxBatch)}

	for {
		select {
		case u, ok := <-b.ch:
			if !ok {
				f.flush(b.ctx)
				return
			}
			f.add(u)
			if f.full() {
				f.flush(b.ctx)
			}
		case <-ticker.C:
			f.flush(b.ctx)
		case <-b.done:
			f.drain(b.ctx, b.ch)
			return
		}
	}
}

// flusher accumulates pending updates and writes them to the database as a
// single batch. It is owned by the run goroutine and is not safe for concurrent
// use.
type flusher struct {
	db  BatchUpdater
	buf []update
}

// add appends a pending update to the current batch.
func (f *flusher) add(u update) {
	f.buf = append(f.buf, u)
}

// full reports whether the batch has reached the size threshold.
func (f *flusher) full() bool {
	return len(f.buf) >= maxBatch
}

// flush writes the buffered updates as one batch and resets the buffer. The
// timestamp sent is the most recent activity time across the batch. An empty
// batch is a no-op.
func (f *flusher) flush(ctx context.Context) {
	if len(f.buf) == 0 {
		return
	}
	hashes := make([]string, len(f.buf))
	latest := f.buf[0].at
	for i, u := range f.buf {
		hashes[i] = u.hash
		if u.at.After(latest) {
			latest = u.at
		}
	}
	tctx, cancel := context.WithTimeout(ctx, batchTimeout)
	defer cancel()
	if err := f.db.BatchUpdateSessionActivity(tctx, hashes, latest); err != nil {
		slog.Debug("session activity batch update failed", "error", err, "count", len(hashes))
	}
	f.buf = f.buf[:0]
}

// drain moves any updates still buffered in ch into the current batch and then
// flushes, so no pending update is lost at shutdown.
func (f *flusher) drain(ctx context.Context, ch <-chan update) {
	for {
		select {
		case u := <-ch:
			f.add(u)
		default:
			f.flush(ctx)
			return
		}
	}
}
