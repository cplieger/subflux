package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
)

// The deferral of a run its media cannot take yet (8.1).
const (
	// rescheduleAfter is how long a deferred row waits.
	rescheduleAfter = 5 * time.Minute
	// rescheduleFor is how long after its first deferral a row is given up.
	rescheduleFor = 24 * time.Hour
)

// deferrable reports whether err defers the run: its root unavailable, or
// a write meeting a pending path or a changed file.
func deferrable(err error) bool {
	return errors.Is(err, mediawrite.ErrRootUnavailable) || errors.Is(err, mediawrite.ErrPathPending) ||
		errors.Is(err, mediawrite.ErrExpectation)
}

// deferRow makes w's row due rescheduleAfter from now, the store keeping
// its first deferral across restarts, and acknowledges it once that is
// rescheduleFor old. A row re-enqueued meanwhile stays as queued.
func (e *Engine) deferRow(ctx context.Context, w statedb.Work, cause error) error {
	now := time.Now()
	first, deferred, err := e.deps.Store.DeferWork(ctx, w, now.Add(rescheduleAfter))
	switch {
	case err != nil:
		return fmt.Errorf("search: defer instance %d's work: %w", w.Inst, err)
	case !deferred:
		return nil
	case now.Sub(first) < rescheduleFor:
		e.log.Info("search: run deferred", "instance", w.Inst, "until", now.Add(rescheduleAfter), "cause", cause)
		return nil
	}
	if err := e.deps.Store.AckWork(ctx, []statedb.Work{w}); err != nil {
		return fmt.Errorf("search: acknowledge instance %d's work: %w", w.Inst, err)
	}
	e.log.Info("search: run given up, deferred since its first deferral", "instance", w.Inst, "first_deferral", first, "cause", cause)
	return nil
}
