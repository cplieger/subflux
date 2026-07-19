package queryhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

var errMock = errors.New("mock error")

// mockQueryStore implements QueryStore for testing. It records the last
// *api.StateQuery passed to GetState so tests can assert the limit/offset
// guards HandleState applies before querying, and the last type/prefix
// passed to GetBackoffByPrefix.
type mockQueryStore struct {
	err            error
	lastState      *api.StateQuery
	lastPrefixType api.MediaType
	lastPrefix     string
	stateEntries   []api.StateEntry
	backoffItems   []api.BackoffEntry
	manualLocks    []api.ManualLockEntry
	downloads      int
	attempts       int
}

func (m *mockQueryStore) GetState(_ context.Context, q *api.StateQuery) ([]api.StateEntry, error) {
	cp := *q
	m.lastState = &cp
	return m.stateEntries, m.err
}

func (m *mockQueryStore) GetBackoffItems(_ context.Context) ([]api.BackoffEntry, error) {
	return m.backoffItems, m.err
}

func (m *mockQueryStore) GetBackoffByPrefix(_ context.Context, mediaType api.MediaType, prefix string) ([]api.BackoffEntry, error) {
	m.lastPrefixType = mediaType
	m.lastPrefix = prefix
	return m.backoffItems, m.err
}

func (m *mockQueryStore) GetManualLocks(_ context.Context) ([]api.ManualLockEntry, error) {
	return m.manualLocks, m.err
}

func (m *mockQueryStore) Stats(_ context.Context) (int, int, error) {
	return m.downloads, m.attempts, m.err
}

func TestHandleState(t *testing.T) {
	t.Parallel()

	t.Run("returns_entries_on_GET", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{
			QueryDB: &mockQueryStore{stateEntries: []api.StateEntry{{
				ID: 1, MediaType: "movie", MediaID: "tt123",
				Language: "fr", Provider: "os", Score: 200,
			}}},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/state?type=movie&lang=eng", nil)
		w := httptest.NewRecorder()
		h.HandleState(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}
		var entries []api.StateEntry
		if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("HandleState() returned %d entries, want 1", len(entries))
		}
	})

	t.Run("rejects_POST", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{}})
		req := httptest.NewRequest(http.MethodPost, "/api/state", nil)
		w := httptest.NewRecorder()
		h.HandleState(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{err: errMock}})
		req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		w := httptest.NewRecorder()
		h.HandleState(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})

	t.Run("applies_positive_offset", func(t *testing.T) {
		t.Parallel()
		store := &mockQueryStore{}
		h := New(Deps{QueryDB: store})
		req := httptest.NewRequest(http.MethodGet, "/api/state?offset=3", nil)
		h.HandleState(httptest.NewRecorder(), req)
		if store.lastState.Offset != 3 {
			t.Errorf("offset=3 produced Offset=%d, want 3", store.lastState.Offset)
		}
	})

	t.Run("negative_offset_uses_default", func(t *testing.T) {
		t.Parallel()
		store := &mockQueryStore{}
		h := New(Deps{QueryDB: store})
		req := httptest.NewRequest(http.MethodGet, "/api/state?offset=-5", nil)
		h.HandleState(httptest.NewRecorder(), req)
		if store.lastState.Offset != 0 {
			t.Errorf("offset=-5 produced Offset=%d, want 0 (only positive offsets apply)", store.lastState.Offset)
		}
	})

	t.Run("handles_empty_result", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{}})
		req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		w := httptest.NewRecorder()
		h.HandleState(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		// The empty response must still be valid JSON (null is acceptable
		// for a nil slice).
		var result json.RawMessage
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Errorf("HandleState() returned invalid JSON: %v", err)
		}
	})

	t.Run("filters_passed_through", func(t *testing.T) {
		t.Parallel()
		store := &mockQueryStore{}
		h := New(Deps{QueryDB: store})
		req := httptest.NewRequest(http.MethodGet, "/api/state?type=episode&lang=fr&provider=os", nil)
		w := httptest.NewRecorder()
		h.HandleState(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if store.lastState.MediaType != "episode" {
			t.Errorf("GetState mediaType = %q, want %q", store.lastState.MediaType, "episode")
		}
		if store.lastState.Language != "fr" {
			t.Errorf("GetState language = %q, want %q", store.lastState.Language, "fr")
		}
		if string(store.lastState.Provider) != "os" {
			t.Errorf("GetState provider = %q, want %q", store.lastState.Provider, "os")
		}
	})
}

// TestHandleState_limit_boundary_values pins the limit-parsing guards:
// non-positive and non-numeric values keep the default, and values above
// the cap are clamped to 10000.
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
			store := &mockQueryStore{}
			h := New(Deps{QueryDB: store})
			req := httptest.NewRequest(http.MethodGet, "/api/state"+tt.query, nil)
			w := httptest.NewRecorder()
			h.HandleState(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if store.lastState.Limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", store.lastState.Limit, tt.wantLimit)
			}
		})
	}
}

func TestHandleBackoff(t *testing.T) {
	t.Parallel()

	t.Run("returns_entries_on_GET", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{backoffItems: []api.BackoffEntry{
			{MediaType: "movie", MediaID: "tt123", Language: "fr", Failures: 3},
		}}})
		req := httptest.NewRequest(http.MethodGet, "/api/backoff", nil)
		w := httptest.NewRecorder()
		h.HandleBackoff(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var entries []api.BackoffEntry
		if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("HandleBackoff() returned %d entries, want 1", len(entries))
		}
	})

	t.Run("rejects_POST", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{}})
		req := httptest.NewRequest(http.MethodPost, "/api/backoff", nil)
		w := httptest.NewRecorder()
		h.HandleBackoff(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{err: errMock}})
		req := httptest.NewRequest(http.MethodGet, "/api/backoff", nil)
		w := httptest.NewRecorder()
		h.HandleBackoff(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})
}

func TestHandleLocks(t *testing.T) {
	t.Parallel()

	t.Run("returns_entries_on_GET", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{manualLocks: []api.ManualLockEntry{
			{MediaType: "episode", MediaID: "tt456-s01e01", Language: "fr", Count: 2},
		}}})
		req := httptest.NewRequest(http.MethodGet, "/api/locks", nil)
		w := httptest.NewRecorder()
		h.HandleLocks(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var entries []api.ManualLockEntry
		if err := json.NewDecoder(w.Body).Decode(&entries); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("HandleLocks() returned %d entries, want 1", len(entries))
		}
	})

	t.Run("rejects_POST", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{}})
		req := httptest.NewRequest(http.MethodPost, "/api/locks", nil)
		w := httptest.NewRecorder()
		h.HandleLocks(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{err: errMock}})
		req := httptest.NewRequest(http.MethodGet, "/api/locks", nil)
		w := httptest.NewRecorder()
		h.HandleLocks(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})
}

func TestHandleBackoffByPrefix(t *testing.T) {
	t.Parallel()

	t.Run("rejects_POST", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{}})
		req := httptest.NewRequest(http.MethodPost, "/api/backoff/prefix", nil)
		w := httptest.NewRecorder()
		h.HandleBackoffByPrefix(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})

	t.Run("defaults_to_episode", func(t *testing.T) {
		t.Parallel()
		store := &mockQueryStore{}
		h := New(Deps{QueryDB: store})
		req := httptest.NewRequest(http.MethodGet, "/api/backoff/prefix?prefix=tvdb-81189-", nil)
		w := httptest.NewRecorder()
		h.HandleBackoffByPrefix(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if store.lastPrefixType != "episode" {
			t.Errorf("GetBackoffByPrefix mediaType = %q, want %q", store.lastPrefixType, "episode")
		}
	})

	t.Run("passes_type_and_prefix", func(t *testing.T) {
		t.Parallel()
		store := &mockQueryStore{}
		h := New(Deps{QueryDB: store})
		req := httptest.NewRequest(http.MethodGet, "/api/backoff/prefix?type=movie&prefix=tmdb-123-", nil)
		w := httptest.NewRecorder()
		h.HandleBackoffByPrefix(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if store.lastPrefixType != "movie" {
			t.Errorf("GetBackoffByPrefix mediaType = %q, want %q", store.lastPrefixType, "movie")
		}
		if store.lastPrefix != "tmdb-123-" {
			t.Errorf("GetBackoffByPrefix prefix = %q, want %q", store.lastPrefix, "tmdb-123-")
		}
	})

	t.Run("db_error_returns_500", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{err: errMock}})
		req := httptest.NewRequest(http.MethodGet, "/api/backoff/prefix", nil)
		w := httptest.NewRecorder()
		h.HandleBackoffByPrefix(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})

	t.Run("invalid_prefix_returns_400", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name   string
			prefix string
		}{
			{"arbitrary_text", "hello-world"},
			{"numeric_only", "12345"},
			{"wrong_case", "TVDB-81189-"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h := New(Deps{QueryDB: &mockQueryStore{}})
				req := httptest.NewRequest(http.MethodGet, "/api/backoff/prefix?prefix="+tt.prefix, nil)
				w := httptest.NewRecorder()
				h.HandleBackoffByPrefix(w, req)
				if w.Code != http.StatusBadRequest {
					t.Errorf("HandleBackoffByPrefix(prefix=%q) status = %d, want 400", tt.prefix, w.Code)
				}
			})
		}
	})
}
