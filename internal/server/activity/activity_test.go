package activity

import (
	"slices"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// ---- helpers ----

func entryIDs(es []Entry) []string {
	out := make([]string, len(es))
	for i := range es {
		out[i] = es[i].ID
	}
	return out
}

func doneEntry(id string, endedAt time.Time) Entry {
	tt := endedAt
	return Entry{ID: id, Action: "scan", Done: true, EndedAtVal: tt, EndedAt: &tt}
}

// ---- Start ----

// Start hands out monotonically increasing decimal IDs beginning at "1".
func TestActivityLog_Start_incrementsID(t *testing.T) {
	a := New(10)
	id1 := a.Start("scan", "d", SourceManual)
	id2 := a.Start("scan", "d", SourceManual)
	if id1 != "1" {
		t.Errorf("Start() first id = %q, want %q", id1, "1")
	}
	if id2 != "2" {
		t.Errorf("Start() second id = %q, want %q", id2, "2")
	}
}

// Start lazily allocates the ID index on a zero-value Log so later lookups
// (here, End) resolve the entry instead of writing to a nil map.
func TestActivityLog_Start_buildsIndexWhenNil(t *testing.T) {
	a := &Log{maxItems: 5} // index intentionally left nil
	id := a.Start("scan", "d", SourceManual)
	got := a.Entries()
	if len(got) != 1 {
		t.Fatalf("Entries() len = %d, want 1", len(got))
	}
	a.End(id)
	if !a.Entries()[0].Done {
		t.Errorf("after End(%q), entry Done = false, want true (index not built)", id)
	}
}

// AppendEntry allocates the ID index on a zero-value Log before recording the
// entry, so the direct write never hits a nil map.
func TestActivityLog_AppendEntry_allocatesNilIndex(t *testing.T) {
	l := &Log{maxItems: 4} // zero-value index map (nil)
	l.Lock()
	l.AppendEntry(Entry{ID: "x", Action: "scan"})
	l.Unlock()

	entries := l.Entries()
	if len(entries) != 1 || entries[0].ID != "x" {
		t.Fatalf("Entries() = %v, want a single entry with ID %q", entries, "x")
	}
}

// ---- End / Fail ----

// End marks an entry done and stamps EndedAt; Fail does the same and also sets
// the Failed flag.
func TestActivityLog_EndAndFail(t *testing.T) {
	a := New(10)
	endID := a.Start("scan", "d", SourceManual)
	failID := a.Start("scan", "d", SourceManual)

	a.End(endID)
	a.Fail(failID)

	entries := a.Entries() // [endID, failID]
	ended := entries[0]
	if !ended.Done || ended.Failed {
		t.Errorf("End(%q): Done=%v Failed=%v, want Done=true Failed=false", endID, ended.Done, ended.Failed)
	}
	if ended.EndedAt == nil {
		t.Errorf("End(%q): EndedAt = nil, want a timestamp", endID)
	}

	failed := entries[1]
	if !failed.Done || !failed.Failed {
		t.Errorf("Fail(%q): Done=%v Failed=%v, want both true", failID, failed.Done, failed.Failed)
	}
	if failed.EndedAt == nil {
		t.Errorf("Fail(%q): EndedAt = nil, want a timestamp", failID)
	}
}

// ---- Progress ----

// Progress always updates the counters but only overwrites Detail when the new
// detail is non-empty.
func TestActivityLog_Progress_detailOnlyWhenNonEmpty(t *testing.T) {
	a := New(10)
	id := a.Start("scan", "orig", SourceManual)

	a.Progress(id, 1, 5, "updated")
	if got := a.Entries()[0].Detail; got != "updated" {
		t.Errorf("Progress(non-empty): Detail = %q, want %q", got, "updated")
	}

	a.Progress(id, 2, 5, "")
	e := a.Entries()[0]
	if e.Detail != "updated" {
		t.Errorf("Progress(empty) overwrote Detail = %q, want %q", e.Detail, "updated")
	}
	if e.Current != 2 || e.Total != 5 {
		t.Errorf("Progress counters = (%d,%d), want (2,5)", e.Current, e.Total)
	}
}

// ---- SetQueued ----

// SetQueued toggles the queued flag and, when un-queuing, resets StartedAt to
// the moment the work actually began.
func TestActivityLog_SetQueued_resetsStartedAtWhenUnqueued(t *testing.T) {
	a := New(10)
	id := a.Start("scan", "d", SourceManual)

	a.SetQueued(id, true)
	if !a.Entries()[0].Queued {
		t.Errorf("SetQueued(true): Queued = false, want true")
	}

	// Backdate StartedAt, then un-queue: StartedAt must be reset to ~now.
	a.Lock()
	a.EntriesUnsafe()[0].StartedAt = time.Now().Add(-time.Hour)
	a.Unlock()

	before := time.Now()
	a.SetQueued(id, false)
	e := a.Entries()[0]
	if e.Queued {
		t.Errorf("SetQueued(false): Queued = true, want false")
	}
	if e.StartedAt.Before(before) {
		t.Errorf("SetQueued(false): StartedAt = %v, want reset to >= %v", e.StartedAt, before)
	}
}

// ---- Cancel ----

// Cancel succeeds only for an entry that is queued and not yet done; every
// other state (not queued, already done, unknown id) is a no-op returning false.
func TestActivityLog_Cancel_onlyQueuedAndNotDone(t *testing.T) {
	tests := []struct {
		name   string
		queued bool
		done   bool
		want   bool
	}{
		{"queued and not done", true, false, true},
		{"not queued", false, false, false},
		{"queued but done", true, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := New(10)
			id := a.Start("scan", "d", SourceManual)
			a.SetQueued(id, tc.queued)
			if tc.done {
				a.End(id)
			}
			if got := a.Cancel(id); got != tc.want {
				t.Errorf("Cancel() = %v, want %v", got, tc.want)
			}
			if got := a.IsCancelled(id); got != tc.want {
				t.Errorf("IsCancelled() = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("unknown id", func(t *testing.T) {
		a := New(10)
		if a.Cancel("nope") {
			t.Errorf("Cancel(unknown) = true, want false")
		}
	})
}

// ---- Dismiss ----

// Dismiss removes a completed entry and rebuilds the ID index so that entries
// shifted left by the removal still resolve to their own slot.
func TestActivityLog_Dismiss_removesDoneAndReindexes(t *testing.T) {
	a := New(10)
	id1 := a.Start("scan", "a", SourceManual)
	id2 := a.Start("scan", "b", SourceManual)
	id3 := a.Start("scan", "c", SourceManual)
	a.End(id1)
	a.End(id2)
	a.End(id3)

	// Dismiss the first entry; id2 and id3 shift left by one slot.
	a.Dismiss(id1)

	ids := entryIDs(a.Entries())
	if slices.Contains(ids, id1) {
		t.Errorf("Dismiss(%q) did not remove the entry; ids=%v", id1, ids)
	}
	if !slices.Contains(ids, id2) || !slices.Contains(ids, id3) {
		t.Errorf("Dismiss(%q) removed the wrong entries; ids=%v", id1, ids)
	}

	// With a stale index, id2's lookup would land on id3's slot. Probing id2
	// must update id2 and leave id3 untouched.
	a.Progress(id2, 3, 4, "moved")
	entries := a.Entries() // [id2, id3]
	if entries[0].ID != id2 || entries[0].Current != 3 {
		t.Errorf("after Dismiss, Progress(%q) did not update it: %+v", id2, entries[0])
	}
	if entries[1].Current != 0 {
		t.Errorf("after Dismiss, Progress(%q) leaked onto %q: %+v", id2, id3, entries[1])
	}
}

// Dismiss is a no-op for an entry that is not done and for an unknown id.
func TestActivityLog_Dismiss_ignoresNotDoneAndUnknown(t *testing.T) {
	a := New(10)
	a.Start("scan", "a", SourceManual) // not done

	a.Dismiss("1")
	if len(a.Entries()) != 1 {
		t.Errorf("Dismiss(not-done) removed the entry; len = %d, want 1", len(a.Entries()))
	}

	a.Dismiss("nope")
	if len(a.Entries()) != 1 {
		t.Errorf("Dismiss(unknown) changed entries; len = %d, want 1", len(a.Entries()))
	}
}

// ---- PruneCompleted ----

// PruneCompleted drops completed entries older than maxAge, keeps recent
// completed entries, and always keeps ongoing entries.
func TestActivityLog_PruneCompleted_keepsRecentRemovesOld(t *testing.T) {
	a := New(50)
	now := time.Now()

	a.Lock()
	a.AppendEntry(doneEntry("old", now.Add(-1*time.Hour)))
	a.AppendEntry(doneEntry("recent", now.Add(-1*time.Minute)))
	a.AppendEntry(Entry{ID: "ongoing", Action: "scan"})
	a.Unlock()

	a.PruneCompleted(10 * time.Minute)

	ids := entryIDs(a.Entries())
	if slices.Contains(ids, "old") {
		t.Errorf("PruneCompleted kept the 1h-old completed entry; ids=%v", ids)
	}
	if !slices.Contains(ids, "recent") {
		t.Errorf("PruneCompleted removed the 1m-old completed entry; ids=%v", ids)
	}
	if !slices.Contains(ids, "ongoing") {
		t.Errorf("PruneCompleted removed the ongoing entry; ids=%v", ids)
	}
}

// PruneCompleted rebuilds the ID index on a zero-value Log so later lookups
// still resolve after pruning.
func TestActivityLog_PruneCompleted_rebuildsNilIndex(t *testing.T) {
	a := &Log{maxItems: 5}                          // nil index
	a.entries = []Entry{{ID: "k1", Action: "scan"}} // not done -> survives prune

	a.PruneCompleted(time.Minute)

	if got := a.Entries(); len(got) != 1 {
		t.Fatalf("Entries() len = %d, want 1", len(got))
	}
	a.End("k1")
	if !a.Entries()[0].Done {
		t.Errorf("End(%q) via rebuilt index failed: Done = false, want true", "k1")
	}
}

// ---- findEntry ----

// findEntry consults the ID index (last-write-wins) rather than a first-match
// linear scan, so a duplicate ID resolves to the most recently appended slot.
func TestActivityLog_findEntry_prefersIndex(t *testing.T) {
	a := New(10)
	a.Lock()
	a.AppendEntry(Entry{ID: "dup", Action: "first", Cancelled: false})
	a.AppendEntry(Entry{ID: "dup", Action: "second", Cancelled: true})
	a.Unlock()

	if !a.IsCancelled("dup") {
		t.Errorf("IsCancelled(%q) = false, want true (findEntry must use the index, not a first-match scan)", "dup")
	}
}

// ---- properties / concurrency / bench ----

func TestActivityLog_exported_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		log := New(5)
		var activeIDs []string

		ops := rapid.IntRange(1, 30).Draw(t, "ops")
		for range ops {
			op := rapid.IntRange(0, 5).Draw(t, "op")
			switch op {
			case 0: // Start
				id := log.Start("scan", "detail", "manual")
				activeIDs = append(activeIDs, id)
			case 1: // End
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.End(activeIDs[idx])
				}
			case 2: // Fail
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.Fail(activeIDs[idx])
				}
			case 3: // Dismiss
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.Dismiss(activeIDs[idx])
				}
			case 4: // PruneCompleted
				log.PruneCompleted(0)
			case 5: // Cancel
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.Cancel(activeIDs[idx])
				}
			}
		}

		// Invariant 1: Len() <= maxItems.
		entries := log.Entries()
		if len(entries) > 5 {
			t.Fatalf("Entries() len = %d, exceeds maxItems=5", len(entries))
		}

		// Invariant 2: unique IDs.
		seen := make(map[string]bool)
		for _, e := range entries {
			if seen[e.ID] {
				t.Fatalf("duplicate ID %q", e.ID)
			}
			seen[e.ID] = true
		}
	})
}

func TestActivityLog_concurrent_exported(t *testing.T) {
	t.Parallel()
	log := New(10)
	var wg sync.WaitGroup
	done := make(chan struct{})

	// 4 goroutines: Start → Progress → End loop.
	for range 4 {
		wg.Go(func() {
			for i := range 50 {
				id := log.Start("scan", "ep", "scheduled")
				log.Progress(id, i, 50, "working")
				log.End(id)
			}
		})
	}

	// 2 goroutines: Dismiss completed entries.
	for range 2 {
		wg.Go(func() {
			for range 100 {
				entries := log.Entries()
				for _, e := range entries {
					if e.Done {
						log.Dismiss(e.ID)
					}
				}
			}
		})
	}

	// 2 goroutines: PruneCompleted.
	for range 2 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					log.PruneCompleted(0)
				}
			}
		})
	}

	// 1 goroutine: Cancel + IsCancelled.
	wg.Go(func() {
		for range 100 {
			entries := log.Entries()
			for _, e := range entries {
				if !e.Done {
					log.Cancel(e.ID)
					log.IsCancelled(e.ID)
				}
			}
		}
	})

	// 1 goroutine: Entries snapshot reads.
	wg.Go(func() {
		for range 200 {
			_ = log.Entries()
		}
	})

	// Wait for the main workers, then signal prune goroutines.
	time.AfterFunc(200*time.Millisecond, func() { close(done) })
	wg.Wait()

	// Final invariant: no panic, Entries() <= maxItems.
	if len(log.Entries()) > 10 {
		t.Fatalf("final Entries() len = %d, exceeds maxItems=10", len(log.Entries()))
	}
}

func BenchmarkActivityLog_StartEnd(b *testing.B) {
	log := New(100)
	b.ReportAllocs()
	for b.Loop() {
		id := log.Start("scan", "bench", "scheduled")
		log.End(id)
	}
}
