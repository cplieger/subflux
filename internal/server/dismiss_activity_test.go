package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

	s.activity.RLock()
	defer s.activity.RUnlock()

	if len(s.activity.EntriesUnsafe()) != 0 {
		t.Errorf("entries count = %d after dismiss, want 0", len(s.activity.EntriesUnsafe()))
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

// --- handleScanMovie ---

func TestHandleScanMovie_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan/movie/42", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanMovie(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScanMovie(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanMovie_missing_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanMovie(empty id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanMovie_non_numeric_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/abc", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanMovie(non-numeric id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanMovie_zero_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/0", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanMovie(zero id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanMovie_negative_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/-1", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanMovie(negative id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanMovie_no_radarr(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/42", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanMovie(no radarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "radarr not configured") {
		t.Errorf("handleScanMovie(no radarr) body = %q, want radarr error", body)
	}
}
