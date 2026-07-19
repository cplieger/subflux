package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- handleDismissActivity ---

func TestHandleDismissActivity_missing_id_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDismissActivity(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleDismissActivity(no id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleDismissActivity_cancels_queued_entry(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	id := s.activity.Start("Scan", "queued scan", "manual")
	s.activity.SetQueued(id, true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/activity?id="+id, http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDismissActivity(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("handleDismissActivity(cancel queued) status = %d, want %d",
			rec.Code, http.StatusNoContent)
	}

	if !s.activity.IsCancelled(id) {
		t.Error("queued entry should be cancelled after dismiss")
	}
}

func TestHandleDismissActivity_dismisses_completed_entry(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	id := s.activity.Start("Scan", "done scan", "manual")
	s.activity.End(id)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/activity?id="+id, http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDismissActivity(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("handleDismissActivity(dismiss done) status = %d, want %d",
			rec.Code, http.StatusNoContent)
	}

	if n := len(s.activity.Entries()); n != 0 {
		t.Errorf("entries count = %d after dismiss, want 0", n)
	}
}

func TestHandleDismissActivity_nonexistent_id_returns_204(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/activity?id=nonexistent", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleDismissActivity(rec, req)

	// dismiss() is a no-op for nonexistent IDs; handler still returns 204.
	if rec.Code != http.StatusNoContent {
		t.Errorf("handleDismissActivity(nonexistent) status = %d, want %d",
			rec.Code, http.StatusNoContent)
	}
}

// The handleScanMovie validation tests formerly in this file moved to
// internal/server/scanning/handler_http_test.go with the rest of the scan
// HTTP surface.
