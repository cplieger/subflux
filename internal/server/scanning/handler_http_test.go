package scanning

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// extractSegment returns the single path segment that follows prefix, and ""
// for every non-segment case: the prefix is absent, nothing follows the
// prefix, or the remainder spans more than one segment.
func TestExtractSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		path   string
		prefix string
		want   string
	}{
		{"valid single segment", "/api/scan/series/123", "/api/scan/series/", "123"},
		{"empty after prefix", "/api/scan/series/", "/api/scan/series/", ""},
		{"prefix absent", "/other/path", "/api/scan/series/", ""},
		{"sub-path rejected", "/api/scan/series/123/extra", "/api/scan/series/", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractSegment(tc.path, tc.prefix); got != tc.want {
				t.Errorf("extractSegment(%q, %q) = %q, want %q",
					tc.path, tc.prefix, got, tc.want)
			}
		})
	}
}

// --- HTTP validation surface of the /api/scan/* handlers ---
//
// Migrated from the root server package's delegate-era tests
// (scan_handlers_test.go, dismiss_activity_test.go): each case exercises
// the request-validation path of one handler, so a Handler with an empty
// HandlerState (nil Sonarr/Radarr) and no engine deps is sufficient — every
// case fails validation before any scan work starts.

// newValidationHandler builds a Handler with just enough deps for the
// request-validation paths: an empty live state (unconfigured arrs) and a
// real WaitGroup in case a test accidentally reaches the background path.
// The handlers capture their operation snapshot (both state callbacks)
// before the arr-configured checks, so every callback must be non-nil.
func newValidationHandler() *Handler {
	return NewHandler(HandlerDeps{
		StateFunc: func() (*HandlerState, *LiveState) { return &HandlerState{}, &LiveState{} },
		ScanDeps:  func() *Deps { return &Deps{} },
		BGTracker: &sync.WaitGroup{},
	})
}

// --- HandleScanSeries ---

func TestHandleScanSeries_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan/series/1", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeries(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleScanSeries(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanSeries_missing_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeries(empty id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeries_non_numeric_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/abc", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeries(non-numeric id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeries_zero_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/0", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeries(zero id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeries_negative_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeries(negative id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeries_no_sonarr(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/series/42", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeries(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeries(no sonarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "sonarr not configured") {
		t.Errorf("HandleScanSeries(no sonarr) body = %q, want sonarr error", body)
	}
}

// --- HandleScanSeason ---

func TestHandleScanSeason_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan/season/1/2", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleScanSeason(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanSeason_missing_season(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	// Only series ID, no season segment.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeason(missing season) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_non_numeric_series_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/abc/2", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeason(non-numeric series id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_zero_series_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/0/2", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeason(zero series id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_non_numeric_season(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/abc", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeason(non-numeric season) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_negative_season(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeason(negative season) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_no_sonarr(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/2", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeason(no sonarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "sonarr not configured") {
		t.Errorf("HandleScanSeason(no sonarr) body = %q, want sonarr error", body)
	}
}

func TestHandleScanSeason_trailing_slash(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/2/", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeason(trailing slash) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanSeason_zero_season_allowed(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	// Season 0 (specials) is valid; should fail on sonarr nil, not season validation.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/season/42/0", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanSeason(rec, req)

	// Should reach the sonarr nil check (400), not the season validation.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanSeason(season 0) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "sonarr not configured") {
		t.Errorf("HandleScanSeason(season 0) body = %q, want sonarr error (season 0 is valid)",
			body)
	}
}

// --- HandleScanItem ---

func TestHandleScanItem_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan/item", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanItem(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleScanItem(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanItem_invalid_json(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	h.HandleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanItem(bad json) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanItem_zero_media_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	body := `{"media_type":"episode","media_id":0}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanItem(zero media_id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanItem_negative_media_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	body := `{"media_type":"movie","media_id":-5}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanItem(negative media_id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanItem_movie_no_radarr(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	body := `{"media_type":"movie","media_id":42}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanItem(movie, no radarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "radarr not configured") {
		t.Errorf("HandleScanItem(movie) body = %q, want radarr error",
			rec.Body.String())
	}
}

func TestHandleScanItem_episode_no_sonarr(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	body := `{"media_type":"episode","media_id":42}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanItem(episode, no sonarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "sonarr not configured") {
		t.Errorf("HandleScanItem(episode) body = %q, want sonarr error",
			rec.Body.String())
	}
}

func TestHandleScanItem_default_type_is_episode(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	// No media_type specified; should default to episode path (sonarr check).
	body := `{"media_id":42}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanItem(no type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "sonarr not configured") {
		t.Errorf("HandleScanItem(no type) body = %q, want sonarr error (default=episode)",
			rec.Body.String())
	}
}

func TestHandleScanItem_invalid_media_type(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	body := `{"media_type":"series","media_id":42}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/item", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleScanItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanItem(invalid type) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "invalid media_type") {
		t.Errorf("HandleScanItem(invalid type) body = %q, want media_type error",
			rec.Body.String())
	}
}

// --- HandleScanMovie ---

func TestHandleScanMovie_rejects_non_post(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan/movie/42", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanMovie(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleScanMovie(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleScanMovie_missing_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanMovie(empty id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanMovie_non_numeric_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/abc", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanMovie(non-numeric id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanMovie_zero_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/0", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanMovie(zero id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanMovie_negative_id(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanMovie(negative id) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

func TestHandleScanMovie_no_radarr(t *testing.T) {
	t.Parallel()
	h := newValidationHandler()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan/movie/42", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleScanMovie(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleScanMovie(no radarr) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "radarr not configured") {
		t.Errorf("HandleScanMovie(no radarr) body = %q, want radarr error", body)
	}
}
