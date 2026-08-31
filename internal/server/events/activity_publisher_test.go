package events

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
)

// recorder captures published events; the mutex covers the AfterFunc timer
// goroutines the publisher fires on. Reads happen after synctest.Wait, when
// the bubble is quiescent.
type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) publish(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) all() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// activityDeltas projects the recorded events into (op, entry) pairs,
// failing on anything that is not an activity event.
func activityDeltas(t *testing.T, events []Event) []ActivityEvent {
	t.Helper()
	out := make([]ActivityEvent, 0, len(events))
	for _, e := range events {
		if e.Type != ActivityDelta {
			t.Fatalf("published event type = %q, want %q", e.Type, ActivityDelta)
		}
		ae, ok := e.Data.(ActivityEvent)
		if !ok {
			t.Fatalf("published event data = %T, want ActivityEvent", e.Data)
		}
		out = append(out, ae)
	}
	return out
}

// A progress burst coalesces to ONE event per activity per window, carrying
// the LAST snapshot; two activities' windows are independent.
func TestActivityPublisher_burst_one_event_per_activity_last_snapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec recorder
		p := NewActivityPublisher(rec.publish)

		for i := range 10 {
			p.Upsert(&activity.Entry{ID: "1", Current: i, Total: 10})
			p.Upsert(&activity.Entry{ID: "2", Current: i * 2, Total: 20})
			time.Sleep(90 * time.Millisecond)
		}
		if got := len(rec.all()); got != 0 {
			t.Fatalf("published %d events inside the coalesce window, want 0 (trailing coalescer)", got)
		}
		time.Sleep(ActivityEventMinInterval)
		synctest.Wait()

		deltas := activityDeltas(t, rec.all())
		if len(deltas) != 2 {
			t.Fatalf("published %d events after one window, want 2 (one per activity)", len(deltas))
		}
		byID := map[string]ActivityEvent{}
		for _, d := range deltas {
			if d.Op != ActivityUpsert {
				t.Errorf("op = %q, want %q", d.Op, ActivityUpsert)
			}
			byID[d.Entry.ID] = d
		}
		if got := byID["1"].Entry.Current; got != 9 {
			t.Errorf("activity 1 snapshot Current = %d, want 9 (last wins)", got)
		}
		if got := byID["2"].Entry.Current; got != 18 {
			t.Errorf("activity 2 snapshot Current = %d, want 18 (last wins)", got)
		}
	})
}

// A sustained burst never publishes twice within one window for one
// activity: publishes are spaced at least ActivityEventMinInterval apart.
func TestActivityPublisher_sustained_burst_spaces_windows(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var (
			mu    sync.Mutex
			times []time.Time
		)
		p := NewActivityPublisher(func(Event) {
			mu.Lock()
			defer mu.Unlock()
			times = append(times, time.Now())
		})

		for i := range 25 { // 3.75s of progress at 150ms spacing
			p.Upsert(&activity.Entry{ID: "1", Current: i})
			time.Sleep(150 * time.Millisecond)
		}
		time.Sleep(ActivityEventMinInterval)
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		if len(times) < 2 {
			t.Fatalf("published %d events over a sustained burst, want several windows", len(times))
		}
		for i := 1; i < len(times); i++ {
			if gap := times[i].Sub(times[i-1]); gap < ActivityEventMinInterval {
				t.Errorf("publishes %d and %d are %v apart, want >= %v", i-1, i, gap, ActivityEventMinInterval)
			}
		}
	})
}

// A terminal transition publishes immediately, without waiting for a window.
func TestActivityPublisher_terminal_publishes_immediately(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec recorder
		p := NewActivityPublisher(rec.publish)

		p.Upsert(&activity.Entry{ID: "1", Done: true})

		deltas := activityDeltas(t, rec.all())
		if len(deltas) != 1 {
			t.Fatalf("published %d events on a terminal upsert, want 1 immediate", len(deltas))
		}
		if !deltas[0].Entry.Done || deltas[0].Op != ActivityUpsert {
			t.Errorf("terminal event = op %q done %v, want upsert with Done=true", deltas[0].Op, deltas[0].Entry.Done)
		}
	})
}

// The flush barrier (the fake-clock fixture): progress at t=0 queues, the
// terminal transition at t=100ms cancels the pending window and discards the
// queued snapshot, and advancing past the window publishes NO stale snapshot
// after the terminal upsert.
func TestActivityPublisher_flush_barrier_no_stale_snapshot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec recorder
		p := NewActivityPublisher(rec.publish)

		p.Upsert(&activity.Entry{ID: "1", Current: 4, Total: 10}) // t=0: queued, window armed
		time.Sleep(100 * time.Millisecond)
		p.Upsert(&activity.Entry{ID: "1", Current: 5, Total: 10, Done: true}) // t=100ms: barrier + terminal

		time.Sleep(2 * ActivityEventMinInterval) // past the t=0 window
		synctest.Wait()

		deltas := activityDeltas(t, rec.all())
		if len(deltas) != 1 {
			t.Fatalf("published %d events, want exactly 1 (the terminal upsert; the queued snapshot is discarded)", len(deltas))
		}
		if !deltas[0].Entry.Done {
			t.Errorf("the one published event has Done=%v, want true (no stale progress snapshot)", deltas[0].Entry.Done)
		}
	})
}

// A non-terminal hook that fires after the terminal one (the after-unlock
// race between two mutators) is dropped rather than re-queued.
func TestActivityPublisher_non_terminal_straggler_after_terminal_dropped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec recorder
		p := NewActivityPublisher(rec.publish)

		p.Upsert(&activity.Entry{ID: "1", Done: true})
		p.Upsert(&activity.Entry{ID: "1", Current: 3}) // stale progress, lost the race

		time.Sleep(2 * ActivityEventMinInterval)
		synctest.Wait()

		deltas := activityDeltas(t, rec.all())
		if len(deltas) != 1 || !deltas[0].Entry.Done {
			t.Fatalf("events after a post-terminal straggler = %+v, want only the terminal upsert", deltas)
		}
	})
}

// A remove publishes immediately, after the terminal upsert, and cancels any
// pending window for the entry.
func TestActivityPublisher_remove_publishes_after_terminal_and_cancels_window(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec recorder
		p := NewActivityPublisher(rec.publish)

		p.Upsert(&activity.Entry{ID: "1", Current: 1}) // pending window
		p.Upsert(&activity.Entry{ID: "1", Done: true}) // barrier + terminal
		p.Remove(&activity.Entry{ID: "1", Done: true}) // dismissed
		p.Upsert(&activity.Entry{ID: "2", Current: 1}) // unrelated pending window
		p.Remove(&activity.Entry{ID: "2", Current: 1}) // removed before its window fired
		time.Sleep(2 * ActivityEventMinInterval)
		synctest.Wait()

		deltas := activityDeltas(t, rec.all())
		var ops []ActivityOp
		for _, d := range deltas {
			ops = append(ops, d.Op)
		}
		want := []ActivityOp{ActivityUpsert, ActivityRemove, ActivityRemove}
		if len(deltas) != 3 || ops[0] != want[0] || ops[1] != want[1] || ops[2] != want[2] {
			t.Fatalf("ops = %v, want %v (terminal upsert, then removes; no window fires after a remove)", ops, want)
		}
		if deltas[1].Entry.ID != "1" || deltas[2].Entry.ID != "2" {
			t.Errorf("remove entries = %q, %q, want 1, 2 (removes carry the entry snapshot)",
				deltas[1].Entry.ID, deltas[2].Entry.ID)
		}
	})
}
