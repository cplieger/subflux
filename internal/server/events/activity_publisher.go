package events

import (
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
)

// ActivityEventMinInterval is the per-activity coalesce window for
// non-terminal activity upserts (the spec's ACTIVITY_EVENT_MIN_MS): a
// progress burst publishes at most one event per activity per window, last
// snapshot wins. Terminal transitions bypass it.
const ActivityEventMinInterval = 1000 * time.Millisecond

// activityTombstones bounds the recently-removed id set behind the
// remove-is-final rule. Drop-oldest, sized well above the activity log's own
// capacity (50 entries) so no single prune or eviction batch can displace a
// tombstone it has just added, and small enough to be a fixed cost.
const activityTombstones = 256

// ActivityPublisher turns the activity log's hook stream into activity SSE
// events. Non-terminal upserts coalesce per activity on a trailing window
// (ActivityEventMinInterval, last snapshot wins). A terminal upsert (the
// entry's Done transition) publishes IMMEDIATELY behind a flush barrier: any
// pending window timer is cancelled and its queued snapshot discarded, so no
// stale progress snapshot can follow the terminal state onto the wire — a
// non-terminal hook that lost the race to the terminal one is dropped for
// the same reason. A remove publishes immediately, clears the activity's
// coalescer state, and is FINAL: no upsert for that id is ever published
// again.
//
// Safe for concurrent use; publishes are serialized under the internal mutex,
// so no two events interleave. Wire order is NOT the log's decision order and
// cannot be: the log fires its hooks after releasing its own lock, so two
// mutators of one entry (a terminal End and a Dismiss) can reach this
// publisher in either order. What is guaranteed per activity is that both
// finalities hold whichever way that race lands — nothing follows a remove,
// and no non-terminal snapshot follows a terminal one.
type ActivityPublisher struct {
	publish func(Event)
	pending map[string]*pendingActivity
	// removed and removedRing are the bounded tombstone set: the map answers
	// the drop test, the ring evicts oldest-first at activityTombstones.
	// Activity ids are monotonic decimal strings never reused within a
	// process, so an evicted tombstone can never be needed by a live entry —
	// only by a straggler from the same after-unlock window, which is a few
	// instructions wide.
	removed     map[string]struct{}
	removedRing []string
	removedNext int
	min         time.Duration
	mu          sync.Mutex
}

// pendingActivity is one activity's coalescer state: the trailing window
// timer, the last queued snapshot, and whether a terminal upsert has been
// published (after which non-terminal stragglers are dropped until the
// remove clears the state).
type pendingActivity struct {
	timer    *time.Timer
	queued   *activity.Entry
	terminal bool
}

// NewActivityPublisher returns a publisher feeding events into publish
// (normally EventBus.Publish), coalescing at ActivityEventMinInterval.
func NewActivityPublisher(publish func(Event)) *ActivityPublisher {
	return &ActivityPublisher{
		publish:     publish,
		pending:     make(map[string]*pendingActivity),
		removed:     make(map[string]struct{}, activityTombstones),
		removedRing: make([]string, activityTombstones),
		min:         ActivityEventMinInterval,
	}
}

// Upsert observes one post-mutation entry snapshot (the Log's OnUpsert
// hook, adapted by the server). By pointer to keep the entry off the
// argument copy; the pointee is the hook's own snapshot and may be retained
// as the queued last-wins candidate.
func (p *ActivityPublisher) Upsert(e *activity.Entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, gone := p.removed[e.ID]; gone {
		// The id's remove is already on the wire. This upsert is a straggler
		// from the after-unlock window — a Dismiss and the entry's own
		// terminal transition mutating one entry in either order — and
		// publishing it would resurrect a removed row on every client and
		// re-create coalescer state no later hook can ever clear.
		return
	}
	st := p.pending[e.ID]
	if e.Done {
		// Terminal: flush barrier, then one immediate terminal upsert.
		if st == nil {
			st = &pendingActivity{}
			p.pending[e.ID] = st
		}
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
		st.queued = nil
		st.terminal = true
		p.publish(Event{Type: ActivityDelta, Data: ActivityEvent{Op: ActivityUpsert, Entry: e}})
		return
	}
	if st != nil && st.terminal {
		// A non-terminal mutation that lost the after-unlock race to the
		// terminal transition; publishing it would regress the entry.
		return
	}
	if st == nil {
		st = &pendingActivity{}
		p.pending[e.ID] = st
	}
	st.queued = e
	if st.timer == nil {
		st.timer = time.AfterFunc(p.min, func() { p.flush(e.ID) })
	}
}

// Remove observes one removal snapshot (the Log's OnRemove hook, adapted by
// the server): the activity's coalescer state is dropped, the id is
// tombstoned so no later upsert can resurrect the row, and the remove is
// published at once.
func (p *ActivityPublisher) Remove(e *activity.Entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st := p.pending[e.ID]; st != nil {
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(p.pending, e.ID)
	}
	p.tombstone(e.ID)
	p.publish(Event{Type: ActivityDelta, Data: ActivityEvent{Op: ActivityRemove, Entry: e}})
}

// tombstone records that id's remove has gone on the wire, evicting the
// oldest tombstone once the ring is full. Caller must hold the mutex.
func (p *ActivityPublisher) tombstone(id string) {
	if _, ok := p.removed[id]; ok {
		return
	}
	if old := p.removedRing[p.removedNext]; old != "" {
		delete(p.removed, old)
	}
	p.removedRing[p.removedNext] = id
	p.removedNext = (p.removedNext + 1) % len(p.removedRing)
	p.removed[id] = struct{}{}
}

// flush publishes the window's last queued snapshot when its timer fires.
func (p *ActivityPublisher) flush(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.pending[id]
	if st == nil {
		return
	}
	st.timer = nil
	if st.terminal {
		// Barrier already ran (timer lost the Stop race); nothing to send.
		return
	}
	if e := st.queued; e != nil {
		st.queued = nil
		p.publish(Event{Type: ActivityDelta, Data: ActivityEvent{Op: ActivityUpsert, Entry: e}})
	}
	delete(p.pending, id)
}
