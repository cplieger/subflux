package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// --- handleScanSeries ---

func TestHandleScanSeries_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan/series/1", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeries(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScanSeries(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanSeries_missing_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeries(empty id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeries_non_numeric_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/abc", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeries(non-numeric id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeries_zero_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/0", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeries(zero id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeries_negative_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/-1", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeries(negative id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeries_no_sonarr(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/42", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeries(no sonarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "sonarr not configured") {
		t.Errorf("handleScanSeries(no sonarr) body = %q, want sonarr error", body)
	}
}

// --- handleScanSeason ---

func TestHandleScanSeason_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan/season/1/2", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScanSeason(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanSeason_missing_season(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Only series ID, no season segment.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeason(missing season) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_non_numeric_series_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/abc/2", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeason(non-numeric series id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_zero_series_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/0/2", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeason(zero series id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_non_numeric_season(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/abc", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeason(non-numeric season) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_negative_season(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/-1", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeason(negative season) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_no_sonarr(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/2", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeason(no sonarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "sonarr not configured") {
		t.Errorf("handleScanSeason(no sonarr) body = %q, want sonarr error", body)
	}
}

func TestHandleScanSeason_trailing_slash(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/2/", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeason(trailing slash) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_zero_season_allowed(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Season 0 (specials) is valid; should fail on sonarr nil, not season validation.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/0", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanSeason(rec, req)

	// Should reach the sonarr nil check (400), not the season validation.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanSeason(season 0) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "sonarr not configured") {
		t.Errorf("handleScanSeason(season 0) body = %q, want sonarr error (season 0 is valid)",
			body)
	}
}

// --- handleScanItem ---

func TestHandleScanItem_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan/item", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScanItem(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScanItem(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanItem_invalid_json(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	s.handleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanItem(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanItem_zero_media_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"episode","media_id":0}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanItem(zero media_id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanItem_negative_media_id(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"movie","media_id":-5}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanItem(negative media_id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanItem_movie_no_radarr(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"movie","media_id":42}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanItem(movie, no radarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "radarr not configured") {
		t.Errorf("handleScanItem(movie) body = %q, want radarr error",
			rec.Body.String())
	}
}

func TestHandleScanItem_episode_no_sonarr(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"episode","media_id":42}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanItem(episode, no sonarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "sonarr not configured") {
		t.Errorf("handleScanItem(episode) body = %q, want sonarr error",
			rec.Body.String())
	}
}

func TestHandleScanItem_default_type_is_episode(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// No media_type specified; should default to episode path (sonarr check).
	body := `{"media_id":42}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanItem(no type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "sonarr not configured") {
		t.Errorf("handleScanItem(no type) body = %q, want sonarr error (default=episode)",
			rec.Body.String())
	}
}

func TestHandleScanItem_invalid_media_type(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"media_type":"series","media_id":42}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleScanItem(invalid type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "invalid media_type") {
		t.Errorf("handleScanItem(invalid type) body = %q, want media_type error",
			rec.Body.String())
	}
}

// --- handleBackoffByPrefix ---

func TestHandleBackoffByPrefix_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/backoff/prefix", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoffByPrefix(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleBackoffByPrefix(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleBackoffByPrefix_defaults_to_episode(t *testing.T) {
	t.Parallel()
	db := &backoffPrefixTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{query: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/backoff/prefix?prefix=tvdb-81189-", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoffByPrefix(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleBackoffByPrefix() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	if db.mediaType != "episode" {
		t.Errorf("GetBackoffByPrefix mediaType = %q, want %q",
			db.mediaType, "episode")
	}
}

func TestHandleBackoffByPrefix_passes_type_and_prefix(t *testing.T) {
	t.Parallel()
	db := &backoffPrefixTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{query: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/backoff/prefix?type=movie&prefix=tmdb-123-", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoffByPrefix(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleBackoffByPrefix() status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	if db.mediaType != "movie" {
		t.Errorf("GetBackoffByPrefix mediaType = %q, want %q",
			db.mediaType, "movie")
	}
	if db.prefix != "tmdb-123-" {
		t.Errorf("GetBackoffByPrefix prefix = %q, want %q",
			db.prefix, "tmdb-123-")
	}
}

func TestHandleBackoffByPrefix_db_error_returns_500(t *testing.T) {
	t.Parallel()
	db := &backoffPrefixErrorStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{query: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/backoff/prefix", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleBackoffByPrefix(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleBackoffByPrefix(db error) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

// backoffPrefixTrackingStore tracks params passed to GetBackoffByPrefix.
type backoffPrefixTrackingStore struct {
	mediaType api.MediaType
	prefix    string
	qhMockStore
}

func (m *backoffPrefixTrackingStore) GetBackoffByPrefix(_ context.Context, mediaType api.MediaType, prefix string) ([]api.BackoffEntry, error) {
	m.mediaType = mediaType
	m.prefix = prefix
	return nil, nil
}

// backoffPrefixErrorStore returns an error from GetBackoffByPrefix.
type backoffPrefixErrorStore struct{ qhMockStore }

func (m *backoffPrefixErrorStore) GetBackoffByPrefix(_ context.Context, _ api.MediaType, _ string) ([]api.BackoffEntry, error) {
	return nil, errMock
}

func TestHandleBackoffByPrefix_invalid_prefix_returns_400(t *testing.T) {
	t.Parallel()
	s := &Server{
		db:       &qhMockStore{},
		stores:   storeFacade{query: &qhMockStore{}},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

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
			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodGet, "/api/backoff/prefix?prefix="+tt.prefix, http.NoBody)
			rec := httptest.NewRecorder()
			s.handleBackoffByPrefix(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("handleBackoffByPrefix(prefix=%q) status = %d, want %d",
					tt.prefix, rec.Code, http.StatusBadRequest)
			}
		})
	}
}
