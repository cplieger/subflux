package queryhandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/testsupport"
)

// fakeMetrics implements MetricsReader with a fixed search count.
type fakeMetrics struct {
	searches int64
}

func (m *fakeMetrics) TotalSearches() int64 { return m.searches }

func TestHandleStateStats_returns_counts(t *testing.T) {
	t.Parallel()
	cfg := &testsupport.NopConfig{
		SearchConfig: api.SearchConfig{ScanInterval: 30 * time.Minute},
	}
	// CountMissing returns a sentinel so the response wiring (not the
	// counting logic, which has its own tests in the server root) is
	// what this test pins.
	h := New(Deps{
		QueryDB: &mockQueryStore{downloads: 42, attempts: 100},
		CovDB:   &testsupport.NopStore{},
		Metrics: &fakeMetrics{},
		State:   func() *LiveState { return &LiveState{Cfg: cfg} },
		CountMissing: func(_ context.Context, _ api.ConfigProvider, _ api.CoverageStore, _ []arrapi.Series, _ []arrapi.Movie) int {
			return 0
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/state/stats", nil)
	rec := httptest.NewRecorder()
	h.HandleStateStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleStateStats() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify all 8 expected response fields are present and correct.
	if int(result["downloads"].(float64)) != 42 {
		t.Errorf("downloads = %v, want 42", result["downloads"])
	}
	if int(result["attempts"].(float64)) != 100 {
		t.Errorf("attempts = %v, want 100 (DB fallback when metrics zero)", result["attempts"])
	}
	if result["last_scan"] != "" {
		t.Errorf("last_scan = %v, want empty string", result["last_scan"])
	}
	if int(result["scan_interval_seconds"].(float64)) != 1800 {
		t.Errorf("scan_interval_seconds = %v, want 1800", result["scan_interval_seconds"])
	}
	if int(result["total_subs"].(float64)) != 0 {
		t.Errorf("total_subs = %v, want 0", result["total_subs"])
	}
	if int(result["total_series"].(float64)) != 0 {
		t.Errorf("total_series = %v, want 0 (no sonarr configured)", result["total_series"])
	}
	if int(result["total_movies"].(float64)) != 0 {
		t.Errorf("total_movies = %v, want 0 (no radarr configured)", result["total_movies"])
	}
	if int(result["missing_subs"].(float64)) != 0 {
		t.Errorf("missing_subs = %v, want 0", result["missing_subs"])
	}
}

func TestHandleStateStats_rejects_non_get(t *testing.T) {
	t.Parallel()
	h := New(Deps{
		QueryDB: &mockQueryStore{},
		CovDB:   &testsupport.NopStore{},
		Metrics: &fakeMetrics{},
		State:   func() *LiveState { return &LiveState{Cfg: &testsupport.NopConfig{}} },
	})

	req := httptest.NewRequest(http.MethodPost, "/api/state/stats", nil)
	rec := httptest.NewRecorder()
	h.HandleStateStats(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleStateStats(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}
