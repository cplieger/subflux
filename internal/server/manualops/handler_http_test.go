package manualops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/release"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/httphelpers"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/testsupport"
)

// HTTP surface tests for the manual search/download/clear-lock handlers.
// Migrated from the root server package's delegate-era tests
// (manual_download_test.go, manual_search_test.go, server_status_test.go);
// the Handler is constructed directly with this package's narrow deps and
// a real search engine for accurate score simulation.

var errHTTPFake = errors.New("mock error")

// fakeActivity satisfies ActivityTracker with no-op lifecycle tracking.
type fakeActivity struct{}

func (fakeActivity) Start(string, string, activity.ActivitySource) string { return "act-1" }
func (fakeActivity) End(string)                                           {}
func (fakeActivity) Fail(string)                                          {}
func (fakeActivity) Progress(string, int, int, string)                    {}

// fakeEvents satisfies EventPublisher with no-ops.
type fakeEvents struct{}

func (fakeEvents) PublishNotify(events.NotifyLevel, string)                    {}
func (fakeEvents) PublishCoverageUpdate(api.MediaType, string, string, string) {}

// httpFakeRadarr resolves any movie ID to a fixed file path, so MediaRef
// resolution succeeds in handler tests without a live arr.
type httpFakeRadarr struct{}

func (httpFakeRadarr) GetMovieByID(context.Context, int) (arrapi.Movie, error) {
	return arrapi.Movie{ID: 42, MovieFile: &arrapi.MovieFile{Path: "/media/movie.mkv"}}, nil
}

// httpStubProvider implements api.Provider for test setup.
type httpStubProvider struct {
	name string
}

func (p *httpStubProvider) Name() api.ProviderID { return api.ProviderID(p.name) }

func (p *httpStubProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, nil
}

func (p *httpStubProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, nil
}

// newHTTPHarness builds a Handler wired like the server's composition root:
// a real search engine (accurate score simulation), permissive path
// validation, and no-op activity/alert/event sinks. The returned WaitGroup
// is the handler's BGTracker; Wait it before finishing tests that reach the
// background download path.
func newHTTPHarness(db api.Store, cfg api.ConfigProvider, providers []api.Provider) (*Handler, *sync.WaitGroup) {
	scores := cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(nil,
		search.WithStore(db), search.WithConfig(cfg),
		search.WithMetrics(metrics.New()), search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{}),
		search.WithTracks(embedded.Detector{}))
	wg := &sync.WaitGroup{}
	resolver := &resolve.Resolver{
		Store: db,
		State: func() *resolve.State {
			return &resolve.State{Cfg: cfg, Radarr: httpFakeRadarr{}}
		},
	}
	h := NewHandler(HandlerDeps{
		DBFunc:    func() DownloadStore { return db },
		Activity:  fakeActivity{},
		Alerts:    activity.NewAlertLog(100),
		Events:    fakeEvents{},
		BGTracker: wg,
		ServerCtx: func() context.Context { return context.Background() },
		StateFunc: func() *LiveState {
			return &LiveState{Cfg: cfg, Engine: engine, Providers: providers}
		},
		Resolve:    resolver,
		DecodeJSON: httphelpers.DecodeJSONBody,
	})
	return h, wg
}

func newValidationHarness() *Handler {
	h, _ := newHTTPHarness(&testsupport.NopStore{}, &testsupport.NopConfig{}, nil)
	return h
}

// --- HandleManualDownload ---

func TestHandleManualDownload_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/download", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleManualDownload(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleManualDownload(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleManualDownload_invalid_json(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	h.HandleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleManualDownload(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleManualDownload_missing_required_fields(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	tests := []struct {
		name string
		body string
	}{
		{"missing provider", `{"subtitle_id":"1","media_id":42,"language":"en"}`},
		{"missing subtitle_id", `{"provider":"os","media_id":42,"language":"en"}`},
		{"missing media_id", `{"provider":"os","subtitle_id":"1","language":"en"}`},
		{"missing language", `{"provider":"os","subtitle_id":"1","media_id":42}`},
		{"all empty", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodPost, "/api/search/download", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.HandleManualDownload(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("HandleManualDownload(%s) status = %d, want %d",
					tt.name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandleManualDownload_invalid_lang_returns_400(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	body := `{"provider":"os","subtitle_id":"1","media_id":42,"language":"en/../.."}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleManualDownload(traversal lang) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleManualDownload_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	body := `{"provider":"os","subtitle_id":"1","media_id":42,"language":"en","media_type":"invalid"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleManualDownload(invalid media_type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleManualDownload_provider_not_found(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	body := `{"provider":"nonexistent","subtitle_id":"1","media_id":42,"language":"en"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleManualDownload(unknown provider) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "provider not found") {
		t.Errorf("HandleManualDownload() body = %q, want to contain %q",
			rec.Body.String(), "provider not found")
	}
}

// dlFailingProvider returns an error from Download.
type dlFailingProvider struct{ httpStubProvider }

func (p *dlFailingProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, errHTTPFake
}

func TestHandleManualDownload_download_error_returns_202(t *testing.T) {
	t.Parallel()
	h, wg := newHTTPHarness(&testsupport.NopStore{}, &testsupport.NopConfig{},
		[]api.Provider{&dlFailingProvider{httpStubProvider{name: "os"}}})

	body := `{"provider":"os","subtitle_id":"1","media_id":42,"language":"en"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleManualDownload(rec, req)

	// Async: handler returns 202 immediately; download runs in background.
	if rec.Code != http.StatusAccepted {
		t.Errorf("HandleManualDownload(download error) status = %d, want %d",
			rec.Code, http.StatusAccepted)
	}
	// Wait for the background download goroutine to finish (it fails fast).
	wg.Wait()
}

// --- HandleClearLock ---

func TestHandleClearLock_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/clear-lock", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleClearLock(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleClearLock(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleClearLock_invalid_json(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader("bad"))
	rec := httptest.NewRecorder()
	h.HandleClearLock(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleClearLock(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleClearLock_missing_required_fields(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

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
			h.HandleClearLock(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("HandleClearLock(%s) status = %d, want %d",
					tc.name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHandleClearLock_invalid_lang_returns_400(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	body := `{"media_type":"movie","media_id":"tt123","language":"en/../../etc"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleClearLock(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleClearLock(traversal lang) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleClearLock_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	body := `{"media_type":"invalid","media_id":"tt123","language":"fr"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleClearLock(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleClearLock(invalid media_type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleClearLock_success(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	body := `{"media_type":"movie","media_id":"tt123","language":"fr"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleClearLock(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("HandleClearLock() status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "lock cleared") {
		t.Errorf("HandleClearLock() body = %q, want to contain %q", body, "lock cleared")
	}
}

// clearLockErrorStore is a minimal store whose ClearManualLock fails.
type clearLockErrorStore struct{ testsupport.NopStore }

func (m *clearLockErrorStore) ClearManualLock(_ context.Context, _ api.MediaType, _, _ string, _ api.Variant) error {
	return errHTTPFake
}

func TestHandleClearLock_db_error(t *testing.T) {
	t.Parallel()
	h, _ := newHTTPHarness(&clearLockErrorStore{}, &testsupport.NopConfig{}, nil)

	body := `{"media_type":"movie","media_id":"tt123","language":"fr"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/clear-lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleClearLock(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("HandleClearLock(db error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// --- HandleManualSearch ---

func TestHandleManualSearch_rejects_non_get(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleManualSearch(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleManualSearch(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleManualSearch_no_providers_returns_empty(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=fr&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleManualSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleManualSearch() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// With no providers, should return empty results array.
	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func TestHandleManualSearch_invalid_lang_returns_400(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=en/../../etc&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleManualSearch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleManualSearch(traversal lang) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleManualSearch_invalid_media_type_returns_400(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=en&type=invalid", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleManualSearch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleManualSearch(invalid type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// resultProvider returns configured results from Search.
type resultProvider struct {
	httpStubProvider

	results []api.Subtitle
}

func (p *resultProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return p.results, nil
}

func TestHandleManualSearch_with_results_returns_scored(t *testing.T) {
	t.Parallel()
	h, _ := newHTTPHarness(&testsupport.NopStore{}, &testsupport.NopConfig{},
		[]api.Provider{&resultProvider{
			httpStubProvider: httpStubProvider{name: "os"},
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
		}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=fr&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleManualSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleManualSearch() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result struct {
		Results []SearchResult `json:"results"`
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
}

// searchFailingProvider returns an error from Search.
type searchFailingProvider struct{ httpStubProvider }

func (p *searchFailingProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, errHTTPFake
}

func TestHandleManualSearch_provider_error_continues(t *testing.T) {
	t.Parallel()
	h, _ := newHTTPHarness(&testsupport.NopStore{}, &testsupport.NopConfig{},
		[]api.Provider{
			&searchFailingProvider{httpStubProvider{name: "bad"}},
			&resultProvider{
				httpStubProvider: httpStubProvider{name: "good"},
				results: []api.Subtitle{
					{
						Provider: "good", Language: "fr", ReleaseName: "Movie-GRP",
						MatchedBy: "imdb", ID: "sub-1",
					},
				},
			},
		})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=fr&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleManualSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleManualSearch() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result struct {
		Results []SearchResult `json:"results"`
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
	testsupport.NopStore

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
	h, _ := newHTTPHarness(db, &testsupport.NopConfig{},
		[]api.Provider{&resultProvider{
			httpStubProvider: httpStubProvider{name: "os"},
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
		}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search?imdb=tt1234567&lang=fr&type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleManualSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleManualSearch() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result struct {
		Results []SearchResult `json:"results"`
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

// The release query parameter is direct user input into the release
// parser: exactly MaxNameLen bytes passes, one byte more answers a loud
// 400 (never a silent truncation).
func TestHandleManualSearch_release_length_boundary(t *testing.T) {
	t.Parallel()
	h := newValidationHarness()

	send := func(t *testing.T, releaseLen int) int {
		t.Helper()
		q := url.Values{}
		q.Set("imdb", "tt1234567")
		q.Set("lang", "fr")
		q.Set("type", "movie")
		q.Set("release", strings.Repeat("a", releaseLen))
		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodGet, "/api/search?"+q.Encode(), http.NoBody)
		rec := httptest.NewRecorder()
		h.HandleManualSearch(rec, req)
		return rec.Code
	}

	if code := send(t, release.MaxNameLen); code != http.StatusOK {
		t.Errorf("HandleManualSearch(release len == max) status = %d, want %d", code, http.StatusOK)
	}
	if code := send(t, release.MaxNameLen+1); code != http.StatusBadRequest {
		t.Errorf("HandleManualSearch(release len == max+1) status = %d, want %d", code, http.StatusBadRequest)
	}
}
