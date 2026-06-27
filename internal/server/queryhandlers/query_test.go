package queryhandlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// mockQueryStore implements QueryStore for testing. It records the last
// *api.StateQuery passed to GetState so tests can assert the limit/offset
// guards HandleState applies before querying.
type mockQueryStore struct {
	err          error
	lastState    *api.StateQuery
	stateEntries []api.StateEntry
	backoffItems []api.BackoffEntry
	manualLocks  []api.ManualLockEntry
	downloads    int
	attempts     int
}

func (m *mockQueryStore) GetState(_ context.Context, q *api.StateQuery) ([]api.StateEntry, error) {
	cp := *q
	m.lastState = &cp
	return m.stateEntries, m.err
}

func (m *mockQueryStore) GetBackoffItems(_ context.Context) ([]api.BackoffEntry, error) {
	return m.backoffItems, m.err
}

func (m *mockQueryStore) GetBackoffByPrefix(_ context.Context, _ api.MediaType, _ string) ([]api.BackoffEntry, error) {
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
			QueryDB: &mockQueryStore{stateEntries: []api.StateEntry{{MediaID: "test-1"}}},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/state?type=movie&lang=eng", nil)
		w := httptest.NewRecorder()
		h.HandleState(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
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

	t.Run("caps_limit_at_10000", func(t *testing.T) {
		t.Parallel()
		store := &mockQueryStore{}
		h := New(Deps{QueryDB: store})
		req := httptest.NewRequest(http.MethodGet, "/api/state?limit=99999", nil)
		w := httptest.NewRecorder()
		h.HandleState(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if store.lastState.Limit != 10000 {
			t.Errorf("limit=99999 produced Limit=%d, want 10000 (capped)", store.lastState.Limit)
		}
	})

	t.Run("applies_positive_limit", func(t *testing.T) {
		t.Parallel()
		store := &mockQueryStore{}
		h := New(Deps{QueryDB: store})
		req := httptest.NewRequest(http.MethodGet, "/api/state?limit=7", nil)
		h.HandleState(httptest.NewRecorder(), req)
		if store.lastState.Limit != 7 {
			t.Errorf("limit=7 produced Limit=%d, want 7", store.lastState.Limit)
		}
	})

	t.Run("zero_limit_uses_default", func(t *testing.T) {
		t.Parallel()
		store := &mockQueryStore{}
		h := New(Deps{QueryDB: store})
		req := httptest.NewRequest(http.MethodGet, "/api/state?limit=0", nil)
		h.HandleState(httptest.NewRecorder(), req)
		if store.lastState.Limit != 50 {
			t.Errorf("limit=0 produced Limit=%d, want 50 (zero must not override default)", store.lastState.Limit)
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
	})
}

func TestHandleBackoff(t *testing.T) {
	t.Parallel()

	t.Run("returns_entries_on_GET", func(t *testing.T) {
		t.Parallel()
		h := New(Deps{QueryDB: &mockQueryStore{backoffItems: []api.BackoffEntry{{MediaID: "test-1"}}}})
		req := httptest.NewRequest(http.MethodGet, "/api/backoff", nil)
		w := httptest.NewRecorder()
		h.HandleBackoff(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
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
}
