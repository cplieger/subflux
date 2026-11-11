package statedb

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/cplieger/bolttable"
)

// Work is one queued instance at the generation NextWork read it.
type Work struct {
	Inst  InstanceID
	Gen   uint64
	Cause WorkCause
}

// maxWorkLimit bounds one NextWork answer.
const maxWorkLimit = 1000

// EnqueueWork queues each known instance once: a new row takes the next
// enqueue position, an existing one keeps its position and the higher cause,
// becomes due at once and moves to the next generation, so an AckWork of the
// run that read the older one leaves it queued. Unknown instances are skipped.
func (s *Store) EnqueueWork(ctx context.Context, ids []InstanceID, cause WorkCause) error {
	if cause < Scan || cause > Item {
		return fmt.Errorf("statedb: work cause %d is not scan, import or item", cause)
	}
	return s.write(ctx, "enqueue work", s.db.Update, func(tx *bolttable.Tx) error { return s.enqueue(tx, ids, cause) })
}

// enqueue is EnqueueWork inside a caller's transaction.
func (s *Store) enqueue(tx *bolttable.Tx, ids []InstanceID, cause WorkCause) error {
	queued := false
	for _, id := range ids {
		ok, err := s.enqueueOne(tx, id, cause)
		if err != nil {
			return err
		}
		queued = queued || ok
	}
	if queued {
		tx.AfterCommit(s.signalWork)
	}
	return nil
}

// enqueueOne queues id, answering false for an unknown instance.
func (s *Store) enqueueOne(tx *bolttable.Tx, id InstanceID, cause WorkCause) (bool, error) {
	r := tx.Read()
	if _, known, err := s.t.instances.Get(r, id); err != nil || !known {
		return false, err
	}
	w, found, err := s.t.work.Get(r, id)
	if err != nil {
		return false, err
	}
	if found {
		w.Cause, w.Gen, w.NotBefore = max(w.Cause, uint8(cause)), w.Gen+1, time.Time{}
	} else {
		seq, err := s.t.work.NextID(tx)
		if err != nil {
			return false, err
		}
		w = workRow{Seq: seq, Gen: 1, Cause: uint8(cause)}
	}
	return true, s.t.work.Put(tx, id, &w)
}

// NextWork returns up to limit due rows, every Item row before any other
// and each cause oldest first. limit outside 1 to 1,000 is an error.
func (s *Store) NextWork(ctx context.Context, limit int) ([]Work, error) {
	if limit < 1 || limit > maxWorkLimit {
		return nil, fmt.Errorf("statedb: NextWork limit %d is outside 1 to %d", limit, maxWorkLimit)
	}
	now := s.now().UnixNano()
	var out []Work
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		out = make([]Work, 0, min(limit, 64))
		return s.t.workOrder.Scan(r, bolttable.Range{}, func(_ []byte, id InstanceID, proj []byte) (bool, error) {
			p, err := parseWorkProjection(proj)
			if err != nil {
				return false, err
			}
			if p.notBefore > now {
				return true, nil
			}
			out = append(out, Work{Inst: id, Gen: p.gen, Cause: p.cause})
			return len(out) < limit, nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AckWork deletes each row still at the generation its Work names.
func (s *Store) AckWork(ctx context.Context, ws []Work) error {
	return s.write(ctx, "acknowledge work", s.db.Update, func(tx *bolttable.Tx) error {
		for _, w := range ws {
			row, found, err := s.t.work.Get(tx.Read(), w.Inst)
			if err != nil {
				return err
			}
			if found && row.Gen == w.Gen {
				if _, err := s.t.work.Delete(tx, w.Inst); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// DeferWork makes w's row due no earlier than notBefore when it is still at
// w's generation, records its first deferral the first time and returns
// that first deferral; at another generation, or for no row, it changes
// nothing and answers deferred false. Both survive a reopen.
func (s *Store) DeferWork(ctx context.Context, w Work, notBefore time.Time) (first time.Time, deferred bool, err error) {
	err = s.write(ctx, "defer work", s.db.Update, func(tx *bolttable.Tx) error {
		first, deferred = time.Time{}, false
		row, found, gerr := s.t.work.Get(tx.Read(), w.Inst)
		if gerr != nil || !found || row.Gen != w.Gen {
			return gerr
		}
		row.NotBefore = notBefore.UTC().Round(0)
		if row.FirstDeferral.IsZero() {
			row.FirstDeferral = s.now().UTC().Round(0)
		}
		if perr := s.t.work.Put(tx, w.Inst, &row); perr != nil {
			return perr
		}
		first, deferred = row.FirstDeferral, true
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return first, deferred, nil
}

// WorkReady is signalled after every commit that enqueued work, and once
// at Open when the stored queue is not empty. It holds one wake, for one
// drainer, which reads NextWork until it answers none.
func (s *Store) WorkReady() <-chan struct{} { return s.workReady }

func (s *Store) signalWork() {
	select {
	case s.workReady <- struct{}{}:
	default:
	}
}

// wakeForStoredWork signals WorkReady when rows survived the last run.
func (s *Store) wakeForStoredWork() error {
	queued := false
	err := s.db.View(context.Background(), func(r *bolttable.ReadTx) error {
		return s.t.work.Scan(r, bolttable.Range{}, func(InstanceID, *workRow) (bool, error) {
			queued = true
			return false, nil
		})
	})
	if queued {
		s.signalWork()
	}
	return err
}

// The order index projects cause, generation and not-before, so NextWork
// reads no row. A zero not-before encodes as the minimum: due at once.
const workProjectionLen = 1 + 8 + 8

type workProjection struct {
	gen       uint64
	notBefore int64
	cause     WorkCause
}

func appendWorkProjection(dst []byte, v *workRow) []byte {
	nb := int64(math.MinInt64)
	if !v.NotBefore.IsZero() {
		nb = v.NotBefore.UnixNano()
	}
	return appendUint64(appendUint64(append(dst, v.Cause), v.Gen), uint64(nb)^signFlip) //nolint:gosec // G115: the flip is the encoding
}

func parseWorkProjection(b []byte) (workProjection, error) {
	if len(b) != workProjectionLen {
		return workProjection{}, fmt.Errorf("%w: work projection of %d bytes", ErrCorrupt, len(b))
	}
	return workProjection{
		cause:     WorkCause(b[0]),
		gen:       binary.BigEndian.Uint64(b[1:9]),
		notBefore: int64(binary.BigEndian.Uint64(b[9:]) ^ signFlip), //nolint:gosec // G115: the flip is the encoding
	}, nil
}
