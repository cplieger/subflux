package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// --- Handler tests ---

func TestHandleState_returns_entries(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{
		state: []api.StateEntry{
			{
				ID: 1, MediaType: "movie", MediaID: "tt123",
				Language: "fr", Provider: "os", Score: 200,
			},
		},
	}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleState() status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var entries []api.StateEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("handleState() returned %d entries, want 1", len(entries))
	}
}

func TestHandleState_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleState(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleState_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{stateErr: errMock}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleState() status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleStateStats_returns_counts(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{downloads: 42, attempts: 100}
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{
			ScanInterval: 30 * time.Minute,
		},
	}
	s := newTestServer(db, cfg)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/state/stats", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleStateStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleStateStats() status = %d, want %d", rec.Code, http.StatusOK)
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
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/state/stats", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleStateStats(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleStateStats(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBackoff_returns_entries(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{
		backoff: []api.BackoffEntry{
			{MediaType: "movie", MediaID: "tt123", Language: "fr", Failures: 3},
		},
	}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/backoff", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleBackoff() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entries []api.BackoffEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("handleBackoff() returned %d entries, want 1", len(entries))
	}
}

func TestHandleBackoff_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{backoffErr: errMock}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/backoff", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoff(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleBackoff() status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleLocks_returns_entries(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{
		locks: []api.ManualLockEntry{
			{MediaType: "episode", MediaID: "tt456-s01e01", Language: "fr", Count: 2},
		},
	}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/locks", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleLocks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleLocks() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entries []api.ManualLockEntry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("handleLocks() returned %d entries, want 1", len(entries))
	}
}

func TestHandleLocks_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{locksErr: errMock}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/locks", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleLocks(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleLocks() status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// --- activity.Log.start maxItems boundary ---

func TestActivityLog_start_exact_maxItems(t *testing.T) {
	t.Parallel()
	al := activity.New(2)

	al.Start("A", "first", "scheduled")
	al.Start("B", "second", "scheduled")

	al.RLock()
	count := len(al.EntriesUnsafe())
	al.RUnlock()

	// At exactly maxItems, should NOT trim.
	if count != 2 {
		t.Errorf("entries count = %d after 2 inserts with maxItems=2, want 2", count)
	}

	// One more should trigger trim.
	al.Start("C", "third", "scheduled")

	al.RLock()
	count = len(al.EntriesUnsafe())
	first := al.EntriesUnsafe()[0].Action
	al.RUnlock()

	if count != 2 {
		t.Errorf("entries count = %d after 3 inserts with maxItems=2, want 2", count)
	}
	if first != "B" {
		t.Errorf("entries[0].Action = %q after trim, want %q", first, "B")
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

// --- handleState limit parsing ---

func TestHandleState_limit_boundary_values(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		query     string
		wantLimit int
	}{
		{"default", "", 50},
		{"zero", "?limit=0", 50},      // n=0, n > 0 is false, keeps default 50
		{"negative", "?limit=-1", 50}, // n=-1, n > 0 is false, keeps default 50
		{"one", "?limit=1", 1},        // n=1, n > 0 is true
		{"ten_thousand", "?limit=10000", 10000},
		{"non_numeric", "?limit=abc", 50},   // strconv.Atoi fails, keeps default 50
		{"over_max", "?limit=20000", 10000}, // clamped to 10000
		{"one_over_max", "?limit=10001", 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := &qhMockStore{state: []api.StateEntry{}}
			s := newTestServer(db, &qhMockConfig{})

			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodGet, "/api/state"+tt.query, http.NoBody)
			rec := httptest.NewRecorder()
			s.handleState(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if db.stateLimit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", db.stateLimit, tt.wantLimit)
			}
		})
	}
}

// --- handleBackoff/handleLocks method checks ---

func TestHandleBackoff_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/backoff", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoff(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleBackoff(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleLocks_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/locks", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleLocks(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleLocks(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

// --- isValidMediaPrefix ---

func TestIsValidMediaPrefix_valid_formats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
	}{
		{"tvdb_with_trailing_dash", "tvdb-81189-"},
		{"tvdb_large_id", "tvdb-999999999-"},
		{"tvdb_single_digit", "tvdb-1-"},
		{"tmdb_with_trailing_dash", "tmdb-1271-"},
		{"tmdb_without_trailing_dash", "tmdb-1271"},
		{"tmdb_single_digit", "tmdb-1"},
		{"imdb_standard", "imdb-tt1234567"},
		{"imdb_short_id", "imdb-tt1"},
		{"imdb_long_id", "imdb-tt12345678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !api.IsValidMediaPrefix(tt.prefix) {
				t.Errorf("IsValidMediaPrefix(%q) = false, want true", tt.prefix)
			}
		})
	}
}

func TestIsValidMediaPrefix_invalid_formats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
	}{
		{"empty_string", ""},
		{"arbitrary_text", "hello-world"},
		{"tvdb_no_digits", "tvdb--"},
		{"tvdb_no_trailing_dash", "tvdb-81189"},
		{"tmdb_no_digits", "tmdb-"},
		{"imdb_no_tt", "imdb-1234567"},
		{"imdb_no_digits", "imdb-tt"},
		{"just_prefix", "tvdb"},
		{"numeric_only", "12345"},
		{"wrong_case_tvdb", "TVDB-81189-"},
		{"wrong_case_tmdb", "TMDB-1271"},
		{"wrong_case_imdb", "IMDB-tt1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if api.IsValidMediaPrefix(tt.prefix) {
				t.Errorf("IsValidMediaPrefix(%q) = true, want false", tt.prefix)
			}
		})
	}
}
