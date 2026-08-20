package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cplieger/subflux/internal/server/activity"
)

// --- activity.AlertLog ---

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

				id := s.alerts.VisibleAlerts()[0].ID

				query = "?id=" + strconv.Itoa(id)
			}

			req := httptest.NewRequestWithContext(t.Context(),
				http.MethodDelete, "/api/alerts"+query, http.NoBody)
			w := httptest.NewRecorder()

			activityH(s).HandleDismissAlert(w, req)

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
		t.Errorf("entries count = %d, want 1", n)
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
