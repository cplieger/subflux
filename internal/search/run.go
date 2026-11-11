package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// corruptAcks is how many consecutive statedb.ErrCorrupt failures of one
// row's runs acknowledge it: a stored record that does not decode fails
// every retry alike.
const corruptAcks = 3

// retry is one row's failure streak: when it may run again, and how many
// of its latest failures in a row were ErrCorrupt.
type retry struct {
	until   time.Time
	backoff backoff
	corrupt int
}

// runRow runs w under its instance's gate and settles its row: a success,
// or a run its SearchNow jobs all cancelled, acknowledges it; a root
// unavailable, pending path or changed file defers it (deferRow); an ended
// ctx leaves it for the next engine; any other failure leaves it waiting
// (failed). An Item row's run settles the SearchNow jobs registered before
// it started, but a deferred or drained run keeps them for the next.
func (e *Engine) runRow(ctx context.Context, w statedb.Work) {
	rctx, cancel := e.takeWaiters(ctx, w)
	defer cancel()
	rep, err := e.gated(rctx, w)
	ws := e.runEnded(w.Inst)
	cancelled := ctx.Err() == nil && rctx.Err() != nil && errors.Is(err, context.Canceled)
	switch {
	case ctx.Err() != nil:
		e.keepWaiters(w.Inst, ws)
		return
	case err == nil || cancelled:
		e.settleRun(ws, rep)
		if err = e.deps.Store.AckWork(ctx, []statedb.Work{w}); err != nil {
			err = fmt.Errorf("search: acknowledge instance %d's work: %w", w.Inst, err)
		}
	case deferrable(err):
		e.keepWaiters(w.Inst, ws)
		err = e.deferRow(ctx, w, err)
	default:
		e.settleWaiters(ws, status.Failed, "")
	}
	if err != nil {
		e.failed(ctx, w, err)
		return
	}
	e.mu.Lock()
	delete(e.retries, w.Inst)
	e.mu.Unlock()
}

// gated runs w holding its instance's gate, which manual downloads of the
// instance share.
func (e *Engine) gated(ctx context.Context, w statedb.Work) (runReport, error) {
	unlock, err := e.gate.Lock(ctx, w.Inst)
	if err != nil {
		return runReport{}, err
	}
	defer unlock()
	return e.run(ctx, w)
}

// failed records a failed run: the row waits out its streak's backoff and
// is never acknowledged, but at its corruptAcks-th consecutive ErrCorrupt.
// A streak's first failure logs ERROR, later ones WARN.
func (e *Engine) failed(ctx context.Context, w statedb.Work, err error) {
	e.mu.Lock()
	r := e.retries[w.Inst]
	if r == nil {
		r = &retry{}
		e.retries[w.Inst] = r
	}
	n := r.backoff.fail()
	if errors.Is(err, statedb.ErrCorrupt) {
		r.corrupt++
	} else {
		r.corrupt = 0
	}
	r.until = time.Now().Add(r.backoff.wait)
	corrupt, wait := r.corrupt, r.backoff.wait
	e.mu.Unlock()
	if corrupt >= corruptAcks {
		e.giveUp(ctx, w, err)
		return
	}
	if n == 1 {
		e.log.Error("search: run failed", "instance", w.Inst, "retry_in", wait, "err", err)
		return
	}
	e.log.Warn("search: run failed again", "instance", w.Inst, "failures", n, "retry_in", wait, "err", err)
}

// giveUp acknowledges a row whose stored record does not decode; a failed
// acknowledgement leaves it waiting as before.
func (e *Engine) giveUp(ctx context.Context, w statedb.Work, cause error) {
	if err := e.deps.Store.AckWork(ctx, []statedb.Work{w}); err != nil {
		e.log.Warn("search: acknowledging a corrupt instance's work failed", "instance", w.Inst, "err", err)
		return
	}
	e.mu.Lock()
	delete(e.retries, w.Inst)
	e.mu.Unlock()
	e.log.Error("search: work acknowledged, its instance's stored record does not decode", "instance", w.Inst, "failures", corruptAcks, "err", cause)
}
