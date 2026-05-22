package coveragehandlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"subflux/internal/api"
)

// mockCoverageStore implements CoverageStore for testing.
type mockCoverageStore struct {
	err           error
	subtitleFiles []api.SubtitleFileRow
	scanStates    []api.ScanStateRow
}

func (m *mockCoverageStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return m.subtitleFiles, m.err
}

func (m *mockCoverageStore) GetScanStates(_ context.Context, _ api.MediaType, _ string) ([]api.ScanStateRow, error) {
	return m.scanStates, m.err
}

func TestHandleCoverage(t *testing.T) {
	t.Parallel()

	t.Run("series_nil_sonarr_returns_empty_array", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(Deps{
			Store: &mockCoverageStore{},
			StateFunc: func() *LiveState {
				return &LiveState{Sonarr: nil}
			},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil)
		w := httptest.NewRecorder()
		h.HandleCoverageSeries(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if body := w.Body.String(); body != "[]" && body != "[]\n" {
			t.Errorf("body = %q, want empty array", body)
		}
	})

	t.Run("movies_nil_radarr_returns_empty_array", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(Deps{
			Store: &mockCoverageStore{},
			StateFunc: func() *LiveState {
				return &LiveState{Radarr: nil}
			},
		})
		req := httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil)
		w := httptest.NewRecorder()
		h.HandleCoverageMovies(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if body := w.Body.String(); body != "[]" && body != "[]\n" {
			t.Errorf("body = %q, want empty array", body)
		}
	})

	t.Run("series_POST_rejected", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(Deps{
			Store:     &mockCoverageStore{},
			StateFunc: func() *LiveState { return &LiveState{} },
		})
		req := httptest.NewRequest(http.MethodPost, "/api/coverage/series", nil)
		w := httptest.NewRecorder()
		h.HandleCoverageSeries(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})

	t.Run("movies_POST_rejected", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(Deps{
			Store:     &mockCoverageStore{},
			StateFunc: func() *LiveState { return &LiveState{} },
		})
		req := httptest.NewRequest(http.MethodPost, "/api/coverage/movies", nil)
		w := httptest.NewRecorder()
		h.HandleCoverageMovies(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})
}
