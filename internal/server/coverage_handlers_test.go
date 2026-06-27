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

func (m *coverageDetailErrorStore) GetSubtitleFiles(_ context.Context, _ api.MediaType, _ string) ([]api.SubtitleEntry, error) {
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
