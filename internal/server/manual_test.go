package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/serveradapter"
)

func TestParseManualSearchQuery(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		url         string
		wantLang    string
		wantType    api.MediaType
		wantFile    string
		wantTitle   string
		wantRelease string
		wantImdb    string
		wantTmdb    int
		wantTvdb    int
		wantYear    int
		wantSeason  int
		wantEpisode int
	}{
		{
			name: "defaults", url: "/api/search",
			wantLang: "en", wantType: "movie",
		},
		{
			name:     "all_params",
			url:      "/api/search?title=Breaking+Bad&imdb=tt0903747&tmdb=1396&tvdb=81189&lang=fr&type=episode&year=2008&season=1&episode=3&release=Breaking.Bad.S01E03&file=/media/bb.mkv",
			wantLang: "fr", wantType: "episode", wantFile: "/media/bb.mkv",
			wantTitle: "Breaking Bad", wantRelease: "Breaking.Bad.S01E03",
			wantImdb: "tt0903747", wantTmdb: 1396, wantTvdb: 81189,
			wantYear: 2008, wantSeason: 1, wantEpisode: 3,
		},
		{
			name: "infers_episode_type", url: "/api/search?season=2&episode=5",
			wantLang: "en", wantType: "episode", wantSeason: 2, wantEpisode: 5,
		},
		{
			name: "invalid_numbers_ignored", url: "/api/search?year=abc&season=xyz&episode=!",
			wantLang: "en", wantType: "episode", // season+episode params present → infers episode
		},
		{
			name: "file_sets_release_name", url: "/api/search?file=/media/Movie.2024.1080p.mkv",
			wantLang: "en", wantType: "movie", wantFile: "/media/Movie.2024.1080p.mkv",
			wantRelease: "/media/Movie.2024.1080p.mkv",
		},
		{
			name: "release_takes_priority_over_file", url: "/api/search?release=Custom.Release&file=/media/movie.mkv",
			wantLang: "en", wantType: "movie", wantFile: "/media/movie.mkv",
			wantRelease: "Custom.Release",
		},
		{
			name: "negative_numbers_ignored", url: "/api/search?year=-1&season=-5&episode=-10&tmdb=-100&tvdb=-200",
			wantLang: "en", wantType: "episode", // season+episode params present → infers episode
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.url, http.NoBody)
			req, lang, mediaType, filePath := manualops.ParseSearchQuery(r)

			if lang != tc.wantLang {
				t.Errorf("lang = %q, want %q", lang, tc.wantLang)
			}
			if mediaType != tc.wantType {
				t.Errorf("mediaType = %q, want %q", mediaType, tc.wantType)
			}
			if filePath != tc.wantFile {
				t.Errorf("filePath = %q, want %q", filePath, tc.wantFile)
			}
			if tc.wantTitle != "" && req.Title != tc.wantTitle {
				t.Errorf("req.Title = %q, want %q", req.Title, tc.wantTitle)
			}
			if tc.wantRelease != "" && req.ReleaseName != tc.wantRelease {
				t.Errorf("req.ReleaseName = %q, want %q", req.ReleaseName, tc.wantRelease)
			}
			if tc.wantImdb != "" && req.ImdbID != tc.wantImdb {
				t.Errorf("req.ImdbID = %q, want %q", req.ImdbID, tc.wantImdb)
			}
			if req.TmdbID != tc.wantTmdb {
				t.Errorf("req.TmdbID = %d, want %d", req.TmdbID, tc.wantTmdb)
			}
			if req.TvdbID != tc.wantTvdb {
				t.Errorf("req.TvdbID = %d, want %d", req.TvdbID, tc.wantTvdb)
			}
			if req.Year != tc.wantYear {
				t.Errorf("req.Year = %d, want %d", req.Year, tc.wantYear)
			}
			if req.Season != tc.wantSeason {
				t.Errorf("req.Season = %d, want %d", req.Season, tc.wantSeason)
			}
			if req.Episode != tc.wantEpisode {
				t.Errorf("req.Episode = %d, want %d", req.Episode, tc.wantEpisode)
			}
		})
	}
}

func TestBuildManualSearchResults_basic(t *testing.T) {
	t.Parallel()

	scored := []api.ScoredResult{
		{Sub: api.Subtitle{Provider: "os", Language: "fr", ReleaseName: "Movie.2024.srt", MatchedBy: "hash", ID: "123", HearingImp: true, Forced: false}, Score: 300, Matches: map[string]int{"source": 28, "release_group": 23}},
		{Sub: api.Subtitle{Provider: "yify", Language: "en", ReleaseName: "Movie.2024.en.srt", MatchedBy: "title", ID: "456"}, Score: 200},
	}

	refs := []api.DownloadedRef{
		{ReleaseName: "Movie.2024.srt", Provider: "os"},
	}
	results := manualops.BuildSearchResults(scored, refs)

	if len(results) != 2 {
		t.Fatalf("manualops.BuildSearchResults() returned %d results, want 2", len(results))
	}

	// First result should match on-disk.
	r0 := results[0]
	if r0.Provider != "os" {
		t.Errorf("results[0].Provider = %q, want %q", r0.Provider, "os")
	}
	if r0.Language != "fr" {
		t.Errorf("results[0].Language = %q, want %q", r0.Language, "fr")
	}
	if r0.Score != 300 {
		t.Errorf("results[0].Score = %d, want %d", r0.Score, 300)
	}
	if !r0.OnDisk {
		t.Error("results[0].OnDisk = false, want true (matches refs entry)")
	}
	if !r0.HearingImp {
		t.Error("results[0].HearingImp = false, want true")
	}
	if r0.SubtitleID != "123" {
		t.Errorf("results[0].SubtitleID = %q, want %q", r0.SubtitleID, "123")
	}
	if r0.Forced {
		t.Error("results[0].Forced = true, want false")
	}
	if len(r0.Matches) != 2 {
		t.Errorf("results[0].Matches has %d entries, want 2", len(r0.Matches))
	}
	if r0.Matches["source"] != 28 {
		t.Errorf("results[0].Matches[\"source\"] = %d, want 28", r0.Matches["source"])
	}

	// Second result should not match on-disk.
	r1 := results[1]
	if r1.OnDisk {
		t.Error("results[1].OnDisk = true, want false (different provider)")
	}
	if r1.Matches != nil {
		t.Errorf("results[1].Matches = %v, want nil (no matches provided)", r1.Matches)
	}
}

func TestBuildManualSearchResults_limits_to_max(t *testing.T) {
	t.Parallel()

	scored := make([]api.ScoredResult, 60)
	for i := range scored {
		scored[i] = api.ScoredResult{
			Sub:   api.Subtitle{Provider: "os", Language: "en", ID: "id"},
			Score: 100 - i,
		}
	}

	results := manualops.BuildSearchResults(scored, nil)

	if len(results) != manualops.MaxResults {
		t.Errorf("manualops.BuildSearchResults() returned %d results, want %d (capped)",
			len(results), manualops.MaxResults)
	}
}

func TestBuildManualSearchResults_empty_input(t *testing.T) {
	t.Parallel()

	results := manualops.BuildSearchResults(nil, nil)

	if len(results) != 0 {
		t.Errorf("manualops.BuildSearchResults(nil) returned %d results, want 0", len(results))
	}
}

func TestBuildManualSearchResults_fewer_than_10(t *testing.T) {
	t.Parallel()

	scored := []api.ScoredResult{
		{Sub: api.Subtitle{Provider: "os", Language: "fr", ID: "1"}, Score: 100},
	}

	results := manualops.BuildSearchResults(scored, nil)

	if len(results) != 1 {
		t.Errorf("manualops.BuildSearchResults() returned %d results, want 1", len(results))
	}
}

func TestBuildManualSearchResults_on_disk_requires_both_provider_and_release(t *testing.T) {
	t.Parallel()

	scored := []api.ScoredResult{
		{Sub: api.Subtitle{Provider: "os", Language: "fr", ReleaseName: "Movie.srt", ID: "1"}, Score: 100},
	}

	// Same provider, different release.
	results := manualops.BuildSearchResults(scored, []api.DownloadedRef{
		{ReleaseName: "Different.srt", Provider: "os"},
	})
	if results[0].OnDisk {
		t.Error("OnDisk = true with matching provider but different release, want false")
	}

	// Different provider, same release.
	results = manualops.BuildSearchResults(scored, []api.DownloadedRef{
		{ReleaseName: "Movie.srt", Provider: "yify"},
	})
	if results[0].OnDisk {
		t.Error("OnDisk = true with different provider but matching release, want false")
	}
}

func TestBuildManualSearchResults_multiple_historical_matches(t *testing.T) {
	t.Parallel()

	scored := []api.ScoredResult{
		{Sub: api.Subtitle{
			Provider: "os", Language: "fr",
			ReleaseName: "Movie.2024.BluRay-GRP", ID: "1",
		}, Score: 300},
		{Sub: api.Subtitle{
			Provider: "subdl", Language: "fr",
			ReleaseName: "Movie.2024.WEB-DL-OTHER", ID: "2",
		}, Score: 250},
		{Sub: api.Subtitle{
			Provider: "yify", Language: "fr",
			ReleaseName: "Movie.2024.Other-NEW", ID: "3",
		}, Score: 200},
	}

	refs := []api.DownloadedRef{
		{ReleaseName: "Movie.2024.BluRay-GRP", Provider: "os"},
		{ReleaseName: "Movie.2024.WEB-DL-OTHER", Provider: "subdl"},
	}
	results := manualops.BuildSearchResults(scored, refs)

	if !results[0].OnDisk {
		t.Error("results[0] OnDisk = false, want true (first historical entry)")
	}
	if !results[1].OnDisk {
		t.Error("results[1] OnDisk = false, want true (second historical entry)")
	}
	if results[2].OnDisk {
		t.Error("results[2] OnDisk = true, want false (not in history)")
	}
}

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

func TestHandleManualDownload_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/download", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleManualDownload(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleManualDownload_invalid_json(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleManualDownload(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleManualDownload_missing_required_fields(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	tests := []struct {
		name string
		body string
	}{
		{"missing provider", `{"subtitle_id":"1","file_path":"/f","language":"en"}`},
		{"missing subtitle_id", `{"provider":"os","file_path":"/f","language":"en"}`},
		{"missing file_path", `{"provider":"os","subtitle_id":"1","language":"en"}`},
		{"missing language", `{"provider":"os","subtitle_id":"1","file_path":"/f"}`},
		{"all empty", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodPost, "/api/search/download", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			s.handleManualDownload(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("handleManualDownload(%s) status = %d, want %d",
					tt.name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandleClearLock_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/clear-lock", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleClearLock(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleClearLock(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleClearLock_invalid_json(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader("bad"))
	rec := httptest.NewRecorder()
	s.handleClearLock(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleClearLock(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleClearLock_missing_required_fields(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	cases := []struct {
		name string
		body string
	}{
		{"empty media_type", `{"media_type":"","media_id":"tt123","language":"fr"}`},
		{"empty media_id", `{"media_type":"movie","media_id":"","language":"fr"}`},
		{"empty language", `{"media_type":"movie","media_id":"tt123","language":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodPost, "/api/search/clear-lock", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			s.handleClearLock(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("handleClearLock(%s) status = %d, want %d",
					tc.name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandleClearLock_success(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{}
	s := newTestServer(db, &qhMockConfig{})

	body := `{"media_type":"movie","media_id":"tt123","language":"fr"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleClearLock(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleClearLock() status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "lock cleared") {
		t.Errorf("handleClearLock() body = %q, want to contain %q", body, "lock cleared")
	}
}

func TestHandleClearLock_db_error(t *testing.T) {
	t.Parallel()

	db := &clearLockErrorStore{}
	s := &Server{
		db:       db,
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
	}
	s.manualH = manualops.NewHandler(manualops.HandlerDeps{
		DBFunc:       func() manualops.DownloadStore { return s.db.(manualops.DownloadStore) },
		Activity:     &serveradapter.ActivityAdapter{A: s.activity},
		Alerts:       &serveradapter.AlertAdapter{A: s.alerts},
		Events:       &serveradapter.ManualEventAdapter{E: s.events},
		StateFunc:    func() *manualops.LiveState { return &manualops.LiveState{} },
		BGTracker:    &s.bgWg,
		ServerCtx:    func() context.Context { return context.Background() },
		ValidatePath: func(w http.ResponseWriter, r *http.Request, p, l string) bool { return true },
		DecodeJSON:   decodeJSONBodyAny,
	})

	body := `{"media_type":"movie","media_id":"tt123","language":"fr"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleClearLock(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleClearLock(db error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// clearLockErrorStore is a minimal mock that returns an error from ClearManualLock.
type clearLockErrorStore struct{ qhMockStore }

func (m *clearLockErrorStore) ClearManualLock(_ context.Context, _ api.MediaType, _, _ string) error {
	return errMock
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

// --- handleManualDownload invalid language code ---

func TestHandleManualDownload_invalid_lang_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"provider":"os","subtitle_id":"1","file_path":"/media/movie.mkv","language":"en/../.."}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleManualDownload(traversal lang) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- handleManualDownload download error ---

// failingProvider returns an error from Download.
type failingProvider struct{ stubProvider }

func (p *failingProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, errMock
}

func TestHandleManualDownload_download_error_returns_202(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{}
	cfg := &qhMockConfig{}
	s := newTestServer(db, cfg)

	// Store a provider that fails on Download.
	ls := s.state()
	s.live.Store(&liveState{
		cfg:       ls.cfg,
		engine:    ls.engine,
		scorer:    ls.scorer,
		providers: []api.Provider{&failingProvider{stubProvider{name: "os"}}},
	})

	body := `{"provider":"os","subtitle_id":"1","file_path":"/media/movie.mkv","language":"en"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	// Async: handler returns 202 immediately; download runs in background.
	if rec.Code != http.StatusAccepted {
		t.Errorf("handleManualDownload(download error) status = %d, want %d",
			rec.Code, http.StatusAccepted)
	}
}

// --- handleClearLock invalid language code ---

func TestHandleClearLock_invalid_lang_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"movie","media_id":"tt123","language":"en/../../etc"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleClearLock(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleClearLock(traversal lang) status = %d, want %d",
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

// --- lookupMediaTitle ---

// movieTitleArrClient returns a movie with a known title.
type movieTitleArrClient struct{ dummyArrClient }

func (movieTitleArrClient) GetMovieByID(_ context.Context, _ int) (*api.Movie, error) {
	return &api.Movie{Title: "Inception", TmdbID: 27205}, nil
}

// seriesTitleArrClient returns a series with a known title.
type seriesTitleArrClient struct{ dummyArrClient }

func (seriesTitleArrClient) GetSeriesByID(_ context.Context, _ int) (*api.Series, error) {
	return &api.Series{Title: "Breaking Bad", TvdbID: 81189}, nil
}

// arrErrorClient returns errors from GetMovieByID and GetSeriesByID.
type arrErrorClient struct{ dummyArrClient }

func (arrErrorClient) GetMovieByID(_ context.Context, _ int) (*api.Movie, error) {
	return nil, errMock
}

func (arrErrorClient) GetSeriesByID(_ context.Context, _ int) (*api.Series, error) {
	return nil, errMock
}

func TestLookupMediaTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		radarr    api.ArrClient
		sonarr    api.ArrClient
		name      string
		mediaType api.MediaType
		want      string
		arrID     int
	}{
		{name: "movie with radarr", mediaType: "movie", arrID: 42, radarr: movieTitleArrClient{}, sonarr: nil, want: "Inception"},
		{name: "episode with sonarr", mediaType: "episode", arrID: 7, radarr: nil, sonarr: seriesTitleArrClient{}, want: "Breaking Bad"},
		{name: "movie with nil radarr", mediaType: "movie", arrID: 42, radarr: nil, sonarr: nil, want: ""},
		{name: "episode with nil sonarr", mediaType: "episode", arrID: 7, radarr: nil, sonarr: nil, want: ""},
		{name: "zero arrID", mediaType: "movie", arrID: 0, radarr: movieTitleArrClient{}, sonarr: nil, want: ""},
		{name: "negative arrID", mediaType: "movie", arrID: -1, radarr: movieTitleArrClient{}, sonarr: nil, want: ""},
		{name: "movie radarr error", mediaType: "movie", arrID: 42, radarr: arrErrorClient{}, sonarr: nil, want: ""},
		{name: "episode sonarr error", mediaType: "episode", arrID: 7, radarr: nil, sonarr: arrErrorClient{}, want: ""},
		{name: "unknown media type", mediaType: "unknown", arrID: 42, radarr: movieTitleArrClient{}, sonarr: seriesTitleArrClient{}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ls := &liveState{
				radarr: tt.radarr,
				sonarr: tt.sonarr,
			}
			got := lookupMediaTitle(context.Background(), ls, tt.mediaType, tt.arrID)
			if got != tt.want {
				t.Errorf("lookupMediaTitle(ctx, ls, %q, %d) = %q, want %q",
					tt.mediaType, tt.arrID, got, tt.want)
			}
		})
	}
}

// --- lookupMovieMediaID ---

func TestLookupMovieMediaID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		radarr api.ArrClient
		name   string
		want   string
		arrID  int
	}{
		{name: "success returns tmdb prefix", radarr: movieTitleArrClient{}, arrID: 42, want: "tmdb-27205"},
		{name: "nil radarr returns empty", radarr: nil, arrID: 42, want: ""},
		{name: "radarr error returns empty", radarr: arrErrorClient{}, arrID: 42, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{
				db:       &qhMockStore{},
				activity: activity.New(50),
				alerts:   activity.NewAlertLog(100),
			}
			s.live.Store(&liveState{radarr: tt.radarr})

			got := s.lookupMovieMediaID(context.Background(), s.state(), tt.arrID)
			if got != tt.want {
				t.Errorf("lookupMovieMediaID(ctx, ls, %d) = %q, want %q",
					tt.arrID, got, tt.want)
			}
		})
	}
}

// --- lookupEpisodeMediaID ---

func TestLookupEpisodeMediaID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		sonarr  api.ArrClient
		name    string
		want    string
		series  int
		season  int
		episode int
	}{
		{name: "success returns tvdb episode ID", sonarr: seriesTitleArrClient{}, series: 7, season: 3, episode: 5, want: "tvdb-81189-s03e05"},
		{name: "nil sonarr returns empty", sonarr: nil, series: 7, season: 3, episode: 5, want: ""},
		{name: "sonarr error returns empty", sonarr: arrErrorClient{}, series: 7, season: 3, episode: 5, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{
				db:       &qhMockStore{},
				activity: activity.New(50),
				alerts:   activity.NewAlertLog(100),
			}
			s.live.Store(&liveState{sonarr: tt.sonarr})

			got := s.lookupEpisodeMediaID(context.Background(), s.state(), tt.series, tt.season, tt.episode)
			if got != tt.want {
				t.Errorf("lookupEpisodeMediaID(ctx, ls, %d, %d, %d) = %q, want %q",
					tt.series, tt.season, tt.episode, got, tt.want)
			}
		})
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

// --- handleManualDownload mediaType validation ---

func TestHandleManualDownload_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"provider":"os","subtitle_id":"1","file_path":"/media/movie.mkv","language":"en","media_type":"invalid"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleManualDownload(invalid media_type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- handleClearLock mediaType validation ---

func TestHandleClearLock_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"invalid","media_id":"tt123","language":"fr"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleClearLock(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleClearLock(invalid media_type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestResolveMediaIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		radarr       api.ArrClient
		sonarr       api.ArrClient
		name         string
		mediaType    api.MediaType
		wantCoverage string
		wantHistory  string
		arrID        int
		season       int
		episode      int
	}{
		{
			name:         "movie with successful radarr lookup",
			mediaType:    "movie",
			arrID:        42,
			radarr:       movieTitleArrClient{},
			wantCoverage: "tmdb-27205",
			wantHistory:  "tmdb-27205",
		},
		{
			name:         "episode with successful sonarr lookup",
			mediaType:    "episode",
			arrID:        7,
			season:       3,
			episode:      5,
			sonarr:       seriesTitleArrClient{},
			wantCoverage: "tvdb-81189-s03e05",
			wantHistory:  "tvdb-81189-s03e05",
		},
		{
			name:         "movie with radarr error falls back to radarr-N",
			mediaType:    "movie",
			arrID:        42,
			radarr:       arrErrorClient{},
			wantCoverage: "",
			wantHistory:  "radarr-42",
		},
		{
			name:         "episode with sonarr error falls back to sonarr-N-sNNeNN",
			mediaType:    "episode",
			arrID:        7,
			season:       3,
			episode:      5,
			sonarr:       arrErrorClient{},
			wantCoverage: "",
			wantHistory:  "sonarr-7-s03e05",
		},
		{
			name:         "movie with nil radarr and arrID falls back to radarr-N",
			mediaType:    "movie",
			arrID:        10,
			wantCoverage: "",
			wantHistory:  "radarr-10",
		},
		{
			name:         "episode with nil sonarr and arrID falls back to sonarr-N-sNNeNN",
			mediaType:    "episode",
			arrID:        10,
			season:       1,
			episode:      2,
			wantCoverage: "",
			wantHistory:  "sonarr-10-s01e02",
		},
		{
			name:         "movie with zero arrID falls back to BuildMediaID",
			mediaType:    "movie",
			arrID:        0,
			radarr:       movieTitleArrClient{},
			wantCoverage: "",
			wantHistory:  "",
		},
		{
			name:         "episode with zero arrID falls back to BuildMediaID",
			mediaType:    "episode",
			arrID:        0,
			season:       1,
			episode:      2,
			sonarr:       seriesTitleArrClient{},
			wantCoverage: "",
			wantHistory:  "s00e00",
		},
		{
			name:         "unknown media type with arrID falls back to sonarr format",
			mediaType:    "unknown",
			arrID:        5,
			season:       2,
			episode:      3,
			wantCoverage: "",
			wantHistory:  "sonarr-5-s02e03",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{
				db:       &qhMockStore{},
				activity: activity.New(50),
				alerts:   activity.NewAlertLog(100),
			}
			s.live.Store(&liveState{radarr: tt.radarr, sonarr: tt.sonarr})

			coverageID, historyID := s.resolveMediaIDs(
				context.Background(), s.state(),
				tt.mediaType, tt.arrID, tt.season, tt.episode,
			)
			if coverageID != tt.wantCoverage {
				t.Errorf("resolveMediaIDs() coverageID = %q, want %q",
					coverageID, tt.wantCoverage)
			}
			if historyID != tt.wantHistory {
				t.Errorf("resolveMediaIDs() historyID = %q, want %q",
					historyID, tt.wantHistory)
			}
		})
	}
}
