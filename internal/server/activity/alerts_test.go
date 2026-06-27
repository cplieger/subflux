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

	al.Lock()
	al.AlertsUnsafe()[0].Time = time.Now().Add(-30 * time.Minute)
	al.Unlock()

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

	al.Lock()
	al.AlertsUnsafe()[0].Time = time.Now().Add(-2 * time.Hour)
	al.Unlock()

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

	al.RLock()
	defer al.RUnlock()
	for _, a := range al.AlertsUnsafe() {
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
