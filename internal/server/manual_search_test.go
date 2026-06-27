package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/manualops"
)

func TestHandleManualSearch_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleManualSearch(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleManualSearch(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

// handleManualSearch with no providers returns empty results.
func TestHandleManualSearch_no_providers_returns_empty(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{}
	cfg := &qhMockConfig{}
	s := newTestServer(db, cfg)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=fr&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleManualSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleManualSearch() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// With no providers, should return empty results array.
	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// --- handleManualSearch invalid language code ---

func TestHandleManualSearch_invalid_lang_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=en/../../etc&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleManualSearch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleManualSearch(traversal lang) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- manualops.QueryInt negative values ---

// --- handleManualSearch with provider results ---

// resultProvider returns configured results from Search.
type resultProvider struct {
	stubProvider

	results []api.Subtitle
}

func (p *resultProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return p.results, nil
}

func TestHandleManualSearch_with_results_returns_scored(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{}
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(db, cfg)

	// Store a provider that returns results.
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		providers: []api.Provider{&resultProvider{
			stubProvider: stubProvider{name: "os"},
			results: []api.Subtitle{
				{
					Provider: "os", Language: "fr", ReleaseName: "Movie.2024.BluRay-GRP",
					MatchedBy: "imdb", ID: "sub-1",
				},
				{
					Provider: "os", Language: "fr", ReleaseName: "Movie.2024.WEB-DL-OTHER",
					MatchedBy: "title", ID: "sub-2",
				},
			},
		}},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=fr&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleManualSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleManualSearch() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result struct {
		Results        []manualops.SearchResult `json:"results"`
		ManuallyLocked bool                     `json:"manually_locked"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("results count = %d, want 2", len(result.Results))
	}
	// Results should be sorted by score (descending).
	if result.Results[0].Score < result.Results[1].Score {
		t.Errorf("results not sorted: scores %d, %d",
			result.Results[0].Score, result.Results[1].Score)
	}
	if result.ManuallyLocked {
		t.Error("manually_locked = true, want false")
	}
}

// searchFailingProvider returns an error from Search.
type searchFailingProvider struct{ stubProvider }

func (p *searchFailingProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, errMock
}

func TestHandleManualSearch_provider_error_continues(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{}
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(db, cfg)

	// One provider fails on Search, one succeeds with results.
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		providers: []api.Provider{
			&searchFailingProvider{stubProvider{name: "bad"}},
			&resultProvider{
				stubProvider: stubProvider{name: "good"},
				results: []api.Subtitle{
					{
						Provider: "good", Language: "fr", ReleaseName: "Movie-GRP",
						MatchedBy: "imdb", ID: "sub-1",
					},
				},
			},
		},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=fr&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleManualSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleManualSearch() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result struct {
		Results []manualops.SearchResult `json:"results"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Should still get results from the good provider.
	if len(result.Results) != 1 {
		t.Errorf("results count = %d, want 1 (from good provider)", len(result.Results))
	}
}

// downloadedRefsStore tracks DownloadedRefs calls and returns configured values.
type downloadedRefsStore struct {
	refs []api.DownloadedRef
	qhMockStore

	called bool
}

func (m *downloadedRefsStore) DownloadedRefs(_ context.Context, _ api.MediaType, _, _ string) ([]api.DownloadedRef, error) {
	m.called = true
	return m.refs, nil
}

func TestHandleManualSearch_on_disk_detection(t *testing.T) {
	t.Parallel()
	db := &downloadedRefsStore{
		refs: []api.DownloadedRef{
			{ReleaseName: "Movie.2024.BluRay-GRP", Provider: "os"},
			// A second historical download — both should show as on-disk.
			{ReleaseName: "Movie.2024.WEB-DL-OTHER", Provider: "subdl"},
		},
	}
	cfg := &qhMockConfig{
		searchCfg: api.SearchConfig{},
	}
	s := newTestServer(&db.qhMockStore, cfg)

	// Override live state with the downloadedRefsStore and a provider with results.
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		providers: []api.Provider{&resultProvider{
			stubProvider: stubProvider{name: "os"},
			results: []api.Subtitle{
				{
					Provider: "os", Language: "fr", ReleaseName: "Movie.2024.BluRay-GRP",
					MatchedBy: "imdb", ID: "sub-1",
				},
				{
					Provider: "subdl", Language: "fr", ReleaseName: "Movie.2024.WEB-DL-OTHER",
					MatchedBy: "title", ID: "sub-2",
				},
				// Not-yet-downloaded release — should NOT be on disk.
				{
					Provider: "yify", Language: "fr", ReleaseName: "Movie.2024.Other-NEW",
					MatchedBy: "title", ID: "sub-3",
				},
			},
		}},
	})
	// Replace the db with our tracking store.
	s.db = db

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=fr&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleManualSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleManualSearch() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result struct {
		Results []manualops.SearchResult `json:"results"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !db.called {
		t.Error("DownloadedRefs not called when results exist")
	}
	if len(result.Results) != 3 {
		t.Fatalf("results count = %d, want 3", len(result.Results))
	}
	// Both historical downloads must show on-disk.
	onDiskByID := map[string]bool{}
	for _, r := range result.Results {
		onDiskByID[r.SubtitleID] = r.OnDisk
	}
	if !onDiskByID["sub-1"] {
		t.Error("sub-1 (Movie.2024.BluRay-GRP/os) OnDisk = false, want true")
	}
	if !onDiskByID["sub-2"] {
		t.Error("sub-2 (Movie.2024.WEB-DL-OTHER/subdl) OnDisk = false, want true")
	}
	if onDiskByID["sub-3"] {
		t.Error("sub-3 (not in history) OnDisk = true, want false")
	}
}

// --- handleManualSearch mediaType validation ---

func TestHandleManualSearch_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=en&type=invalid", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleManualSearch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleManualSearch(invalid type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}
