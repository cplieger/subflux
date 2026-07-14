package server

import (
	"context"
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
		events:   events.New(0),
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

func (m *clearLockErrorStore) ClearManualLock(_ context.Context, _ api.MediaType, _, _ string, _ api.Variant) error {
	return errMock
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
