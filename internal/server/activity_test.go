package server

import (
	"subflux/internal/server/activity"
	"testing"
	"time"
)

// --- activity.Log.setQueued ---

func TestActivityLog_setQueued_marks_queued(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	al.SetQueued(id, true)

	al.RLock()
	defer al.RUnlock()

	if !al.EntriesUnsafe()[0].Queued {
		t.Error("entry should be queued after setQueued(true)")
	}
}

func TestActivityLog_setQueued_unqueue_resets_start_time(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	al.RLock()
	originalStart := al.EntriesUnsafe()[0].StartedAt
	al.RUnlock()

	al.SetQueued(id, true)
	al.SetQueued(id, false)

	al.RLock()
	defer al.RUnlock()

	if al.EntriesUnsafe()[0].Queued {
		t.Error("entry should not be queued after setQueued(false)")
	}
	if al.EntriesUnsafe()[0].StartedAt.Before(originalStart) {
		t.Error("StartedAt should be >= original after unqueue")
	}
}

func TestActivityLog_setQueued_nonexistent_id_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	al.Start("Scan", "detail", "manual")

	al.SetQueued("nonexistent", true)

	al.RLock()
	defer al.RUnlock()

	if al.EntriesUnsafe()[0].Queued {
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

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 0 {
		t.Errorf("entries count = %d after dismiss, want 0", len(al.EntriesUnsafe()))
	}
}

func TestActivityLog_dismiss_does_not_remove_active_entry(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	al.Dismiss(id)

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 1 {
		t.Errorf("entries count = %d, want 1 (active entry not dismissed)", len(al.EntriesUnsafe()))
	}
}

func TestActivityLog_dismiss_nonexistent_id_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	al.Start("Scan", "detail", "manual")
	al.End(al.EntriesUnsafe()[0].ID)

	al.Dismiss("nonexistent")

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 1 {
		t.Errorf("entries count = %d, want 1 (nonexistent ID)", len(al.EntriesUnsafe()))
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

	al.RLock()
	defer al.RUnlock()

	if !al.EntriesUnsafe()[0].Cancelled {
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

	al.RLock()
	defer al.RUnlock()

	if !al.EntriesUnsafe()[0].Done {
		t.Error("entry should be done after fail()")
	}
	if !al.EntriesUnsafe()[0].Failed {
		t.Error("entry should be failed after fail()")
	}
	if al.EntriesUnsafe()[0].EndedAt == nil {
		t.Error("entry.EndedAt should be set after fail()")
	}
}

func TestActivityLog_fail_nonexistent_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)
	id := al.Start("Scan", "detail", "manual")

	al.Fail("nonexistent")

	al.RLock()
	defer al.RUnlock()

	if al.EntriesUnsafe()[0].Done || al.EntriesUnsafe()[0].Failed {
		t.Errorf("entry %q should not be affected by fail(nonexistent)", id)
	}
}

// --- activity.Log.pruneCompleted ---

func TestActivityLog_pruneCompleted_removes_old_done_entries(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "old scan", "scheduled")
	al.End(id)

	// Backdate the EndedAt to 20 minutes ago.
	al.Lock()
	old := al.EntriesUnsafe()[0].EndedAt.Add(-20 * time.Minute)
	al.EntriesUnsafe()[0].EndedAt = &old
	al.Unlock()

	al.PruneCompleted(15 * time.Minute)

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 0 {
		t.Errorf("entries count = %d after pruneCompleted, want 0 (old done entry removed)",
			len(al.EntriesUnsafe()))
	}
}

func TestActivityLog_pruneCompleted_keeps_recent_done_entries(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "recent scan", "scheduled")
	al.End(id)

	al.PruneCompleted(15 * time.Minute)

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 1 {
		t.Errorf("entries count = %d after pruneCompleted, want 1 (recent done entry kept)",
			len(al.EntriesUnsafe()))
	}
}

func TestActivityLog_pruneCompleted_keeps_active_entries(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	al.Start("Scan", "active scan", "scheduled")

	al.PruneCompleted(0) // maxAge=0 means prune everything completed

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 1 {
		t.Errorf("entries count = %d after pruneCompleted, want 1 (active entry kept)",
			len(al.EntriesUnsafe()))
	}
}

func TestActivityLog_pruneCompleted_mixed_entries(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	// Active entry (not done).
	al.Start("Active", "still running", "scheduled")

	// Recent done entry.
	recentID := al.Start("Recent", "just finished", "scheduled")
	al.End(recentID)

	// Old done entry.
	oldID := al.Start("Old", "finished long ago", "scheduled")
	al.End(oldID)
	al.Lock()
	old := al.EntriesUnsafe()[2].EndedAt.Add(-30 * time.Minute)
	al.EntriesUnsafe()[2].EndedAt = &old
	al.Unlock()

	al.PruneCompleted(15 * time.Minute)

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 2 {
		t.Fatalf("entries count = %d after pruneCompleted, want 2 (active + recent kept)",
			len(al.EntriesUnsafe()))
	}
	if al.EntriesUnsafe()[0].Action != "Active" {
		t.Errorf("entries[0].Action = %q, want %q", al.EntriesUnsafe()[0].Action, "Active")
	}
	if al.EntriesUnsafe()[1].Action != "Recent" {
		t.Errorf("entries[1].Action = %q, want %q", al.EntriesUnsafe()[1].Action, "Recent")
	}
}

func TestActivityLog_pruneCompleted_empty_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	al.PruneCompleted(15 * time.Minute)

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 0 {
		t.Errorf("entries count = %d, want 0", len(al.EntriesUnsafe()))
	}
}
