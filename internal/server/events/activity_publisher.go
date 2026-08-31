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

// ActivityPublisher turns the activity log's hook stream into activity SSE
// events. Non-terminal upserts coalesce per activity on a trailing window
// (ActivityEventMinInterval, last snapshot wins). A terminal upsert (the
// entry's Done transition) publishes IMMEDIATELY behind a flush barrier: any
// pending window timer is cancelled and its queued snapshot discarded, so no
// stale progress snapshot can follow the terminal state onto the wire — a
// non-terminal hook that lost the race to the terminal one is dropped for
// the same reason. A remove publishes immediately and clears the activity's
// coalescer state.
//
// Safe for concurrent use; publishes are serialized under the internal
// mutex, so per-activity wire order matches decision order.
type ActivityPublisher struct {
	publish func(Event)
	pending map[string]*pendingActivity
	min     time.Duration
	mu      sync.Mutex
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
		publish: publish,
		pending: make(map[string]*pendingActivity),
		min:     ActivityEventMinInterval,
	}
}

// Upsert observes one post-mutation entry snapshot (the Log's OnUpsert
// hook, adapted by the server). By pointer to keep the entry off the
// argument copy; the pointee is the hook's own snapshot and may be retained
// as the queued last-wins candidate.
func (p *ActivityPublisher) Upsert(e *activity.Entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
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
// the server): the activity's coalescer state is dropped and the remove
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
	p.publish(Event{Type: ActivityDelta, Data: ActivityEvent{Op: ActivityRemove, Entry: e}})
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
