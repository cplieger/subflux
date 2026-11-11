package search

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// causeInstanceUnknown ends a SearchNow job whose instance is gone.
const causeInstanceUnknown = "instance_unknown"

// waiter is one SearchNow job waiting for its instance's next Item run.
// cancelled is set under e.mu when the stop handle ran before await set id;
// await then settles the job Cancelled instead of registering it.
type waiter struct {
	id        status.JobID
	done      bool
	cancelled bool
}

// itemRun is a running Item run and the SearchNow jobs it settles; cancel
// ends it once none of them waits any more.
type itemRun struct {
	cancel  context.CancelFunc
	waiters []*waiter
}

// SearchNow starts an ItemScan job settled by the first Item run of inst
// starting after it: Succeeded, Partial with the skips' reasons in Cause,
// Failed on a run error or instance_unknown. With idem, under Starts' lock,
// the job started under that key is returned whatever inst's state; else an
// instance not held or itemless is ErrInstanceUnknown before any job.
// Cancelling settles it Cancelled and cancels a run no other job waits on;
// on a drained engine the work is enqueued and the job settles Interrupted.
func (e *Engine) SearchNow(ctx context.Context, owner int64, inst statedb.InstanceID, idem *status.JobKey) (status.Job, error) {
	unlock, err := e.deps.Starts.lock(ctx, owner, idem)
	if err != nil {
		return status.Job{}, err
	}
	defer unlock()
	if idem != nil {
		if j, ok, kerr := e.deps.Status.Keyed(ctx, owner, *idem); kerr != nil || ok {
			return j, kerr
		}
	}
	if ok, aerr := e.attached(ctx, inst); aerr != nil || !ok {
		return status.Job{}, cmpErr(aerr, ErrInstanceUnknown)
	}
	w := &waiter{}
	job := status.Job{Kind: status.ItemScan, Owner: owner, Key: idem, Scope: strconv.FormatUint(uint64(inst), 10)}
	j, replayed, err := e.begin(ctx, job, func() { e.cancelWaiter(inst, w) })
	if err != nil || replayed {
		return j, err
	}
	if err := e.await(ctx, inst, w, j.ID); err != nil {
		return status.Job{}, err
	}
	return e.job(ctx, j)
}

// await registers w, begun as job id, for inst's next Item run and enqueues
// that run; a stop handle that ran before settles it Cancelled instead.
func (e *Engine) await(ctx context.Context, inst statedb.InstanceID, w *waiter, id status.JobID) error {
	e.mu.Lock()
	w.id = id
	drained, cancelled := e.draining, w.cancelled
	if !drained && !cancelled {
		e.waiters[inst] = append(e.waiters[inst], w)
	}
	e.mu.Unlock()
	if cancelled {
		e.settleWaiters([]*waiter{w}, status.Cancelled, "")
		return nil
	}
	if err := e.deps.Store.EnqueueWork(ctx, []statedb.InstanceID{inst}, statedb.Item); err != nil {
		e.dropWaiter(inst, w)
		e.settleWaiters([]*waiter{w}, status.Failed, "")
		return fmt.Errorf("search: enqueue instance %d's search: %w", inst, err)
	}
	switch ok, err := e.attached(ctx, inst); {
	case drained:
		e.settleWaiters([]*waiter{w}, status.Interrupted, causeSettingsChanged)
	case err == nil && !ok:
		e.dropWaiter(inst, w)
		e.settleWaiters([]*waiter{w}, status.Failed, causeInstanceUnknown)
	}
	return nil
}

// attached reports whether the store holds inst with an item.
func (e *Engine) attached(ctx context.Context, inst statedb.InstanceID) (bool, error) {
	in, found, err := e.deps.Store.Instance(ctx, inst)
	if err != nil {
		return false, fmt.Errorf("search: read instance %d: %w", inst, err)
	}
	return found && len(in.Items) > 0, nil
}

// cmpErr is err, else fallback.
func cmpErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// takeWaiters hands an Item run of w the SearchNow jobs registered for its
// instance, under a context their cancellation ends; any other run keeps
// ctx.
func (e *Engine) takeWaiters(ctx context.Context, w statedb.Work) (context.Context, context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ws := e.waiters[w.Inst]
	if w.Cause != statedb.Item || len(ws) == 0 {
		return ctx, func() {}
	}
	delete(e.waiters, w.Inst)
	rctx, cancel := context.WithCancel(ctx)
	e.running[w.Inst] = &itemRun{cancel: cancel, waiters: ws}
	return rctx, cancel
}

// runEnded is the jobs still waiting on inst's run that just returned.
func (e *Engine) runEnded(inst statedb.InstanceID) []*waiter {
	e.mu.Lock()
	defer e.mu.Unlock()
	ir := e.running[inst]
	delete(e.running, inst)
	if ir == nil {
		return nil
	}
	return ir.waiters
}

// keepWaiters registers ws again, ahead of the jobs registered since, for
// inst's next run.
func (e *Engine) keepWaiters(inst statedb.InstanceID, ws []*waiter) {
	if len(ws) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.draining {
		ws = slices.DeleteFunc(ws, func(w *waiter) bool { return w.done })
	}
	e.waiters[inst] = append(ws, e.waiters[inst]...)
}

// dropWaiter unregisters w.
func (e *Engine) dropWaiter(inst statedb.InstanceID, w *waiter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.waiters[inst] = slices.DeleteFunc(e.waiters[inst], func(x *waiter) bool { return x == w })
	if len(e.waiters[inst]) == 0 {
		delete(e.waiters, inst)
	}
}

// cancelWaiter is a SearchNow job's stop handle: it settles the job
// Cancelled and cancels the run that took it once no other job waits on
// that run.
func (e *Engine) cancelWaiter(inst statedb.InstanceID, w *waiter) {
	e.mu.Lock()
	if w.done {
		e.mu.Unlock()
		return
	}
	if w.id == "" {
		w.cancelled = true
		e.mu.Unlock()
		return
	}
	e.waiters[inst] = slices.DeleteFunc(e.waiters[inst], func(x *waiter) bool { return x == w })
	if len(e.waiters[inst]) == 0 {
		delete(e.waiters, inst)
	}
	if ir := e.running[inst]; ir != nil && slices.Contains(ir.waiters, w) {
		ir.waiters = slices.DeleteFunc(ir.waiters, func(x *waiter) bool { return x == w })
		if len(ir.waiters) == 0 {
			ir.cancel()
		}
	}
	e.mu.Unlock()
	e.settleWaiters([]*waiter{w}, status.Cancelled, "")
}

// settleRun settles ws by their run's report.
func (e *Engine) settleRun(ws []*waiter, rep runReport) {
	switch {
	case rep.gone:
		e.settleWaiters(ws, status.Failed, causeInstanceUnknown)
	case len(rep.skips) > 0:
		reasons := slices.Compact(slices.Sorted(slices.Values(rep.skips)))
		e.settleWaiters(ws, status.Partial, strings.Join(reasons, ","))
	default:
		e.settleWaiters(ws, status.Succeeded, "")
	}
}

// settleWaiters settles each of ws not yet settled with outcome and cause.
func (e *Engine) settleWaiters(ws []*waiter, o status.Outcome, cause string) {
	e.mu.Lock()
	var ids []status.JobID
	for _, w := range ws {
		if !w.done && w.id != "" {
			w.done = true
			ids = append(ids, w.id)
		}
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.end(id, o, cause)
	}
}
