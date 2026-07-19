package server

import (
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
)

// The activity.Log assertions below observe state exclusively through the
// public snapshot API (Get/Entries): the exported test-only lock/unsafe
// accessors were removed so no caller can bypass the Log's invariants.
// States only internal mutation could produce (backdated EndedAt) are
// covered by the activity package's own tests.

// --- activity.Log.setQueued ---

func TestActivityLog_setQueued_marks_queued(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	al.SetQueued(id, true)

	e, ok := al.Get(id)
	if !ok {
		t.Fatal("entry not found")
	}
	if !e.Queued {
		t.Error("entry should be queued after setQueued(true)")
	}
}

func TestActivityLog_setQueued_unqueue_resets_start_time(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	originalStart := al.Entries()[0].StartedAt

	al.SetQueued(id, true)
	al.SetQueued(id, false)

	e, _ := al.Get(id)
	if e.Queued {
		t.Error("entry should not be queued after setQueued(false)")
	}
	if e.StartedAt.Before(originalStart) {
		t.Error("StartedAt should be >= original after unqueue")
	}
}

func TestActivityLog_setQueued_nonexistent_id_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	al.Start("Scan", "detail", "manual")

	al.SetQueued("nonexistent", true)

	if al.Entries()[0].Queued {
		t.Error("entry should not be affected by nonexistent ID")
	}
}

// --- activity.Log.dismiss ---

func TestActivityLog_dismiss_removes_completed_entry(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")
	al.End(id)

	al.Dismiss(id)

	if n := len(al.Entries()); n != 0 {
		t.Errorf("entries count = %d after dismiss, want 0", n)
	}
}

func TestActivityLog_dismiss_does_not_remove_active_entry(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	al.Dismiss(id)

	if n := len(al.Entries()); n != 1 {
		t.Errorf("entries count = %d, want 1 (active entry not dismissed)", n)
	}
}

func TestActivityLog_dismiss_nonexistent_id_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")
	al.End(id)

	al.Dismiss("nonexistent")

	if n := len(al.Entries()); n != 1 {
		t.Errorf("entries count = %d, want 1 (nonexistent ID)", n)
	}
}

// --- activity.Log.cancel ---

func TestActivityLog_cancel_queued_entry(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")
	al.SetQueued(id, true)

	ok := al.Cancel(id)
	if !ok {
		t.Error("cancel() returned false for queued entry")
	}

	e, _ := al.Get(id)
	if !e.Cancelled {
		t.Error("entry should be cancelled")
	}
}

func TestActivityLog_cancel_non_queued_returns_false(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	ok := al.Cancel(id)
	if ok {
		t.Error("cancel() should return false for non-queued entry")
	}
}

func TestActivityLog_cancel_done_entry_returns_false(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")
	al.SetQueued(id, true)
	al.End(id)

	ok := al.Cancel(id)
	if ok {
		t.Error("cancel() should return false for done entry")
	}
}

func TestActivityLog_cancel_nonexistent_returns_false(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	ok := al.Cancel("nonexistent")
	if ok {
		t.Error("cancel() should return false for nonexistent ID")
	}
}

// --- activity.Log.isCancelled ---

func TestActivityLog_isCancelled_true_after_cancel(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")
	al.SetQueued(id, true)
	al.Cancel(id)

	if !al.IsCancelled(id) {
		t.Error("isCancelled() = false after cancel, want true")
	}
}

func TestActivityLog_isCancelled_false_by_default(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	if al.IsCancelled(id) {
		t.Error("isCancelled() = true for non-cancelled entry, want false")
	}
}

func TestActivityLog_isCancelled_nonexistent_returns_false(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	if al.IsCancelled("nonexistent") {
		t.Error("isCancelled() = true for nonexistent ID, want false")
	}
}

// --- activity.Log.fail ---

func TestActivityLog_fail_marks_done_and_failed(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	al.Fail(id)

	e, _ := al.Get(id)
	if !e.Done {
		t.Error("entry should be done after fail()")
	}
	if !e.Failed {
		t.Error("entry should be failed after fail()")
	}
	if e.EndedAt == nil {
		t.Error("entry.EndedAt should be set after fail()")
	}
}

func TestActivityLog_fail_nonexistent_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	al.Fail("nonexistent")

	e, _ := al.Get(id)
	if e.Done || e.Failed {
		t.Errorf("entry %q should not be affected by fail(nonexistent)", id)
	}
}

// --- activity.Log.pruneCompleted ---
//
// The removes-old and mixed-age cases need a backdated EndedAt, which the
// public API deliberately cannot produce; they live in the activity
// package's own tests (TestActivityLog_PruneCompleted_keepsRecentRemovesOld).

func TestActivityLog_pruneCompleted_keeps_recent_done_entries(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "recent scan", "scheduled")
	al.End(id)

	al.PruneCompleted(15 * time.Minute)

	if n := len(al.Entries()); n != 1 {
		t.Errorf("entries count = %d after pruneCompleted, want 1 (recent done entry kept)", n)
	}
}

func TestActivityLog_pruneCompleted_keeps_active_entries(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	al.Start("Scan", "active scan", "scheduled")

	al.PruneCompleted(0) // maxAge=0 means prune everything completed

	if n := len(al.Entries()); n != 1 {
		t.Errorf("entries count = %d after pruneCompleted, want 1 (active entry kept)", n)
	}
}

func TestActivityLog_pruneCompleted_empty_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	al.PruneCompleted(15 * time.Minute)

	if n := len(al.Entries()); n != 0 {
		t.Errorf("entries count = %d, want 0", n)
	}
}

// --- activity.Log.start maxItems boundary ---

func TestActivityLog_start_exact_maxItems(t *testing.T) {
	t.Parallel()
	al := activity.New(2)

	idA := al.Start("A", "first", "scheduled")
	al.Start("B", "second", "scheduled")

	// At exactly maxItems, should NOT trim.
	if n := len(al.Entries()); n != 2 {
		t.Errorf("entries count = %d after 2 inserts with maxItems=2, want 2", n)
	}

	// Over capacity with a completed entry present: the oldest COMPLETED
	// entry is evicted (running entries are never trimmed).
	al.End(idA)
	al.Start("C", "third", "scheduled")

	entries := al.Entries()
	if len(entries) != 2 {
		t.Errorf("entries count = %d after 3 inserts with maxItems=2, want 2", len(entries))
	}
	if entries[0].Action != "B" {
		t.Errorf("entries[0].Action = %q after trim, want %q", entries[0].Action, "B")
	}
}

func TestActivityLog_start_never_evicts_running(t *testing.T) {
	t.Parallel()
	al := activity.New(2)

	// Three running entries with maxItems=2: none may be evicted — a busy
	// system must not hide a live cancellable scan.
	al.Start("A", "first", "scheduled")
	al.Start("B", "second", "scheduled")
	al.Start("C", "third", "scheduled")

	entries := al.Entries()
	if len(entries) != 3 {
		t.Fatalf("entries count = %d, want 3 (running entries survive capacity pressure)", len(entries))
	}
	for i, want := range []string{"A", "B", "C"} {
		if got := entries[i].Action; got != want {
			t.Errorf("entries[%d].Action = %q, want %q", i, got, want)
		}
	}
}

// --- activity.AlertLog.record max boundary ---

func TestAlertLog_record_exact_max(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(2)

	al.Record("a", "first")
	al.Record("b", "second")

	al.RLock()
	count := len(al.AlertsUnsafe())
	al.RUnlock()

	// At exactly max, should NOT trim.
	if count != 2 {
		t.Errorf("alerts count = %d after 2 inserts with max=2, want 2", count)
	}

	// One more should trigger trim.
	al.Record("c", "third")

	al.RLock()
	count = len(al.AlertsUnsafe())
	first := al.AlertsUnsafe()[0].Source
	al.RUnlock()

	if count != 2 {
		t.Errorf("alerts count = %d after 3 inserts with max=2, want 2", count)
	}
	if first != "b" {
		t.Errorf("alerts[0].Source = %q after trim, want %q", first, "b")
	}
}

// --- activity.AlertLog.recordWarn and recordInfo ---

func TestAlertLog_recordWarn_sets_warn_level(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordWarn("sonarr", "warning message")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 1 {
		t.Fatalf("alerts count = %d, want 1", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[0].Level != "warn" {
		t.Errorf("alert.Level = %q, want %q", al.AlertsUnsafe()[0].Level, "warn")
	}
	if al.AlertsUnsafe()[0].Kind != activity.AlertTransient {
		t.Errorf("alert.Kind = %q, want %q", al.AlertsUnsafe()[0].Kind, activity.AlertTransient)
	}
}

func TestAlertLog_recordInfo_sets_info_level_with_short_ttl(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordInfo("scan complete")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 1 {
		t.Fatalf("alerts count = %d, want 1", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[0].Level != "info" {
		t.Errorf("alert.Level = %q, want %q", al.AlertsUnsafe()[0].Level, "info")
	}
	if al.AlertsUnsafe()[0].TTL == 0 {
		t.Error("alert.TTL should be non-zero for info alerts")
	}
}

// --- visibleAlerts respects per-alert TTL ---

func TestVisibleAlerts_respects_custom_ttl(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	// Add an info alert with a very short TTL that has already expired.
	al.RecordInfo("old info")
	al.Lock()
	// Backdate the alert so its 10-minute TTL has expired.
	al.AlertsUnsafe()[0].Time = al.AlertsUnsafe()[0].Time.Add(-15 * time.Minute)
	al.Unlock()

	visible := al.VisibleAlerts()
	if len(visible) != 0 {
		t.Errorf("visibleAlerts() returned %d alerts, want 0 (info TTL expired)",
			len(visible))
	}
}
