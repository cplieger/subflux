package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"pgregory.net/rapid"
)

// dummyArrClient is a non-nil api.ArrClient for tests that need sonarr/radarr != nil
// to reach deeper handler branches. All methods return errors or empty results.
type dummyArrClient struct{}

func (dummyArrClient) Ping(context.Context) error                      { return nil }
func (dummyArrClient) GetSeries(context.Context) ([]api.Series, error) { return nil, nil }
func (dummyArrClient) GetEpisodes(context.Context, int) ([]api.Episode, error) {
	return nil, nil
}
func (dummyArrClient) GetMovies(context.Context) ([]api.Movie, error) { return nil, nil }
func (dummyArrClient) GetHistorySince(context.Context, time.Time, api.HistoryEventType) ([]api.HistoryEntry, error) {
	return nil, nil
}

func (dummyArrClient) GetWantedEpisodes(context.Context, map[int]struct{}, func(api.Series, api.Episode) error) error {
	return nil
}

func (dummyArrClient) GetWantedMovies(context.Context, map[int]struct{}, func(api.Movie) error) error {
	return nil
}

func (dummyArrClient) ResolveExcludeTagIDs(context.Context, []string, bool) map[int]struct{} {
	return nil
}
func (dummyArrClient) RefreshSeries(context.Context, int) error { return nil }
func (dummyArrClient) RefreshMovie(context.Context, int) error  { return nil }
func (dummyArrClient) GetSeriesByID(context.Context, int) (*api.Series, error) {
	return nil, nil
}

func (dummyArrClient) GetEpisodeByID(context.Context, int) (*api.Episode, error) {
	return nil, nil
}

func (dummyArrClient) GetMovieByID(context.Context, int) (*api.Movie, error) {
	return nil, nil
}

// --- handleCoverageSeries ---

func TestHandleCoverageSeries_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleCoverageSeries(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCoverageSeries_no_sonarr_returns_empty(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageSeries() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleCoverageSeries(no sonarr) body = %q, want %q", body, "[]")
	}
}

// --- handleCoverageMovies ---

func TestHandleCoverageMovies_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleCoverageMovies(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCoverageMovies_no_radarr_returns_empty(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageMovies() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleCoverageMovies(no radarr) body = %q, want %q", body, "[]")
	}
}

// --- handleCoverageDetail ---

func TestHandleCoverageDetail_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/coverage/series/123", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageDetail(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleCoverageDetail(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCoverageDetail_missing_tvdb_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Path without a tvdb ID segment.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series/", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageDetail(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleCoverageDetail(missing id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCoverageDetail_invalid_tvdb_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series/abc", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageDetail(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleCoverageDetail(non-numeric id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCoverageDetail_valid_tvdb_id_returns_files(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series/81189", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageDetail(valid id) status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	// With empty DB, should return null (no files).
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

func TestHandleCoverageDetail_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &coverageDetailErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series/81189", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageDetail(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleCoverageDetail(db error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// coverageDetailErrorStore returns an error from GetSubtitleFiles.
type coverageDetailErrorStore struct{ qhMockStore }

func (m *coverageDetailErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return nil, errMock
}

// --- handleScanStates ---

func TestHandleScanStates_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/coverage/scan-state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanStates(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScanStates(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanStates_defaults_to_episode(t *testing.T) {
	t.Parallel()
	db := &scanStateTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	// No type param — should default to "episode".
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/scan-state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanStates(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleScanStates() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	if db.mediaType != "episode" {
		t.Errorf("GetScanStates mediaType = %q, want %q", db.mediaType, "episode")
	}
}

func TestHandleScanStates_passes_type_and_prefix(t *testing.T) {
	t.Parallel()
	db := &scanStateTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/scan-state?type=movie&prefix=tmdb-123-", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanStates(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleScanStates() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	if db.mediaType != "movie" {
		t.Errorf("GetScanStates mediaType = %q, want %q", db.mediaType, "movie")
	}
	if db.prefix != "tmdb-123-" {
		t.Errorf("GetScanStates prefix = %q, want %q", db.prefix, "tmdb-123-")
	}
}

func TestHandleScanStates_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &scanStateErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/scan-state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanStates(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleScanStates(db error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// scanStateTrackingStore tracks the params passed to GetScanStates.
type scanStateTrackingStore struct {
	mediaType api.MediaType
	prefix    string
	qhMockStore
}

func (m *scanStateTrackingStore) GetScanStates(_ context.Context, mediaType api.MediaType, prefix string) ([]api.ScanStateRow, error) {
	m.mediaType = mediaType
	m.prefix = prefix
	return nil, nil
}

// scanStateErrorStore returns an error from GetScanStates.
type scanStateErrorStore struct{ qhMockStore }

func (m *scanStateErrorStore) GetScanStates(_ context.Context, _ api.MediaType, _ string) ([]api.ScanStateRow, error) {
	return nil, errMock
}

// --- extractPathSegment additional edge cases ---

func TestExtractPathSegment_table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		path   string
		prefix string
		suffix string
		want   string
	}{
		{name: "valid extraction", path: "/api/media/series/42/episodes", prefix: "/api/media/series/", suffix: "/episodes", want: "42"},
		{name: "non-numeric segment", path: "/api/media/series/abc/episodes", prefix: "/api/media/series/", suffix: "/episodes", want: "abc"},
		{name: "missing suffix", path: "/api/media/series/42", prefix: "/api/media/series/", suffix: "/episodes", want: ""},
		{name: "wrong prefix", path: "/other/path/42/episodes", prefix: "/api/media/series/", suffix: "/episodes", want: ""},
		{name: "empty suffix extracts rest", path: "/api/coverage/series/81189", prefix: "/api/coverage/series/", suffix: "", want: "81189"},
		{name: "empty segment between prefix and suffix", path: "/api/media/series//episodes", prefix: "/api/media/series/", suffix: "/episodes", want: ""},
		{name: "nested suffix", path: "/api/media/series/42/episodes/extra", prefix: "/api/media/series/", suffix: "/episodes", want: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractPathSegment(tt.path, tt.prefix, tt.suffix)
			if got != tt.want {
				t.Errorf("extractPathSegment(%q, %q, %q) = %q, want %q",
					tt.path, tt.prefix, tt.suffix, got, tt.want)
			}
		})
	}
}

// --- handleMediaEpisodes additional branches ---

func TestHandleMediaEpisodes_missing_series_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	// Sonarr must be non-nil to reach the ID extraction branch.
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: dummyArrClient{},
	})

	// Path with no ID segment: /api/media/series//episodes
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/media/series//episodes", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleMediaEpisodes(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleMediaEpisodes(empty id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleMediaEpisodes_non_numeric_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: dummyArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/media/series/abc/episodes", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleMediaEpisodes(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleMediaEpisodes(non-numeric id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleMediaEpisodes_negative_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: dummyArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/media/series/-1/episodes", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleMediaEpisodes(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleMediaEpisodes(negative id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleMediaEpisodes_zero_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: dummyArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/media/series/0/episodes", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleMediaEpisodes(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleMediaEpisodes(zero id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- activity.AlertLog.recordWarn and recordInfo ---

func TestAlertLog_recordWarn_sets_warn_level(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordWarn("sonarr", "warning message")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 1 {
		t.Fatalf("alerts count = %d, want 1", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[0].Level != "warn" {
		t.Errorf("alert.Level = %q, want %q", al.AlertsUnsafe()[0].Level, "warn")
	}
	if al.AlertsUnsafe()[0].Kind != activity.AlertTransient {
		t.Errorf("alert.Kind = %q, want %q", al.AlertsUnsafe()[0].Kind, activity.AlertTransient)
	}
}

func TestAlertLog_recordInfo_sets_info_level_with_short_ttl(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordInfo("scan complete")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 1 {
		t.Fatalf("alerts count = %d, want 1", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[0].Level != "info" {
		t.Errorf("alert.Level = %q, want %q", al.AlertsUnsafe()[0].Level, "info")
	}
	if al.AlertsUnsafe()[0].TTL == 0 {
		t.Error("alert.TTL should be non-zero for info alerts")
	}
}

// --- visibleAlerts respects per-alert TTL ---

func TestVisibleAlerts_respects_custom_ttl(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	// Add an info alert with a very short TTL that has already expired.
	al.RecordInfo("old info")
	al.Lock()
	// Backdate the alert so its 10-minute TTL has expired.
	al.AlertsUnsafe()[0].Time = al.AlertsUnsafe()[0].Time.Add(-15 * time.Minute)
	al.Unlock()

	visible := al.VisibleAlerts()
	if len(visible) != 0 {
		t.Errorf("visibleAlerts() returned %d alerts, want 0 (info TTL expired)",
			len(visible))
	}
}

// --- handleState empty result returns valid JSON ---

func TestHandleState_empty_result_returns_valid_json(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{state: nil}
	s := newTestServer(db, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/state", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleState() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify the response is valid JSON (null is acceptable for nil slice).
	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("handleState() returned invalid JSON: %v", err)
	}
}

// --- indexSubStatus ---

func TestIndexSubStatus(t *testing.T) {
	t.Parallel()
	ignoredCodecs := map[string]bool{"pgs": true}

	cases := []struct {
		ignored      map[string]bool
		checkKey     covKey
		name         string
		checkMediaID string
		files        []api.SubtitleFileRow
		wantMediaIDs int
		wantUsable   bool
		wantIgnored  bool
	}{
		{
			name:         "empty_input",
			files:        nil,
			ignored:      nil,
			wantMediaIDs: 0,
		},
		{
			name:         "external_sub_is_usable",
			files:        []api.SubtitleFileRow{{MediaID: "tvdb-123-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"}},
			ignored:      nil,
			wantMediaIDs: 1,
			checkMediaID: "tvdb-123-s01e01",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   true,
			wantIgnored:  false,
		},
		{
			name:         "embedded_ignored_codec",
			files:        []api.SubtitleFileRow{{MediaID: "tvdb-123-s01e01", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"}},
			ignored:      ignoredCodecs,
			wantMediaIDs: 1,
			checkMediaID: "tvdb-123-s01e01",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   false,
			wantIgnored:  true,
		},
		{
			name:         "embedded_non_ignored_codec",
			files:        []api.SubtitleFileRow{{MediaID: "tvdb-123-s01e01", Language: "fr", Variant: "standard", Source: "embedded", Codec: "srt"}},
			ignored:      ignoredCodecs,
			wantMediaIDs: 1,
			checkMediaID: "tvdb-123-s01e01",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   true,
			wantIgnored:  false,
		},
		{
			name: "usable_overrides_ignored",
			files: []api.SubtitleFileRow{
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
			},
			ignored:      ignoredCodecs,
			wantMediaIDs: 1,
			checkMediaID: "ep1",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   true,
			wantIgnored:  false,
		},
		{
			name: "ignored_does_not_override_usable",
			files: []api.SubtitleFileRow{
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
			},
			ignored:      ignoredCodecs,
			wantMediaIDs: 1,
			checkMediaID: "ep1",
			checkKey:     covKey{Lang: "fr", Variant: "standard"},
			wantUsable:   true,
			wantIgnored:  false,
		},
		{
			name: "multiple_media_ids",
			files: []api.SubtitleFileRow{
				{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
				{MediaID: "ep2", Language: "en", Variant: "hi", Source: "embedded", Codec: "pgs"},
			},
			ignored:      ignoredCodecs,
			wantMediaIDs: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			idx := indexSubStatus(tc.files, tc.ignored)

			if tc.wantMediaIDs == 0 {
				if len(idx) != 0 {
					t.Errorf("indexSubStatus() returned %d entries, want 0", len(idx))
				}
				return
			}

			if len(idx) != tc.wantMediaIDs {
				t.Fatalf("indexSubStatus() returned %d media IDs, want %d", len(idx), tc.wantMediaIDs)
			}

			if tc.checkMediaID == "" {
				// Special case: multiple_media_ids — check both.
				if !idx["ep1"][covKey{Lang: "fr", Variant: "standard"}].Usable {
					t.Error("ep1 fr/standard should be usable")
				}
				if idx["ep2"][covKey{Lang: "en", Variant: "hi"}].Usable {
					t.Error("ep2 en/hi should not be usable (ignored pgs)")
				}
				return
			}

			st := idx[tc.checkMediaID][tc.checkKey]
			if st == nil {
				t.Fatalf("expected non-nil status for %s %v", tc.checkMediaID, tc.checkKey)
			}
			if st.Usable != tc.wantUsable {
				t.Errorf("Usable = %v, want %v", st.Usable, tc.wantUsable)
			}
			if st.IgnoredOnly != tc.wantIgnored {
				t.Errorf("IgnoredOnly = %v, want %v", st.IgnoredOnly, tc.wantIgnored)
			}
		})
	}
}

// --- resolveRuleName ---

func TestResolveRuleName_with_audio_lang(t *testing.T) {
	t.Parallel()
	got := resolveRuleName("en", []api.SubtitleTarget{{Code: "fr"}})
	if got != "en" {
		t.Errorf("resolveRuleName(en, targets) = %q, want %q", got, "en")
	}
}

func TestResolveRuleName_empty_lang_returns_default(t *testing.T) {
	t.Parallel()
	got := resolveRuleName("", []api.SubtitleTarget{{Code: "fr"}})
	if got != ruleDefault {
		t.Errorf("resolveRuleName('', targets) = %q, want %q", got, ruleDefault)
	}
}

func TestResolveRuleName_no_targets_returns_no_targets(t *testing.T) {
	t.Parallel()
	got := resolveRuleName("en", nil)
	if got != ruleNoTargets {
		t.Errorf("resolveRuleName(en, nil) = %q, want %q", got, ruleNoTargets)
	}
}

// --- deduplicateFileRows ---

func TestDeduplicateFileRows_collapses_duplicates(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleFileRow{
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Path: "/a.srt"},
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Path: "/b.srt"},
		{MediaID: "ep1", Language: "en", Variant: "hi", Source: "embedded"},
	}
	got := deduplicateFileRows(rows)
	if len(got) != 2 {
		t.Errorf("deduplicateFileRows() returned %d rows, want 2", len(got))
	}
}

func TestDeduplicateFileRows_preserves_distinct_rows(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleFileRow{
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external"},
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "embedded"},
		{MediaID: "ep1", Language: "en", Variant: "standard", Source: "external"},
		{MediaID: "ep2", Language: "fr", Variant: "standard", Source: "external"},
	}
	got := deduplicateFileRows(rows)
	if len(got) != 4 {
		t.Errorf("deduplicateFileRows() returned %d rows, want 4 (all distinct)", len(got))
	}
}

func TestDeduplicateFileRows_empty_input(t *testing.T) {
	t.Parallel()
	got := deduplicateFileRows([]api.SubtitleFileRow{})
	if len(got) != 0 {
		t.Errorf("deduplicateFileRows(empty) returned %d rows, want 0", len(got))
	}
}

func TestDeduplicateFileRows_preserves_order(t *testing.T) {
	t.Parallel()
	rows := []api.SubtitleFileRow{
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Path: "/first.srt"},
		{MediaID: "ep1", Language: "fr", Variant: "standard", Source: "external", Path: "/second.srt"},
	}
	got := deduplicateFileRows(rows)
	if len(got) != 1 {
		t.Fatalf("deduplicateFileRows() returned %d rows, want 1", len(got))
	}
	if got[0].Path != "/first.srt" {
		t.Errorf("deduplicateFileRows() kept path %q, want %q (first seen)", got[0].Path, "/first.srt")
	}
}

// --- handleCoverageSeries full path tests ---

// coverageSeriesArrClient returns canned series data for coverage tests.
type coverageSeriesArrClient struct{ dummyArrClient }

func (c coverageSeriesArrClient) GetSeries(_ context.Context) ([]api.Series, error) {
	return []api.Series{
		{
			ID:               1,
			Title:            "Test Show",
			Year:             2024,
			TvdbID:           81189,
			ImdbID:           "tt1234567",
			FirstAired:       "2024-01-01",
			OriginalLanguage: &api.LanguageInfo{Name: "English"},
			Statistics:       &api.SeriesStatistics{EpisodeFileCount: 3},
			Tags:             []int{1},
		},
		{
			ID:         2,
			Title:      "No Episodes",
			TvdbID:     99999,
			Statistics: &api.SeriesStatistics{EpisodeFileCount: 0},
		},
	}, nil
}

// coverageSeriesStore returns subtitle files for coverage series tests.
type coverageSeriesStore struct{ qhMockStore }

func (m *coverageSeriesStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return []api.SubtitleFileRow{
		{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
		{MediaID: "tvdb-81189-s01e02", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
	}, nil
}

func TestHandleCoverageSeries_returns_series_with_coverage(t *testing.T) {
	t.Parallel()
	db := &coverageSeriesStore{}
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
		providers: map[api.ProviderID]api.ProviderCfg{
			"embedded": {Enabled: true, Settings: map[string]any{"ignore_pgs": true}},
		},
	}
	s := newTestServer(&db.qhMockStore, cfg)
	ls := s.state()
	s.db = db
	s.stores.cov = db
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: coverageSeriesArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageSeries() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []seriesCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Series with 0 episodes should be skipped.
	if len(result) != 1 {
		t.Fatalf("handleCoverageSeries() returned %d series, want 1", len(result))
	}

	s0 := result[0]
	if s0.Title != "Test Show" {
		t.Errorf("series title = %q, want %q", s0.Title, "Test Show")
	}
	if s0.TvdbID != 81189 {
		t.Errorf("series tvdb_id = %d, want %d", s0.TvdbID, 81189)
	}
	if s0.Episodes != 3 {
		t.Errorf("series episodes = %d, want %d", s0.Episodes, 3)
	}
	if s0.AudioLang != "en" {
		t.Errorf("series audio_lang = %q, want %q", s0.AudioLang, "en")
	}
	if s0.Rule != "en" {
		t.Errorf("series rule = %q, want %q", s0.Rule, "en")
	}

	if len(s0.Targets) != 1 {
		t.Fatalf("series targets count = %d, want 1", len(s0.Targets))
	}
	tc := s0.Targets[0]
	if tc.Language != "fr" {
		t.Errorf("target language = %q, want %q", tc.Language, "fr")
	}
	if tc.Have != 1 {
		t.Errorf("target have = %d, want 1 (one external srt)", tc.Have)
	}
	if tc.HaveIgnored != 1 {
		t.Errorf("target have_ignored = %d, want 1 (one ignored pgs)", tc.HaveIgnored)
	}
	if tc.Total != 3 {
		t.Errorf("target total = %d, want 3", tc.Total)
	}
}

// coverageSeriesErrorArrClient returns an error from GetSeries.
type coverageSeriesErrorArrClient struct{ dummyArrClient }

func (c coverageSeriesErrorArrClient) GetSeries(_ context.Context) ([]api.Series, error) {
	return nil, errMock
}

func TestHandleCoverageSeries_get_series_error_returns_502(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: coverageSeriesErrorArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("handleCoverageSeries(GetSeries error) status = %d, want %d",
			rec.Code, http.StatusBadGateway)
	}
}

// coverageSeriesDBErrorStore returns an error from GetSubtitleFiles.
type coverageSeriesDBErrorStore struct{ qhMockStore }

func (m *coverageSeriesDBErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return nil, errMock
}

func TestHandleCoverageSeries_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &coverageSeriesDBErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{
		cfg:    &qhMockConfig{},
		sonarr: dummyArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleCoverageSeries(DB error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleCoverageSeries_no_targets_sets_rule_no_targets(t *testing.T) {
	t.Parallel()
	db := &coverageSeriesStore{}
	cfg := &qhMockConfig{
		targets: nil,
	}
	s := newTestServer(&db.qhMockStore, cfg)
	ls := s.state()
	s.db = db
	s.stores.cov = db
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: coverageSeriesArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageSeries() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []seriesCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 series, got %d", len(result))
	}
	if result[0].Rule != ruleNoTargets {
		t.Errorf("series rule = %q, want %q", result[0].Rule, ruleNoTargets)
	}
}

// coverageSeriesNoLangArrClient returns a series with no OriginalLanguage.
type coverageSeriesNoLangArrClient struct{ dummyArrClient }

func (c coverageSeriesNoLangArrClient) GetSeries(_ context.Context) ([]api.Series, error) {
	return []api.Series{
		{
			ID:         1,
			Title:      "No Lang Show",
			TvdbID:     55555,
			Statistics: &api.SeriesStatistics{EpisodeFileCount: 2},
		},
	}, nil
}

func TestHandleCoverageSeries_no_original_language_uses_default_rule(t *testing.T) {
	t.Parallel()
	db := &coverageSeriesStore{}
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	s := newTestServer(&db.qhMockStore, cfg)
	ls := s.state()
	s.db = db
	s.stores.cov = db
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		sonarr: coverageSeriesNoLangArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageSeries(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []seriesCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 series, got %d", len(result))
	}
	if result[0].Rule != "default" {
		t.Errorf("rule = %q, want %q", result[0].Rule, "default")
	}
}

// --- handleCoverageMovies full path tests ---

// coverageMoviesArrClient returns canned movie data for coverage tests.
type coverageMoviesArrClient struct{ dummyArrClient }

func (c coverageMoviesArrClient) GetMovies(_ context.Context) ([]api.Movie, error) {
	return []api.Movie{
		{
			ID:               1,
			Title:            "Test Movie",
			Year:             2024,
			TmdbID:           12345,
			ImdbID:           "tt9876543",
			InCinemas:        "2024-06-01",
			DigitalRelease:   "2024-09-01",
			HasFile:          true,
			OriginalLanguage: &api.LanguageInfo{Name: "English"},
			MovieFile:        &api.MovieFile{Path: "/movies/test.mkv", SceneName: "Test.Movie.2024"},
			Tags:             []int{2},
		},
		{
			ID:      2,
			Title:   "No File Movie",
			TmdbID:  99999,
			HasFile: false,
		},
	}, nil
}

// coverageMoviesStore returns subtitle files for coverage movie tests.
type coverageMoviesStore struct{ qhMockStore }

func (m *coverageMoviesStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return []api.SubtitleFileRow{
		{MediaID: "tmdb-12345", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
	}, nil
}

func TestHandleCoverageMovies_returns_movies_with_coverage(t *testing.T) {
	t.Parallel()
	db := &coverageMoviesStore{}
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	s := newTestServer(&db.qhMockStore, cfg)
	ls := s.state()
	s.db = db
	s.stores.cov = db
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		radarr: coverageMoviesArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleCoverageMovies() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []movieCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Movie without file should be skipped.
	if len(result) != 1 {
		t.Fatalf("handleCoverageMovies() returned %d movies, want 1", len(result))
	}

	m0 := result[0]
	if m0.Title != "Test Movie" {
		t.Errorf("movie title = %q, want %q", m0.Title, "Test Movie")
	}
	if m0.TmdbID != 12345 {
		t.Errorf("movie tmdb_id = %d, want %d", m0.TmdbID, 12345)
	}
	if m0.Path != "/movies/test.mkv" {
		t.Errorf("movie path = %q, want %q", m0.Path, "/movies/test.mkv")
	}
	if m0.SceneName != "Test.Movie.2024" {
		t.Errorf("movie scene_name = %q, want %q", m0.SceneName, "Test.Movie.2024")
	}
	if m0.AudioLang != "en" {
		t.Errorf("movie audio_lang = %q, want %q", m0.AudioLang, "en")
	}
	if !m0.HasFile {
		t.Error("movie has_file should be true")
	}

	if len(m0.Targets) != 1 {
		t.Fatalf("movie targets count = %d, want 1", len(m0.Targets))
	}
	tc := m0.Targets[0]
	if tc.Have != 1 {
		t.Errorf("target have = %d, want 1", tc.Have)
	}
	if tc.Total != 1 {
		t.Errorf("target total = %d, want 1", tc.Total)
	}
}

// coverageMoviesErrorArrClient returns an error from GetMovies.
type coverageMoviesErrorArrClient struct{ dummyArrClient }

func (c coverageMoviesErrorArrClient) GetMovies(_ context.Context) ([]api.Movie, error) {
	return nil, errMock
}

func TestHandleCoverageMovies_get_movies_error_returns_502(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		radarr: coverageMoviesErrorArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("handleCoverageMovies(GetMovies error) status = %d, want %d",
			rec.Code, http.StatusBadGateway)
	}
}

// coverageMoviesDBErrorStore returns an error from GetSubtitleFiles.
type coverageMoviesDBErrorStore struct{ qhMockStore }

func (m *coverageMoviesDBErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleFileRow, error) {
	return nil, errMock
}

func TestHandleCoverageMovies_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &coverageMoviesDBErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{
		cfg:    &qhMockConfig{},
		radarr: dummyArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleCoverageMovies(DB error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// coverageMoviesNilFileArrClient returns a movie with HasFile=true but nil MovieFile.
type coverageMoviesNilFileArrClient struct{ dummyArrClient }

func (c coverageMoviesNilFileArrClient) GetMovies(_ context.Context) ([]api.Movie, error) {
	return []api.Movie{
		{
			ID:      1,
			Title:   "Nil File Movie",
			TmdbID:  77777,
			HasFile: true,
		},
	}, nil
}

func TestHandleCoverageMovies_nil_movie_file_omits_path(t *testing.T) {
	t.Parallel()
	db := &qhMockStore{}
	cfg := &qhMockConfig{
		targets: []api.SubtitleTarget{{Code: "fr"}},
	}
	s := newTestServer(db, cfg)
	ls := s.state()
	s.live.Store(&liveState{
		cfg:    ls.cfg,
		engine: ls.engine,
		scorer: ls.scorer,
		radarr: coverageMoviesNilFileArrClient{},
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/movies", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageMovies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var result []movieCoverage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 movie, got %d", len(result))
	}
	if result[0].Path != "" {
		t.Errorf("path = %q, want empty (nil MovieFile)", result[0].Path)
	}
	if result[0].SceneName != "" {
		t.Errorf("scene_name = %q, want empty (nil MovieFile)", result[0].SceneName)
	}
}

// --- handleCoverageDetail additional tests ---

func TestHandleCoverageDetail_negative_tvdb_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/series/-5", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleCoverageDetail(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleCoverageDetail(negative id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- handleScanStates mediaType validation ---

func TestHandleScanStates_invalid_type_returns_400(t *testing.T) {
	t.Parallel()
	db := &scanStateTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{cov: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/coverage/scan-state?type=foo", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanStates(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanStates(type=foo) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- extractSeriesPrefix ---

func TestExtractSeriesPrefix_table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "standard_episode", input: "tvdb-12345-s01e01", want: "tvdb-12345-"},
		{name: "double_digit_season", input: "tvdb-99999-s12e05", want: "tvdb-99999-"},
		{name: "imdb_prefix", input: "imdb-tt1234567-s03e10", want: "imdb-tt1234567-"},
		{name: "empty_string", input: "", want: ""},
		{name: "no_dash_s_pattern", input: "tmdb-12345", want: ""},
		{name: "single_char", input: "s", want: ""},
		{name: "just_dash_s", input: "-s", want: "-"},
		{name: "trailing_s_no_dash", input: "abcs", want: ""},
		{name: "multiple_dash_s", input: "tvdb-123-s01-s02e01", want: "tvdb-123-s01-"},
		{name: "dash_s_at_start", input: "-s01e01", want: "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractSeriesPrefix(tc.input)
			if got != tc.want {
				t.Errorf("extractSeriesPrefix(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractSeriesPrefix_property_roundtrip_with_BuildEpisodeID(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		tvdbID := rapid.IntRange(1, 999999).Draw(t, "tvdb_id")
		season := rapid.IntRange(0, 99).Draw(t, "season")
		episode := rapid.IntRange(1, 999).Draw(t, "episode")

		epID := api.BuildEpisodeID(tvdbID, "", season, episode)
		prefix := extractSeriesPrefix(epID)
		wantPrefix := api.BuildSeriesPrefix(tvdbID, "")

		if prefix != wantPrefix {
			t.Fatalf("extractSeriesPrefix(%q) = %q, want %q", epID, prefix, wantPrefix)
		}
	})
}

// --- countEpisodeCoverageGrouped ---

func TestCountEpisodeCoverageGrouped_empty_episodes(t *testing.T) {
	t.Parallel()
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countEpisodeCoverageGrouped(nil, targets, 10)
	if len(got) != 1 {
		t.Fatalf("countEpisodeCoverageGrouped(nil, 1 target, 10) len = %d, want 1", len(got))
	}
	if got[0].Have != 0 || got[0].HaveIgnored != 0 || got[0].Total != 10 {
		t.Errorf("countEpisodeCoverageGrouped(nil) = {Have:%d, HaveIgnored:%d, Total:%d}, want {0, 0, 10}",
			got[0].Have, got[0].HaveIgnored, got[0].Total)
	}
}

func TestCountEpisodeCoverageGrouped_counts_usable_and_ignored(t *testing.T) {
	t.Parallel()
	episodes := []map[covKey]*covStatus{
		{covKey{Lang: "fr", Variant: "standard"}: {Usable: true}},
		{covKey{Lang: "fr", Variant: "standard"}: {IgnoredOnly: true}},
		{covKey{Lang: "en", Variant: "standard"}: {Usable: true}},
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countEpisodeCoverageGrouped(episodes, targets, 5)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Have != 1 {
		t.Errorf("countEpisodeCoverageGrouped Have = %d, want 1", got[0].Have)
	}
	if got[0].HaveIgnored != 1 {
		t.Errorf("countEpisodeCoverageGrouped HaveIgnored = %d, want 1", got[0].HaveIgnored)
	}
	if got[0].Total != 5 {
		t.Errorf("countEpisodeCoverageGrouped Total = %d, want 5", got[0].Total)
	}
}

func TestCountEpisodeCoverageGrouped_multiple_targets(t *testing.T) {
	t.Parallel()
	episodes := []map[covKey]*covStatus{
		{
			covKey{Lang: "fr", Variant: "standard"}: {Usable: true},
			covKey{Lang: "en", Variant: "forced"}:   {Usable: true},
		},
	}
	targets := []api.SubtitleTarget{
		{Code: "fr"},
		{Code: "en", Variant: "forced"},
	}
	got := countEpisodeCoverageGrouped(episodes, targets, 3)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Language != "fr" || got[0].Have != 1 {
		t.Errorf("target[0] = {lang:%q, have:%d}, want {fr, 1}", got[0].Language, got[0].Have)
	}
	if got[1].Language != "en" || got[1].Have != 1 {
		t.Errorf("target[1] = {lang:%q, have:%d}, want {en, 1}", got[1].Language, got[1].Have)
	}
}

func TestCountEpisodeCoverageGrouped_no_targets(t *testing.T) {
	t.Parallel()
	episodes := []map[covKey]*covStatus{
		{covKey{Lang: "fr", Variant: "standard"}: {Usable: true}},
	}
	got := countEpisodeCoverageGrouped(episodes, nil, 5)
	if len(got) != 0 {
		t.Errorf("countEpisodeCoverageGrouped(no targets) len = %d, want 0", len(got))
	}
}

// --- countMovieCoverage ---

func TestCountMovieCoverage_nil_subs(t *testing.T) {
	t.Parallel()
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countMovieCoverage(nil, targets)
	if len(got) != 1 {
		t.Fatalf("countMovieCoverage(nil) len = %d, want 1", len(got))
	}
	if got[0].Have != 0 || got[0].HaveIgnored != 0 || got[0].Total != 1 {
		t.Errorf("countMovieCoverage(nil) = {Have:%d, HaveIgnored:%d, Total:%d}, want {0, 0, 1}",
			got[0].Have, got[0].HaveIgnored, got[0].Total)
	}
}

func TestCountMovieCoverage_usable_sub(t *testing.T) {
	t.Parallel()
	subs := map[covKey]*covStatus{
		{Lang: "fr", Variant: "standard"}: {Usable: true},
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countMovieCoverage(subs, targets)
	if got[0].Have != 1 {
		t.Errorf("countMovieCoverage(usable) Have = %d, want 1", got[0].Have)
	}
	if got[0].HaveIgnored != 0 {
		t.Errorf("countMovieCoverage(usable) HaveIgnored = %d, want 0", got[0].HaveIgnored)
	}
}

func TestCountMovieCoverage_ignored_only(t *testing.T) {
	t.Parallel()
	subs := map[covKey]*covStatus{
		{Lang: "fr", Variant: "standard"}: {IgnoredOnly: true},
	}
	targets := []api.SubtitleTarget{{Code: "fr"}}
	got := countMovieCoverage(subs, targets)
	if got[0].Have != 0 {
		t.Errorf("countMovieCoverage(ignored) Have = %d, want 0", got[0].Have)
	}
	if got[0].HaveIgnored != 1 {
		t.Errorf("countMovieCoverage(ignored) HaveIgnored = %d, want 1", got[0].HaveIgnored)
	}
}

func TestCountMovieCoverage_multiple_targets(t *testing.T) {
	t.Parallel()
	subs := map[covKey]*covStatus{
		{Lang: "fr", Variant: "standard"}: {Usable: true},
		{Lang: "en", Variant: "forced"}:   {IgnoredOnly: true},
	}
	targets := []api.SubtitleTarget{
		{Code: "fr"},
		{Code: "en", Variant: "forced"},
		{Code: "de"},
	}
	got := countMovieCoverage(subs, targets)
	if len(got) != 3 {
		t.Fatalf("countMovieCoverage len = %d, want 3", len(got))
	}
	if got[0].Have != 1 || got[0].HaveIgnored != 0 {
		t.Errorf("target fr = {Have:%d, HaveIgnored:%d}, want {1, 0}", got[0].Have, got[0].HaveIgnored)
	}
	if got[1].Have != 0 || got[1].HaveIgnored != 1 {
		t.Errorf("target en = {Have:%d, HaveIgnored:%d}, want {0, 1}", got[1].Have, got[1].HaveIgnored)
	}
	if got[2].Have != 0 || got[2].HaveIgnored != 0 {
		t.Errorf("target de = {Have:%d, HaveIgnored:%d}, want {0, 0}", got[2].Have, got[2].HaveIgnored)
	}
}

func TestCountMovieCoverage_no_targets(t *testing.T) {
	t.Parallel()
	subs := map[covKey]*covStatus{
		{Lang: "fr", Variant: "standard"}: {Usable: true},
	}
	got := countMovieCoverage(subs, nil)
	if len(got) != 0 {
		t.Errorf("countMovieCoverage(no targets) len = %d, want 0", len(got))
	}
}
