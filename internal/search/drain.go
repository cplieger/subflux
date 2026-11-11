package search

import (
	"context"
	"errors"
	"time"

	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// The drainer's bounds (8.1).
const (
	// runsInFlight bounds the Scan and Import runs at once.
	runsInFlight = 4
	// itemRunsInFlight bounds the Item runs and manual downloads at once,
	// apart from runsInFlight, so a user's run never waits behind a scan.
	itemRunsInFlight = 4
	// workBatch is how many rows one poll may start.
	workBatch = 16
	// storeRetryMin and storeRetryMax bound the wait of a row whose run
	// failed, and of a failed poll: the first, doubling to the second.
	storeRetryMin = time.Second
	storeRetryMax = time.Minute
	// pollEvery polls for deferred rows coming due.
	pollEvery = time.Minute
	// maxPoll is statedb.NextWork's largest limit.
	maxPoll = 1000
)

var errDrainTwice = errors.New("search: Drain called twice")

// Drain is the one drainer of the work queue and owns every goroutine the
// engine starts until ctx ends; it then waits for them, releases the
// engine's bindings and returns ctx's error. A second call returns an
// error at once.
func (e *Engine) Drain(ctx context.Context) error {
	e.mu.Lock()
	again := e.drained
	e.drained = true
	e.mu.Unlock()
	if again {
		return errDrainTwice
	}
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	wake := time.NewTimer(time.Hour)
	defer wake.Stop()
	var polls backoff
	for {
		if after, ok := e.poll(ctx, &polls); ok {
			wake.Reset(after)
		} else {
			wake.Stop()
		}
		select {
		case <-ctx.Done():
			e.stop()
			return ctx.Err()
		case <-e.deps.Store.WorkReady():
		case <-e.done:
		case <-tick.C:
		case <-wake.C:
		}
	}
}

// stop sets the draining flag, so nothing starts another goroutine, ends
// every manual job, drops the handles, waits for the running goroutines,
// releases every binding and settles the SearchNow jobs no run took
// Interrupted, their Item rows staying for the next engine.
func (e *Engine) stop() {
	e.mu.Lock()
	e.draining = true
	for _, cancel := range e.jobs {
		cancel()
	}
	e.mu.Unlock()
	e.handles.clear()
	_ = e.scans.Wait()
	_ = e.items.Wait()
	for i := range e.providers {
		e.providers[i].binding.Release()
	}
	e.mu.Lock()
	var left []*waiter
	for inst, ws := range e.waiters {
		left = append(left, ws...)
		delete(e.waiters, inst)
	}
	e.mu.Unlock()
	e.settleWaiters(left, status.Interrupted, causeSettingsChanged)
}

// poll starts every due row whose instance is neither running nor waiting
// out a failure, as far as its group has room, and answers when the next
// timed wake is due: a failed poll's retry or the earliest row's wait.
func (e *Engine) poll(ctx context.Context, polls *backoff) (time.Duration, bool) {
	e.mu.Lock()
	limit := min(workBatch+len(e.inFlight)+len(e.retries), maxPoll)
	clear(e.leftSince)
	e.mu.Unlock()
	rows, err := e.next(ctx, limit)
	if err != nil {
		if ctx.Err() != nil {
			return 0, false
		}
		if polls.fail() == 1 {
			e.log.Warn("search: reading the work queue failed", "err", err)
		}
		return polls.wait, true
	}
	if polls.failures > 0 {
		e.log.Info("search: reading the work queue recovered", "failures", polls.failures)
		*polls = backoff{}
	}
	now := time.Now()
	for _, w := range rows {
		e.start(ctx, w, now)
	}
	return e.soonest(now)
}

// start runs w on its cause's group unless its instance is running,
// waiting, or ended a run after the poll's read, which may list the row
// that run acknowledged, or the group is full; each leaves the row for a
// later poll, the ended run's wake bringing one.
func (e *Engine) start(ctx context.Context, w statedb.Work, now time.Time) {
	e.mu.Lock()
	r := e.retries[w.Inst]
	if e.inFlight[w.Inst] || e.leftSince[w.Inst] || r != nil && r.until.After(now) {
		e.mu.Unlock()
		return
	}
	e.inFlight[w.Inst] = true
	e.mu.Unlock()
	run := func() {
		e.runRow(ctx, w)
		e.mu.Lock()
		delete(e.inFlight, w.Inst)
		e.leftSince[w.Inst] = true
		e.mu.Unlock()
		// After leaving: a row re-enqueued during the run is startable
		// when this wake's poll reads it.
		select {
		case e.done <- struct{}{}:
		default:
		}
	}
	if w.Cause != statedb.Item {
		if !e.scans.TryGo(func() error { run(); return nil }) {
			e.leave(w.Inst)
		}
		return
	}
	select {
	case e.slots <- struct{}{}:
		e.items.Go(func() error {
			defer func() { <-e.slots }()
			run()
			return nil
		})
	default:
		e.leave(w.Inst)
	}
}

func (e *Engine) leave(inst statedb.InstanceID) {
	e.mu.Lock()
	delete(e.inFlight, inst)
	e.mu.Unlock()
}

// soonest is how long until the first row still waiting out a failure may
// start. A row past its wait needs no timer: a poll found it running or
// its group full, and the run ending wakes the drainer.
func (e *Engine) soonest(now time.Time) (time.Duration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var first time.Time
	for _, r := range e.retries {
		if r.until.After(now) && (first.IsZero() || r.until.Before(first)) {
			first = r.until
		}
	}
	if first.IsZero() {
		return 0, false
	}
	return first.Sub(now), true
}

// backoff is a failure streak's wait: storeRetryMin, doubling to
// storeRetryMax.
type backoff struct {
	wait     time.Duration
	failures int
}

// fail counts one more failure and answers the streak's length.
func (b *backoff) fail() int {
	b.failures++
	if b.wait == 0 {
		b.wait = storeRetryMin
	} else {
		b.wait = min(2*b.wait, storeRetryMax)
	}
	return b.failures
}
