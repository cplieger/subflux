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
	return Entry{ID: id, Action: "scan", Done: true, EndedAt: &tt}
}

// appendEntry inserts an entry directly — test setup for states the public
// API cannot produce (backdated EndedAt, duplicate IDs) — maintaining the
// ID index the way startLocked does.
func appendEntry(a *Log, e Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	if a.index == nil {
		a.index = make(map[string]int, a.maxItems)
	}
	a.index[e.ID] = len(a.entries) - 1
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
	a.mu.Lock()
	a.entries[0].StartedAt = time.Now().Add(-time.Hour)
	a.mu.Unlock()

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
// completed entries and ongoing entries, and preserves their relative order.
func TestActivityLog_PruneCompleted_keepsRecentRemovesOld(t *testing.T) {
	a := New(50)
	now := time.Now()

	appendEntry(a, doneEntry("old", now.Add(-1*time.Hour)))
	appendEntry(a, doneEntry("recent", now.Add(-1*time.Minute)))
	appendEntry(a, Entry{ID: "ongoing", Action: "scan"})

	a.PruneCompleted(10 * time.Minute)

	ids := entryIDs(a.Entries())
	if !slices.Equal(ids, []string{"recent", "ongoing"}) {
		t.Errorf("PruneCompleted survivors = %v, want [recent ongoing] (old removed, order preserved)", ids)
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
// Duplicate IDs are a state the public API cannot produce, so the entries are
// planted via the in-package appendEntry helper.
func TestActivityLog_findEntry_prefersIndex(t *testing.T) {
	a := New(10)
	appendEntry(a, Entry{ID: "dup", Action: "first", Cancelled: false})
	appendEntry(a, Entry{ID: "dup", Action: "second", Cancelled: true})

	if !a.IsCancelled("dup") {
		t.Errorf("IsCancelled(%q) = false, want true (findEntry must use the index, not a first-match scan)", "dup")
	}
}

// ---- ActiveScan ----

// ActiveScan resolves the live entry for a scope — the read-only half of
// StartScan's idempotency, used by the full-scan duplicate-start answer —
// and ignores terminal and cancelled entries.
func TestActivityLog_ActiveScan(t *testing.T) {
	a := New(10)
	scope := ScanScope{Kind: ScanKindFull}

	if id, ok := a.ActiveScan(scope); ok {
		t.Fatalf("ActiveScan(empty log) = %q, true; want none", id)
	}

	started, _ := a.StartScan("Full Scan", "d", SourceScheduled, scope, "admin")
	id, ok := a.ActiveScan(scope)
	if !ok || id != started {
		t.Errorf("ActiveScan = %q, %v; want the running entry %q", id, ok, started)
	}
	if otherID, ok := a.ActiveScan(ScanScope{Kind: ScanKindSeries, MediaID: 1}); ok {
		t.Errorf("ActiveScan(different scope) = %q, true; want none", otherID)
	}

	a.End(started)
	if id, ok := a.ActiveScan(scope); ok {
		t.Errorf("ActiveScan(after End) = %q, true; want none (terminal entries are not active)", id)
	}
}

// ---- snapshot isolation: EndedAt must never alias internal storage ----

// Mutating a returned snapshot — including through its EndedAt pointer —
// must never change the log's internal state.
func TestActivityLog_snapshot_EndedAt_mutation_does_not_corrupt_log(t *testing.T) {
	a := New(10)
	id := a.Start("scan", "d", SourceManual)
	a.End(id)

	orig, ok := a.Get(id)
	if !ok || orig.EndedAt == nil {
		t.Fatalf("Get(%q) = %+v, %v; want a terminal entry with EndedAt", id, orig, ok)
	}
	want := *orig.EndedAt

	// Hostile caller writes through the Get snapshot's pointer.
	snap, _ := a.Get(id)
	*snap.EndedAt = snap.EndedAt.Add(-time.Hour)
	fresh, _ := a.Get(id)
	if !fresh.EndedAt.Equal(want) {
		t.Errorf("internal EndedAt = %v after mutating a Get snapshot, want %v (snapshot aliases storage)",
			fresh.EndedAt, want)
	}

	// Same through an Entries snapshot.
	ents := a.Entries()
	*ents[0].EndedAt = ents[0].EndedAt.Add(time.Hour)
	fresh, _ = a.Get(id)
	if !fresh.EndedAt.Equal(want) {
		t.Errorf("internal EndedAt = %v after mutating an Entries snapshot, want %v (snapshot aliases storage)",
			fresh.EndedAt, want)
	}

	// Every snapshot is its own allocation — never the same pointer twice.
	again, _ := a.Get(id)
	if again.EndedAt == snap.EndedAt {
		t.Error("two Get snapshots share one EndedAt allocation")
	}
}

// A snapshot taken BEFORE compaction (Dismiss) and append-into-freed-capacity
// keeps reading its own terminal time: with pointer-into-backing-array
// storage, the row shift plus append reuse would expose another row's end
// time through the stale pointer.
func TestActivityLog_snapshot_EndedAt_survives_compaction_and_append(t *testing.T) {
	a := New(4)
	id1 := a.Start("scan", "a", SourceManual)
	id2 := a.Start("scan", "b", SourceManual)
	a.End(id1)
	a.End(id2)

	snap2, ok := a.Get(id2)
	if !ok || snap2.EndedAt == nil {
		t.Fatalf("Get(%q) missing terminal entry", id2)
	}
	want := *snap2.EndedAt

	a.Dismiss(id1) // compaction: id2's row shifts left in the backing array

	id3 := a.Start("scan", "c", SourceManual) // append reuses freed capacity
	a.End(id3)

	if !snap2.EndedAt.Equal(want) {
		t.Errorf("pre-compaction snapshot EndedAt = %v, want %v (reads another row's storage after shift+append)",
			snap2.EndedAt, want)
	}
	fresh, _ := a.Get(id2)
	if !fresh.EndedAt.Equal(want) {
		t.Errorf("entry %q EndedAt = %v after compaction+append, want %v", id2, fresh.EndedAt, want)
	}
}

// ---- properties / concurrency / bench ----

func TestActivityLog_exported_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		log := New(5)
		var activeIDs []string
		// Model for the capacity invariant: running entries are never
		// evicted, so the log may exceed maxItems only up to the peak
		// number of simultaneously-running entries.
		terminal := make(map[string]bool)
		running, peakRunning := 0, 0

		ops := rapid.IntRange(1, 30).Draw(t, "ops")
		for range ops {
			op := rapid.IntRange(0, 5).Draw(t, "op")
			switch op {
			case 0: // Start
				id := log.Start("scan", "detail", "manual")
				activeIDs = append(activeIDs, id)
				running++
				peakRunning = max(peakRunning, running)
			case 1: // End
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.End(activeIDs[idx])
					if !terminal[activeIDs[idx]] {
						terminal[activeIDs[idx]] = true
						running--
					}
				}
			case 2: // Fail
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.Fail(activeIDs[idx])
					if !terminal[activeIDs[idx]] {
						terminal[activeIDs[idx]] = true
						running--
					}
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

		// Invariant 1: Len() <= max(maxItems, peak simultaneous running).
		// Running entries survive capacity pressure by design (a busy
		// system must not hide a live cancellable scan), so overflow is
		// bounded by the running peak, never unbounded.
		entries := log.Entries()
		if bound := max(5, peakRunning); len(entries) > bound {
			t.Fatalf("Entries() len = %d, exceeds max(maxItems=5, peakRunning=%d)", len(entries), peakRunning)
		}

		// Invariant 1b: every never-terminal entry survives (running
		// entries are never ring-evicted; Dismiss only removes done rows).
		present := make(map[string]bool, len(entries))
		for _, e := range entries {
			present[e.ID] = true
		}
		for _, id := range activeIDs {
			if !terminal[id] && !present[id] {
				t.Fatalf("running entry %q was evicted", id)
			}
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

// ---- upsert / remove hooks (status events, E1) ----

// The upsert hook observes every mutation with the post-mutation snapshot,
// fired after the lock is released — proven by re-entering the log from
// inside the hook, which would deadlock if it ran under the write lock.
func TestActivityLog_onUpsert_fires_per_mutation_after_unlock(t *testing.T) {
	a := New(10)
	var got []Entry
	a.SetOnUpsert(func(e Entry) {
		if _, ok := a.Get(e.ID); !ok {
			t.Errorf("hook re-entry: entry %q not readable from inside the hook", e.ID)
		}
		got = append(got, e)
	})

	id := a.Start("Scan", "d", SourceManual)
	a.Progress(id, 3, 10, "working")
	a.End(id)

	if len(got) != 3 {
		t.Fatalf("upsert hook fired %d times for start+progress+end, want 3", len(got))
	}
	if got[0].ID != id || got[0].Done {
		t.Errorf("start snapshot = id %q done %v, want %q running", got[0].ID, got[0].Done, id)
	}
	if got[1].Current != 3 || got[1].Detail != "working" {
		t.Errorf("progress snapshot = current %d detail %q, want 3 %q", got[1].Current, got[1].Detail, "working")
	}
	if !got[2].Done || got[2].EndedAt == nil {
		t.Errorf("end snapshot = done %v endedAt %v, want terminal", got[2].Done, got[2].EndedAt)
	}
}

// Fail and FinishCancelled snapshots carry their full terminal state (the
// hook must fire after the whole mutation, not mid-way through finish).
func TestActivityLog_onUpsert_terminal_snapshots_complete(t *testing.T) {
	a := New(10)
	var got []Entry
	a.SetOnUpsert(func(e Entry) { got = append(got, e) })

	a.Fail(a.Start("Scan", "d", SourceManual))
	a.FinishCancelled(a.Start("Scan", "d", SourceManual))

	if len(got) != 4 {
		t.Fatalf("upsert hook fired %d times, want 4", len(got))
	}
	if !got[1].Done || !got[1].Failed {
		t.Errorf("Fail snapshot = done %v failed %v, want both true", got[1].Done, got[1].Failed)
	}
	if !got[3].Done || !got[3].Cancelled {
		t.Errorf("FinishCancelled snapshot = done %v cancelled %v, want both true", got[3].Done, got[3].Cancelled)
	}
}

// A queued-cancel is an upsert (the entry stays in the log, flagged); a
// StartScan hitting an existing scope mutates nothing and fires nothing.
func TestActivityLog_onUpsert_cancel_and_idempotent_startScan(t *testing.T) {
	a := New(10)
	count := 0
	a.SetOnUpsert(func(Entry) { count++ })

	id, _ := a.StartScan("Scan", "d", SourceManual, ScanScope{Kind: ScanKindFull}, "admin")
	a.SetQueued(id, true)
	if !a.Cancel(id) {
		t.Fatal("Cancel(queued) = false, want true")
	}
	if count != 3 {
		t.Errorf("upsert hook fired %d times for start+queue+cancel, want 3", count)
	}

	if _, existing := a.StartScan("Scan", "d", SourceManual, ScanScope{Kind: ScanKindFull}, "admin"); existing {
		t.Fatal("second StartScan existing = true; scope should be startable again (cancelled)")
	}
	before := count
	a.Progress("no-such-id", 1, 2, "x")
	if count != before {
		t.Errorf("hook count after miss-Progress = %d, want %d (a no-op mutation fires nothing)", count, before)
	}
}

// The remove hook fires on DISMISS with the under-lock snapshot, after
// unlock (the hook re-enters the log).
func TestActivityLog_onRemove_fires_on_dismiss_after_unlock(t *testing.T) {
	a := New(10)
	var removed []Entry
	a.SetOnRemove(func(e Entry) {
		_ = a.Entries() // re-entry: deadlocks if fired under the write lock
		removed = append(removed, e)
	})

	id := a.Start("Scan", "d", SourceManual)
	a.End(id)
	a.Dismiss(id)

	if len(removed) != 1 {
		t.Fatalf("remove hook fired %d times on dismiss, want 1", len(removed))
	}
	if removed[0].ID != id || !removed[0].Done {
		t.Errorf("dismiss snapshot = id %q done %v, want %q true", removed[0].ID, removed[0].Done, id)
	}
}

// Dismissing a running entry removes nothing and fires nothing.
func TestActivityLog_onRemove_not_fired_for_running_dismiss(t *testing.T) {
	a := New(10)
	fired := false
	a.SetOnRemove(func(Entry) { fired = true })

	a.Dismiss(a.Start("Scan", "d", SourceManual))

	if fired {
		t.Error("remove hook fired for a running entry's dismiss; dismiss only removes Done entries")
	}
}

// The remove hook fires on PRUNE, once per pruned entry, with snapshots
// collected under the lock.
func TestActivityLog_onRemove_fires_on_prune(t *testing.T) {
	a := New(10)
	old := time.Now().Add(-20 * time.Minute)
	appendEntry(a, doneEntry("old1", old))
	appendEntry(a, doneEntry("old2", old))
	appendEntry(a, Entry{ID: "live", Action: "scan"})
	var removed []string
	a.SetOnRemove(func(e Entry) { removed = append(removed, e.ID) })

	a.PruneCompleted(10 * time.Minute)

	if !slices.Equal(removed, []string{"old1", "old2"}) {
		t.Errorf("prune removes = %v, want [old1 old2]", removed)
	}
}

// The remove hook fires on CAP EVICTION, with the evicted entry's snapshot,
// after unlock (the hook re-enters the log mid-Start).
func TestActivityLog_onRemove_fires_on_cap_eviction(t *testing.T) {
	a := New(1)
	var removed []Entry
	a.SetOnRemove(func(e Entry) {
		_ = a.Entries() // re-entry: deadlocks if fired under the write lock
		removed = append(removed, e)
	})

	a.End(a.Start("Scan", "first", SourceManual))
	a.Start("Scan", "second", SourceManual)

	if len(removed) != 1 {
		t.Fatalf("remove hook fired %d times on cap eviction, want 1", len(removed))
	}
	if removed[0].ID != "1" || !removed[0].Done {
		t.Errorf("evicted snapshot = id %q done %v, want id 1, done", removed[0].ID, removed[0].Done)
	}
}
