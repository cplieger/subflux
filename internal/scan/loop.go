package scan

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
)

// loop is one source's state, held by its goroutine alone except q, which
// Start shares under q.mu, and poke and rearm, the signals that wake it.
type loop struct {
	s *Scanner
	q *admission
	// poke asks for a due whole pass now (a request, PassNow); rearm
	// recomputes the next wake (SetSchedule); rekey drops the whole-pass
	// backoff (Reload of a Secret-only edit).
	poke, rearm, rekey chan struct{}
	// cancel stops the loop and done is closed once it has returned; Run
	// sets both when it starts it.
	cancel context.CancelFunc
	done   chan struct{}

	// wholeSourceErr is the latest *SourceError a whole pass failed on, nil
	// once one completes; it keeps source:<id> raised between attempts. A
	// pass failed by storage or cancelled leaves it as it was.
	wholeSourceErr *mediasource.SourceError

	// retry holds the roots whose read failed while the source still
	// answered, at most one entry per root; it lives in memory, since a
	// restart's boot whole pass covers it.
	retry map[mediasource.NativeRef]retryRoot

	// A whole pass is due, never before backoffUntil, at the scheduled time
	// (firstWhole until wholeFrom is set, then wholeFrom + the whole
	// interval), or once expired, wholeRetry or a queued request is set.
	started, firstWhole, wholeFrom time.Time
	backoffUntil, lastTick         time.Time

	src media.SourceID

	// cur is the cursor batches read from: the stored one, a complete whole
	// pass's head, or a failed one's when there was none. haveCur is false
	// before a fresh source's first whole pass starts, after Expired, and
	// always without Changes.
	cur mediasource.Cursor

	firstDelay time.Duration
	// backoff is the latest failed whole pass's wait, 0 after a complete one.
	backoff time.Duration

	expired    bool
	wholeRetry bool
	haveCur    bool
	cursorRead bool
	poked      bool
}

func newLoop(s *Scanner, src media.SourceID, firstDelay time.Duration) *loop {
	return &loop{s: s, q: newAdmission(), src: src, firstDelay: firstDelay, retry: map[mediasource.NativeRef]retryRoot{}, poke: make(chan struct{}, 1), rearm: make(chan struct{}, 1), rekey: make(chan struct{}, 1)}
}

// interruptQueued ends every unstarted request Interrupted, so none waits
// on a loop that no longer runs.
func (l *loop) interruptQueued() {
	for _, id := range l.q.takeQueued() {
		l.s.status.Update(id, func(j *status.Job) { j.State, j.Outcome = status.Done, status.Interrupted })
	}
}

// signal wakes the loop on c without blocking; wakes coalesce.
func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// run ticks the source until ctx ends, then ends its unstarted requests
// Interrupted. Each tick starts after the previous one ended, so its passes
// never overlap.
func (l *loop) run(ctx context.Context) {
	defer l.interruptQueued()
	l.started = time.Now()
	l.firstWhole = l.started.Add(l.firstDelay)
	l.s.metrics.mark(l.src, l.started)
	for {
		_, sched := l.s.current()
		now := time.Now()
		at := l.nextWake(now, sched)
		if !at.After(now) {
			l.poked = false
			err := l.tick(ctx, now)
			if ctx.Err() != nil {
				return
			}
			l.reconcileSource(err)
			continue
		}
		t := time.NewTimer(at.Sub(now))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-l.poke:
			t.Stop()
			l.poked = true
		case <-l.rearm:
			t.Stop()
		case <-l.rekey:
			t.Stop()
			l.backoff, l.backoffUntil = 0, time.Time{}
		}
	}
}

// nextWake is the next change tick, or a due whole pass when sooner. A whole
// pass due before the latest tick began, which that tick could not run,
// waits for the next tick unless a poke asks for it, so a failing probe
// never spins the loop.
func (l *loop) nextWake(now time.Time, sched mediasource.Schedule) time.Time {
	if l.lastTick.IsZero() {
		return now
	}
	at := l.lastTick.Add(sched.Change)
	if w := l.wholeDueAt(sched); w.Before(at) && (w.After(l.lastTick) || l.poked) {
		at = w
	}
	return at
}

// wholeDueAt is when the next whole pass is due.
func (l *loop) wholeDueAt(sched mediasource.Schedule) time.Time {
	at := l.scheduledAt(sched)
	if l.expired || l.wholeRetry || l.q.pendingWhole() {
		at = time.Time{}
	}
	if l.backoffUntil.After(at) {
		at = l.backoffUntil
	}
	return at
}

func (l *loop) scheduledAt(sched mediasource.Schedule) time.Time {
	if l.wholeFrom.IsZero() {
		return l.firstWhole
	}
	return l.wholeFrom.Add(sched.Whole)
}

// due is what makes a whole pass due now; the zero due is not due.
type due struct{ schedule, expired, retry, requested bool }

// system reports whether the pass carries a SystemOwner job: anything but
// queued requests made it due.
func (d due) system() bool { return d.schedule || d.expired || d.retry }

func (d due) any() bool { return d.system() || d.requested }

func (l *loop) wholeDue(now time.Time, sched mediasource.Schedule) due {
	if now.Before(l.backoffUntil) {
		return due{}
	}
	d := due{schedule: !now.Before(l.scheduledAt(sched)), expired: l.expired, retry: l.wholeRetry}
	l.q.mu.Lock()
	d.requested = len(l.q.whole) > 0
	d.schedule = d.schedule || l.q.passNow
	l.q.mu.Unlock()
	return d
}

// tick reads the stored cursor once, probes the source, then runs a due
// whole pass or else a change batch (design 5.1), and answers the error it
// ended on. A failed cursor read or probe ends the tick.
func (l *loop) tick(ctx context.Context, now time.Time) error {
	l.lastTick = now
	set, sched := l.s.current()
	caps, ok := set.Capabilities(l.src)
	if !ok {
		return errNoVerdict
	}
	if caps.Changes && !l.cursorRead {
		c, found, err := l.s.store.SourceCursor(ctx, l.src)
		if err != nil {
			if ctx.Err() == nil {
				l.s.log.Warn("scan: cannot read the stored cursor", "source", l.src, "error", err)
			}
			return err
		}
		l.cursorRead = true
		if found && !c.IsZero() {
			l.cur, l.haveCur = c, true
		}
	}
	if err := l.probe(ctx, set); err != nil {
		return err
	}
	if d := l.wholeDue(time.Now(), sched); d.any() {
		return l.whole(ctx, set, caps, d)
	}
	return l.batch(ctx, set)
}
