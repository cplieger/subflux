package scan

import (
	"context"
	"slices"
	"sync"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
)

// The queue's bounds (architecture NFR4): unstarted slots per source, and
// per owner and source, so one key cannot fill a source's queue for everyone.
const (
	maxQueued         = 16
	maxQueuedPerOwner = 4
)

// admission is a source's unstarted requests and the passes carrying them.
// Start checks capacity, calls Begin and inserts under mu, and a pass takes
// what is queued when it starts under mu, so a request is either queued or
// carried by exactly one pass, and the bounds hold under concurrent Starts.
type admission struct {
	running *running
	// roots holds the unstarted refetches, one slot per root; carried the
	// refetch jobs a batch took and has not ended.
	roots   map[mediasource.NativeRef][]queued
	carried map[status.JobID]bool
	// whole holds the unstarted whole requests, one slot however many.
	whole []queued
	mu    sync.Mutex
	// passNow makes the scheduled whole pass due at once (PassNow).
	passNow bool
	// closed is set once the loop has ended; Start then queues nothing.
	closed bool
}

func newAdmission() *admission {
	return &admission{roots: map[mediasource.NativeRef][]queued{}, carried: map[status.JobID]bool{}}
}

// queued is one unstarted request's job; a slot counts against the owner of
// its first job.
type queued struct {
	job   status.JobID
	owner int64
}

// running is the whole pass in flight. live holds its requesters' jobs not
// yet ended; requested is true when only requests made the pass due, so the
// end of its last live requester job cancels it.
type running struct {
	cancel    context.CancelFunc
	live      map[status.JobID]bool
	requested bool
}

// roomLocked reports whether a request of owner for root (nil: a whole
// pass) fits: one joining a queued request of its scope takes no slot.
func (q *admission) roomLocked(owner int64, root *mediasource.NativeRef) bool {
	if root == nil && len(q.whole) > 0 || root != nil && len(q.roots[*root]) > 0 {
		return true
	}
	slots, mine := len(q.roots), 0
	for _, qs := range q.roots {
		if qs[0].owner == owner {
			mine++
		}
	}
	if len(q.whole) > 0 {
		slots++
		if q.whole[0].owner == owner {
			mine++
		}
	}
	return slots < maxQueued && mine < maxQueuedPerOwner
}

func (q *admission) insertLocked(j queued, root *mediasource.NativeRef) {
	if root == nil {
		q.whole = append(q.whole, j)
		return
	}
	q.roots[*root] = append(q.roots[*root], j)
}

// pendingWhole reports whether a queued request or PassNow makes a whole
// pass due.
func (q *admission) pendingWhole() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.whole) > 0 || q.passNow
}

// markPassNow makes the next whole pass due at once; startWhole clears it.
func (q *admission) markPassNow() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.passNow = true
}

// takeQueued closes the queue, empties it and answers its jobs.
func (q *admission) takeQueued() []status.JobID {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	var ids []status.JobID
	for _, w := range q.whole {
		ids = append(ids, w.job)
	}
	for _, qs := range q.roots {
		for _, w := range qs {
			ids = append(ids, w.job)
		}
	}
	q.whole = nil
	clear(q.roots)
	return ids
}

// pendingRoots reports whether a refetch is queued.
func (q *admission) pendingRoots() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.roots) > 0
}

// startWhole takes every queued whole request and PassNow's mark into r and
// makes r the pass in flight; it answers the requesters' jobs.
func (q *admission) startWhole(r *running) []status.JobID {
	q.mu.Lock()
	defer q.mu.Unlock()
	ids := make([]status.JobID, 0, len(q.whole))
	for _, w := range q.whole {
		r.live[w.job] = true
		ids = append(ids, w.job)
	}
	q.whole, q.passNow, q.running = nil, false, r
	return ids
}

// endWhole clears the pass in flight, so a later stop finds nothing.
func (q *admission) endWhole() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.running = nil
}

// takeRoots moves every queued refetch to the batch about to run and
// answers their jobs by root.
func (q *admission) takeRoots() map[mediasource.NativeRef][]status.JobID {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[mediasource.NativeRef][]status.JobID, len(q.roots))
	for ref, qs := range q.roots {
		for _, j := range qs {
			out[ref] = append(out[ref], j.job)
			q.carried[j.job] = true
		}
	}
	clear(q.roots)
	return out
}

// release answers which of ids the batch still carries and lets them go, so
// the caller ends each once.
func (q *admission) release(ids []status.JobID) []status.JobID {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []status.JobID
	for _, id := range ids {
		if q.carried[id] {
			delete(q.carried, id)
			out = append(out, id)
		}
	}
	return out
}

// stop is a requester job's stop handle: a queued request leaves the queue,
// a carried refetch its batch, and a carried whole request its pass,
// cancelling a pass only requests made due once no requester is left. It
// answers *ref, read under mu since Start sets it there, and whether that
// job was found, so the caller ends it.
func (q *admission) stop(ref *status.JobID) (status.JobID, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	id := *ref
	match := func(w queued) bool { return w.job == id }
	if i := slices.IndexFunc(q.whole, match); i >= 0 {
		q.whole = slices.Delete(q.whole, i, i+1)
		return id, true
	}
	for root, qs := range q.roots {
		if i := slices.IndexFunc(qs, match); i >= 0 {
			if qs = slices.Delete(qs, i, i+1); len(qs) == 0 {
				delete(q.roots, root)
			} else {
				q.roots[root] = qs
			}
			return id, true
		}
	}
	if q.carried[id] {
		delete(q.carried, id)
		return id, true
	}
	r := q.running
	if r == nil || !r.live[id] {
		return id, false
	}
	delete(r.live, id)
	if r.requested && len(r.live) == 0 {
		r.cancel()
	}
	return id, true
}
