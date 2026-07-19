package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cplieger/subflux/internal/server/activity"
)

// --- activity.AlertLog ---

func TestAlertLog_recordPersistent_deduplicates(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "First error")
	al.RecordPersistent("startup", "Updated error")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 1 {
		t.Fatalf("alerts count = %d, want 1 (deduplicated)", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[0].Message != "Updated error" {
		t.Errorf("alert message = %q, want %q",
			al.AlertsUnsafe()[0].Message, "Updated error")
	}
	if al.AlertsUnsafe()[0].Kind != activity.AlertPersistent {
		t.Errorf("alert kind = %q, want %q",
			al.AlertsUnsafe()[0].Kind, activity.AlertPersistent)
	}
}

func TestAlertLog_recordPersistent_different_sources_not_deduplicated(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "Error A")
	al.RecordPersistent("config", "Error B")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 2 {
		t.Fatalf("alerts count = %d, want 2", len(al.AlertsUnsafe()))
	}
}

func TestAlertLog_dismiss(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.Record("sonarr", "test error")

	al.RLock()
	id := al.AlertsUnsafe()[0].ID
	al.RUnlock()

	if !al.Dismiss(id) {
		t.Error("dismiss() returned false for existing alert")
	}

	al.RLock()
	defer al.RUnlock()

	if !al.AlertsUnsafe()[0].Dismissed {
		t.Error("alert should be dismissed after dismiss()")
	}
}

func TestAlertLog_dismiss_nonexistent_returns_false(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	if al.Dismiss(999) {
		t.Error("dismiss(999) should return false for nonexistent alert")
	}
}

func TestAlertLog_recordPersistent_dismissed_allows_new(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "First error")

	al.RLock()
	id := al.AlertsUnsafe()[0].ID
	al.RUnlock()

	al.Dismiss(id)

	// After dismissing, a new persistent alert with the same source
	// should create a new entry (not update the dismissed one).
	al.RecordPersistent("startup", "Second error")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 2 {
		t.Fatalf("alerts count = %d, want 2 (dismissed + new)", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[1].Message != "Second error" {
		t.Errorf("second alert message = %q, want %q",
			al.AlertsUnsafe()[1].Message, "Second error")
	}
}

// --- handleDismissAlert ---

func TestHandleDismissAlert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		setupAlert bool
		wantCode   int
	}{
		{"missing id", "", false, http.StatusBadRequest},
		{"invalid id", "?id=abc", false, http.StatusBadRequest},
		{"zero id", "?id=0", false, http.StatusBadRequest},
		{"negative id", "?id=-1", false, http.StatusBadRequest},
		{"nonexistent id", "?id=999", false, http.StatusNotFound},
		{"success", "", true, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{
				alerts: activity.NewAlertLog(10),
			}

			query := tt.query
			if tt.setupAlert {
				s.alerts.Record("test", "test error")

				s.alerts.RLock()
				id := s.alerts.AlertsUnsafe()[0].ID
				s.alerts.RUnlock()

				query = "?id=" + strconv.Itoa(id)
			}

			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodDelete, "/api/alerts"+query, http.NoBody)
			w := httptest.NewRecorder()

			s.handleDismissAlert(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("handleDismissAlert(%s) status = %d, want %d",
					tt.name, w.Code, tt.wantCode)
			}
		})
	}
}

// --- activity.Log.progress ---

func TestActivityLog_progress_updates_entry(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "initial detail", "scheduled")
	al.Progress(id, 5, 20, "updated detail")

	if n := len(al.Entries()); n != 1 {
		t.Fatalf("entries count = %d, want 1", n)
	}
	e, _ := al.Get(id)
	if e.Current != 5 {
		t.Errorf("entry.Current = %d, want 5", e.Current)
	}
	if e.Total != 20 {
		t.Errorf("entry.Total = %d, want 20", e.Total)
	}
	if e.Detail != "updated detail" {
		t.Errorf("entry.Detail = %q, want %q", e.Detail, "updated detail")
	}
}

func TestActivityLog_progress_empty_detail_preserves_existing(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "original detail", "scheduled")
	al.Progress(id, 3, 10, "")

	e, _ := al.Get(id)
	if e.Detail != "original detail" {
		t.Errorf("entry.Detail = %q, want %q (empty detail should preserve original)",
			e.Detail, "original detail")
	}
	if e.Current != 3 {
		t.Errorf("entry.Current = %d, want 3", e.Current)
	}
}

func TestActivityLog_progress_nonexistent_id_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	al.Start("Scan", "detail", "scheduled")
	al.Progress("nonexistent", 99, 100, "should not appear")

	if got := al.Entries()[0].Current; got != 0 {
		t.Errorf("entry.Current = %d, want 0 (nonexistent ID should not modify)", got)
	}
}

// --- dismissBySource ---

func TestAlertLog_dismissBySource(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "Error A")
	al.RecordPersistent("config", "Error B")
	al.Record("startup", "Transient C") // transient, same source

	al.DismissBySource("startup")

	al.RLock()
	defer al.RUnlock()

	for _, a := range al.AlertsUnsafe() {
		if a.Source == "startup" && a.Kind == activity.AlertPersistent && !a.Dismissed {
			t.Error("persistent startup alert should be dismissed")
		}
		if a.Source == "config" && a.Dismissed {
			t.Error("config alert should not be dismissed")
		}
		// Transient alerts from the same source should not be dismissed.
		if a.Source == "startup" && a.Kind == activity.AlertTransient && a.Dismissed {
			t.Error("transient startup alert should not be dismissed by dismissBySource")
		}
	}
}

func TestAlertLog_dismissBySource_no_matching_source(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "Error A")

	// Should not panic or modify anything.
	al.DismissBySource("nonexistent")

	al.RLock()
	defer al.RUnlock()

	if al.AlertsUnsafe()[0].Dismissed {
		t.Error("alert should not be dismissed by non-matching source")
	}
}
