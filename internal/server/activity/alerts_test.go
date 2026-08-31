package activity

import (
	"testing"
	"time"
)

// ---- Record / RecordInfo ----

// Each recorded alert gets a monotonically increasing ID starting at 1.
func TestAlertLog_Record_incrementsID(t *testing.T) {
	al := NewAlertLog(10)
	al.Record("poller", "err one")
	al.Record("poller", "err two")

	vis := al.VisibleAlerts()
	if len(vis) != 2 {
		t.Fatalf("VisibleAlerts() len = %d, want 2", len(vis))
	}
	if vis[0].ID != 1 {
		t.Errorf("first alert ID = %d, want 1", vis[0].ID)
	}
	if vis[1].ID != 2 {
		t.Errorf("second alert ID = %d, want 2", vis[1].ID)
	}
}

// RecordInfo gives scan-result alerts a 10-minute TTL, so a 30-minute-old info
// alert has expired and is no longer visible.
func TestAlertLog_RecordInfo_ttlExpiresAfter10m(t *testing.T) {
	al := NewAlertLog(10)
	al.RecordInfo("scan complete")

	al.mu.Lock()
	al.alerts[0].Time = time.Now().Add(-30 * time.Minute)
	al.mu.Unlock()

	if vis := al.VisibleAlerts(); len(vis) != 0 {
		t.Errorf("VisibleAlerts() len = %d, want 0 (30m-old info alert is past its 10m TTL)", len(vis))
	}
}

// ---- AddAlert capacity ----

// AddAlert keeps only the most recent `max` alerts, dropping the oldest.
func TestAlertLog_AddAlert_trimsToCapacity(t *testing.T) {
	al := NewAlertLog(3)
	for range 5 {
		al.Record("src", "msg")
	}

	vis := al.VisibleAlerts()
	if len(vis) != 3 {
		t.Fatalf("VisibleAlerts() len = %d, want 3 (capacity)", len(vis))
	}
	// IDs 1 and 2 (the two oldest) must have been trimmed; 3,4,5 remain.
	for _, a := range vis {
		if a.ID < 3 {
			t.Errorf("alert ID=%d survived, want only the last 3 (IDs 3,4,5)", a.ID)
		}
	}
}

// ---- RecordPersistent ----

// Two persistent alerts from the same source collapse into one, keeping the
// latest message rather than accumulating duplicates.
func TestAlertLog_RecordPersistent_deduplicates(t *testing.T) {
	al := NewAlertLog(10)
	al.RecordPersistent("poller", "first message")
	al.RecordPersistent("poller", "second message")

	count := 0
	var msg string
	for _, a := range al.VisibleAlerts() {
		if a.Kind == AlertPersistent && a.Source == "poller" {
			count++
			msg = a.Message
		}
	}
	if count != 1 {
		t.Errorf("persistent alerts from %q = %d, want 1 (dedup on source)", "poller", count)
	}
	if msg != "second message" {
		t.Errorf("deduped persistent message = %q, want %q", msg, "second message")
	}
}

// ---- VisibleAlerts ----

// Persistent alerts ignore the transient TTL entirely; even a 2-hour-old one
// stays visible until explicitly dismissed.
func TestAlertLog_VisibleAlerts_persistentIgnoresTTL(t *testing.T) {
	al := NewAlertLog(10)
	al.RecordPersistent("poller", "down")

	al.mu.Lock()
	al.alerts[0].Time = time.Now().Add(-2 * time.Hour)
	al.mu.Unlock()

	found := false
	for _, a := range al.VisibleAlerts() {
		if a.Kind == AlertPersistent && a.Source == "poller" {
			found = true
		}
	}
	if !found {
		t.Errorf("VisibleAlerts() dropped a 2h-old persistent alert; persistent alerts must ignore TTL")
	}
}

// ---- Dismiss ----

// Dismiss marks exactly the alert whose ID matches, leaving the others alone.
func TestAlertLog_Dismiss_matchesByID(t *testing.T) {
	al := NewAlertLog(10)
	al.Record("a", "first")  // ID 1
	al.Record("b", "second") // ID 2

	if ok := al.Dismiss(2); !ok {
		t.Fatalf("Dismiss(2) = false, want true")
	}

	al.mu.RLock()
	defer al.mu.RUnlock()
	for _, a := range al.alerts {
		switch a.ID {
		case 2:
			if !a.Dismissed {
				t.Errorf("alert ID=2 Dismissed = false, want true")
			}
		case 1:
			if a.Dismissed {
				t.Errorf("alert ID=1 Dismissed = true, want false (only ID=2 should be dismissed)")
			}
		}
	}
}

// ---- DismissBySource ----

// DismissBySource dismisses only the undismissed persistent alerts from the
// given source, leaving transient alerts and other sources visible.
func TestAlertLog_DismissBySource_dismissesPersistentFromSource(t *testing.T) {
	al := NewAlertLog(10)
	al.RecordPersistent("poller", "poller persistent")
	al.Record("poller", "poller transient")              // transient, same source
	al.RecordPersistent("scanner", "scanner persistent") // other source

	al.DismissBySource("poller")

	var sawPersistentPoller, sawTransientPoller, sawScanner bool
	for _, a := range al.VisibleAlerts() {
		switch {
		case a.Source == "poller" && a.Kind == AlertPersistent:
			sawPersistentPoller = true
		case a.Source == "poller" && a.Kind == AlertTransient:
			sawTransientPoller = true
		case a.Source == "scanner" && a.Kind == AlertPersistent:
			sawScanner = true
		}
	}
	if sawPersistentPoller {
		t.Errorf("DismissBySource(poller) left the persistent poller alert visible")
	}
	if !sawTransientPoller {
		t.Errorf("DismissBySource(poller) wrongly dismissed the transient poller alert")
	}
	if !sawScanner {
		t.Errorf("DismissBySource(poller) wrongly dismissed the scanner alert")
	}
}

// ---- Moved from package server ----
//
// These AlertLog unit tests lived in internal/server and reached into the log
// through six exported mutex/raw-slice helpers (Lock/Unlock/RLock/RUnlock/
// AlertsUnsafe/AppendAlert) that existed only for them. They belong next to
// the type, where the same assertions need no exported escape hatch.

func TestAlertLog_Record_trimsToMax(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(2)

	al.Record("a", "first")
	al.Record("b", "second")
	al.Record("c", "third")

	al.mu.RLock()
	defer al.mu.RUnlock()

	if len(al.alerts) != 2 {
		t.Fatalf("alerts count = %d, want 2 (trimmed)", len(al.alerts))
	}
	if al.alerts[0].Source != "b" {
		t.Errorf("alerts[0].Source = %q, want %q (oldest trimmed)", al.alerts[0].Source, "b")
	}
}

func TestAlertLog_Record_keepsEverySource(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(100)

	al.Record("sonarr", "error 1")
	al.Record("radarr", "error 2")
	al.Record("config", "error 3")

	al.mu.RLock()
	defer al.mu.RUnlock()

	if len(al.alerts) != 3 {
		t.Fatalf("alerts count = %d, want 3", len(al.alerts))
	}
}

func TestAlertLog_RecordPersistent_differentSourcesNotDeduplicated(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(10)

	al.RecordPersistent("startup", "Error A")
	al.RecordPersistent("config", "Error B")

	al.mu.RLock()
	defer al.mu.RUnlock()

	if len(al.alerts) != 2 {
		t.Fatalf("alerts count = %d, want 2", len(al.alerts))
	}
}

func TestAlertLog_RecordPersistent_dismissedAllowsNew(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(10)

	al.RecordPersistent("startup", "First error")

	al.mu.RLock()
	id := al.alerts[0].ID
	al.mu.RUnlock()

	al.Dismiss(id)

	// After dismissing, a new persistent alert with the same source
	// should create a new entry (not update the dismissed one).
	al.RecordPersistent("startup", "Second error")

	al.mu.RLock()
	defer al.mu.RUnlock()

	if len(al.alerts) != 2 {
		t.Fatalf("alerts count = %d, want 2 (dismissed + new)", len(al.alerts))
	}
	if al.alerts[1].Message != "Second error" {
		t.Errorf("second alert message = %q, want %q",
			al.alerts[1].Message, "Second error")
	}
}

func TestAlertLog_Dismiss_nonexistentReturnsFalse(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(10)

	if al.Dismiss(999) {
		t.Error("dismiss(999) should return false for nonexistent alert")
	}
}

func TestAlertLog_DismissBySource_noMatchingSource(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(10)

	al.RecordPersistent("startup", "Error A")

	// Should not panic or modify anything.
	al.DismissBySource("nonexistent")

	al.mu.RLock()
	defer al.mu.RUnlock()

	if al.alerts[0].Dismissed {
		t.Error("alert should not be dismissed by non-matching source")
	}
}

func TestAlertLog_VisibleAlerts_excludesExpiredTransient(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(10)

	// Add a transient alert that expired (older than default TTL).
	al.mu.Lock()
	al.alerts = append(al.alerts, Alert{
		ID: 999, Level: "error", Source: "old", Message: "old error",
		Kind: AlertTransient, Time: time.Now().Add(-2 * time.Hour),
	})
	al.mu.Unlock()

	visible := al.VisibleAlerts()
	if len(visible) != 0 {
		t.Errorf("visibleAlerts() returned %d alerts, want 0 (expired transient excluded)",
			len(visible))
	}
}

func TestAlertLog_VisibleAlerts_excludesDismissed(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(10)

	al.Record("sonarr", "recent error")
	al.mu.RLock()
	id := al.alerts[0].ID
	al.mu.RUnlock()
	al.Dismiss(id)

	visible := al.VisibleAlerts()
	if len(visible) != 0 {
		t.Errorf("visibleAlerts() returned %d alerts, want 0 (dismissed excluded)",
			len(visible))
	}
}

func TestAlertLog_VisibleAlerts_emptyReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(10)

	visible := al.VisibleAlerts()
	if visible == nil {
		t.Fatal("visibleAlerts() returned nil, want non-nil empty slice")
	}
	if len(visible) != 0 {
		t.Errorf("visibleAlerts() returned %d alerts, want 0", len(visible))
	}
}

func TestAlertLog_VisibleAlerts_mixedTypesAndStates(t *testing.T) {
	t.Parallel()
	al := NewAlertLog(10)

	// 1. Recent transient (visible).
	al.Record("sonarr", "recent error")

	// 2. Old transient (expired, hidden).
	al.mu.Lock()
	al.alerts = append(al.alerts, Alert{
		ID: 997, Level: "error", Source: "old-transient", Message: "expired",
		Kind: AlertTransient, Time: time.Now().Add(-48 * time.Hour),
	})
	al.mu.Unlock()

	// 3. Old persistent (visible regardless of age).
	al.mu.Lock()
	al.alerts = append(al.alerts, Alert{
		ID: 996, Level: "error", Source: "startup", Message: "persistent",
		Kind: AlertPersistent, Time: time.Now().Add(-72 * time.Hour),
	})
	al.mu.Unlock()

	// 4. Dismissed recent transient (hidden).
	al.Record("radarr", "dismissed error")
	al.mu.RLock()
	dismissID := al.alerts[len(al.alerts)-1].ID
	al.mu.RUnlock()
	al.Dismiss(dismissID)

	visible := al.VisibleAlerts()
	if len(visible) != 2 {
		t.Fatalf("visibleAlerts() returned %d alerts, want 2 (recent transient + old persistent)",
			len(visible))
	}

	sources := map[string]bool{}
	for _, a := range visible {
		sources[a.Source] = true
	}
	if !sources["sonarr"] {
		t.Error("expected recent transient alert from 'sonarr' to be visible")
	}
	if !sources["startup"] {
		t.Error("expected old persistent alert from 'startup' to be visible")
	}
}

// ---- raise / dismiss hooks (status events, E1) ----

// The raise hook observes every recorded alert with its under-lock snapshot,
// fired after the lock is released — proven by re-entering the log from
// inside the hook.
func TestAlertLog_onRaise_fires_after_unlock_with_snapshot(t *testing.T) {
	al := NewAlertLog(10)
	var raised []Alert
	al.SetOnRaise(func(a Alert) {
		_ = al.VisibleAlerts() // re-entry: deadlocks if fired under the write lock
		raised = append(raised, a)
	})

	al.Record("sonarr", "search failed")

	if len(raised) != 1 {
		t.Fatalf("raise hook fired %d times, want 1", len(raised))
	}
	if raised[0].ID != 1 || raised[0].Message != "search failed" || raised[0].Kind != AlertTransient {
		t.Errorf("raise snapshot = %+v, want id 1, message %q, transient", raised[0], "search failed")
	}
}

// A persistent re-raise (same source, undismissed) refreshes the existing
// alert and raises the REFRESHED snapshot under the same id.
func TestAlertLog_onRaise_fires_for_persistent_refresh(t *testing.T) {
	al := NewAlertLog(10)
	var raised []Alert
	al.SetOnRaise(func(a Alert) { raised = append(raised, a) })

	al.RecordPersistent("config", "first")
	al.RecordPersistent("config", "second")

	if len(raised) != 2 {
		t.Fatalf("raise hook fired %d times for record+refresh, want 2", len(raised))
	}
	if raised[1].ID != raised[0].ID {
		t.Errorf("refresh raised id %d, want the original id %d", raised[1].ID, raised[0].ID)
	}
	if raised[1].Message != "second" {
		t.Errorf("refresh raised message %q, want %q (the refreshed snapshot)", raised[1].Message, "second")
	}
}

// The dismiss hook fires for the by-id dismissal with the dismissed
// snapshot, and DismissBySource fires once per alert it dismisses. A miss
// fires nothing.
func TestAlertLog_onDismiss_fires_for_both_dismiss_paths(t *testing.T) {
	al := NewAlertLog(10)
	var dismissed []Alert
	al.SetOnDismiss(func(a Alert) {
		_ = al.VisibleAlerts() // re-entry: deadlocks if fired under the write lock
		dismissed = append(dismissed, a)
	})

	al.Record("sonarr", "one")
	al.RecordPersistent("startup", "p1")
	al.RecordPersistent("webauthn", "p2")

	al.Dismiss(1)
	if len(dismissed) != 1 || dismissed[0].ID != 1 || !dismissed[0].Dismissed {
		t.Fatalf("dismiss-by-id hook = %+v, want one snapshot with id 1, dismissed", dismissed)
	}

	al.Dismiss(999) // miss
	if len(dismissed) != 1 {
		t.Fatalf("dismiss hook fired on a miss; want no event")
	}

	al.DismissBySource("startup")
	al.DismissBySource("webauthn")
	if len(dismissed) != 3 {
		t.Fatalf("dismiss hook fired %d times total, want 3 (by-id + two by-source)", len(dismissed))
	}
	if dismissed[1].Source != "startup" || dismissed[2].Source != "webauthn" {
		t.Errorf("by-source dismiss snapshots = %q, %q, want startup, webauthn",
			dismissed[1].Source, dismissed[2].Source)
	}
}

// Cap eviction publishes nothing: the raise for the new alert is the only
// delta (evicted alerts converge on the client's reconcile poll, like TTL
// expiry, which also fires no event because VisibleAlerts never mutates).
func TestAlertLog_cap_eviction_fires_no_dismiss(t *testing.T) {
	al := NewAlertLog(2)
	raises, dismisses := 0, 0
	al.SetOnRaise(func(Alert) { raises++ })
	al.SetOnDismiss(func(Alert) { dismisses++ })

	al.Record("a", "1")
	al.Record("b", "2")
	al.Record("c", "3") // evicts "a"

	if raises != 3 {
		t.Errorf("raise hook fired %d times, want 3", raises)
	}
	if dismisses != 0 {
		t.Errorf("dismiss hook fired %d times on cap eviction, want 0 (reconcile-only)", dismisses)
	}
}
