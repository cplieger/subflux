package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/serveradapter"
	"pgregory.net/rapid"
)

var errMock = errors.New("mock error")

func TestHandleHealth_returns_ok(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	s.ready.Store(true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/health", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleHealth() status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("handleHealth() Content-Type = %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("handleHealth() body not JSON: %v; body=%s", err, rec.Body.String())
	}
	if got["status"] != "ok" {
		t.Errorf("handleHealth() status = %q, want %q", got["status"], "ok")
	}
	if _, has := got["reason"]; has {
		t.Errorf("handleHealth() ok response carried reason: %v", got)
	}
}

func TestHandleHealth_not_ready(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/health", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("handleHealth() status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("handleHealth() Content-Type = %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("handleHealth() body not JSON: %v; body=%s", err, rec.Body.String())
	}
	if got["status"] != "unready" {
		t.Errorf("handleHealth() status = %q, want %q", got["status"], "unready")
	}
	if got["reason"] == "" {
		t.Errorf("handleHealth() unready response missing reason")
	}
}

func TestHandleScan_rejects_non_post(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScan(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScan(GET) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestSecurityHeaders_sets_all_headers(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := securityHeaders(inner)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
		{"Cache-Control", "no-store"},
		{"Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; media-src 'self' blob:"},
		{"Permissions-Policy", "camera=(), microphone=(), geolocation=()"},
		{"Cross-Origin-Opener-Policy", "same-origin"},
	}

	for _, tt := range tests {
		got := rec.Header().Get(tt.header)
		if got != tt.want {
			t.Errorf("securityHeaders() %s = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestSecurityHeaders_passes_through_to_next_handler(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Custom", "test")
		w.WriteHeader(http.StatusTeapot)
	})
	handler := securityHeaders(inner)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("securityHeaders() status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	if got := rec.Header().Get("X-Custom"); got != "test" {
		t.Errorf("inner handler header X-Custom = %q, want %q", got, "test")
	}
}

func TestHandleGetAlerts_returns_recent_alerts(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Add a recent alert and an old alert.
	s.alerts.Record("sonarr", "recent error")
	s.alerts.Lock()
	s.alerts.AppendAlert(activity.Alert{
		ID: -1, Level: "error", Source: "old", Message: "old error",
		Kind: activity.AlertTransient, Time: time.Now().Add(-48 * time.Hour),
	})
	s.alerts.Unlock()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/alerts", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetAlerts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetAlerts() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var alerts []activity.Alert
	if err := json.NewDecoder(rec.Body).Decode(&alerts); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Only the recent alert (within 1h default TTL) should be returned.
	if len(alerts) != 1 {
		t.Errorf("handleGetAlerts() returned %d alerts, want 1 (only recent)", len(alerts))
	}
	if len(alerts) > 0 && alerts[0].Source != "sonarr" {
		t.Errorf("alerts[0].Source = %q, want %q", alerts[0].Source, "sonarr")
	}
}

func TestHandleGetAlerts_empty_when_no_alerts(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/alerts", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetAlerts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetAlerts() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Should return an empty JSON array, not null.
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleGetAlerts() body = %q, want %q", body, "[]")
	}
}

func TestHandleGetActivity_returns_last_20(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Add 25 entries manually with distinct IDs.
	s.activity.Lock()
	for i := range 25 {
		s.activity.AppendEntry(activity.Entry{
			ID:     time.Now().Format("20060102150405.000") + string(rune('A'+i)),
			Action: "Scan",
			Detail: "scan",
		})
	}
	s.activity.Unlock()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetActivity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetActivity() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entries []activity.Entry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 20 {
		t.Errorf("handleGetActivity() returned %d entries, want 20 (capped)", len(entries))
	}
}

func TestHandleGetActivity_returns_all_when_under_20(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	s.activity.Start("Scan", "scan 1", "scheduled")
	s.activity.Start("Upgrade", "upgrade 1", "manual")

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetActivity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetActivity() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var entries []activity.Entry
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("handleGetActivity() returned %d entries, want 2", len(entries))
	}
}

func TestHandleConfigParsed_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/parsed", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleConfigParsed(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleConfigParsed(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleManualDownload_path_validation_failure(t *testing.T) {
	t.Parallel()

	cfg := &pathValidationErrorConfig{}
	s := &Server{
		db:       &qhMockStore{},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		events:   events.New(),
	}
	s.live.Store(&liveState{cfg: cfg})
	s.manualH = manualops.NewHandler(manualops.HandlerDeps{
		DBFunc:       func() manualops.DownloadStore { return s.db.(manualops.DownloadStore) },
		Activity:     &serveradapter.ActivityAdapter{A: s.activity},
		Alerts:       &serveradapter.AlertAdapter{A: s.alerts},
		Events:       &serveradapter.ManualEventAdapter{E: s.events},
		StateFunc:    func() *manualops.LiveState { return &manualops.LiveState{} },
		BGTracker:    &s.bgWg,
		ServerCtx:    func() context.Context { return context.Background() },
		ValidatePath: s.validateFSPath,
		DecodeJSON:   decodeJSONBodyAny,
	})

	body := `{"provider":"os","subtitle_id":"1","file_path":"/evil/path","language":"en"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("handleManualDownload(invalid path) status = %d, want %d",
			rec.Code, http.StatusForbidden)
	}
}

// pathValidationErrorConfig returns an error from ValidatePath.
type pathValidationErrorConfig struct{ qhMockConfig }

func (m *pathValidationErrorConfig) ValidatePath(_ context.Context, _ string) error {
	return errors.New("path not under media roots")
}

func (m *pathValidationErrorConfig) RemoveUnderRoot(_ context.Context, _ string) error {
	return config.ErrPathNotAllowed
}

func TestHandleManualDownload_provider_not_found(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := `{"provider":"nonexistent","subtitle_id":"1","file_path":"/media/movie.mkv","language":"en"}`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/download", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleManualDownload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleManualDownload(unknown provider) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "provider not found") {
		t.Errorf("handleManualDownload() body = %q, want to contain %q",
			rec.Body.String(), "provider not found")
	}
}

// configFilePath is tested implicitly by the config handler tests.

func TestWriteJSON_sets_content_type(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	api.WriteJSON(rec, map[string]string{"key": "value"})

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("api.WriteJSON() Content-Type = %q, want %q", ct, "application/json")
	}

	var result map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["key"] != "value" {
		t.Errorf("api.WriteJSON() key = %q, want %q", result["key"], "value")
	}
}

func TestHandleState_filters_passed_through(t *testing.T) {
	t.Parallel()

	db := &filterTrackingStore{}
	s := &Server{
		db:       db,
		stores:   storeFacade{query: db},
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/state?type=episode&lang=fr&provider=os", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleState() status = %d, want %d", rec.Code, http.StatusOK)
	}
	if db.mediaType != "episode" {
		t.Errorf("GetState mediaType = %q, want %q", db.mediaType, "episode")
	}
	if db.language != "fr" {
		t.Errorf("GetState language = %q, want %q", db.language, "fr")
	}
	if db.provider != "os" {
		t.Errorf("GetState provider = %q, want %q", db.provider, "os")
	}
}

// filterTrackingStore tracks the filter params passed to GetState.
type filterTrackingStore struct {
	mediaType api.MediaType
	language  string
	provider  string
	search    string
	qhMockStore
}

func (m *filterTrackingStore) GetState(_ context.Context, q *api.StateQuery) ([]api.StateEntry, error) {
	m.mediaType = q.MediaType
	m.language = q.Language
	m.provider = string(q.Provider)
	m.search = q.Search
	m.stateLimit = q.Limit
	return nil, nil
}

func TestHandleScan_post_returns_accepted(t *testing.T) {
	t.Parallel()
	// handleScan launches runFullScan in a goroutine which needs sonarr/radarr clients.
	// We only test the synchronous response (202 Accepted).
	s := &Server{
		db:       &qhMockStore{},
		metrics:  metrics.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		ctx:      context.Background(),
	}
	ls := &liveState{cfg: &qhMockConfig{}}
	s.live.Store(ls)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScan(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("handleScan(POST) status = %d, want %d",
			rec.Code, http.StatusAccepted)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "scan started" {
		t.Errorf("handleScan(POST) status = %q, want %q", resp["status"], "scan started")
	}
}

func TestHandleGetConfig_reads_file(t *testing.T) {
	// Uses t.Setenv, cannot be parallel.
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	content := "search:\n  scan_interval: 24h\n"
	if err := writeTestFile(configPath, content); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetConfig() status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/yaml" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/yaml")
	}
	if body := rec.Body.String(); body != content {
		t.Errorf("handleGetConfig() body = %q, want %q", body, content)
	}
}

func TestHandleGetConfig_missing_file_returns_500(t *testing.T) {
	cfgFilePath = "/nonexistent/config.yaml"

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleGetConfig(missing file) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleGetConfig_oversized_file_returns_500(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	// Create a file larger than 1 MB.
	bigContent := strings.Repeat("x", 1<<20+1)
	if err := writeTestFile(configPath, bigContent); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleGetConfig(oversized) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleSaveConfig_invalid_yaml_returns_400(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	body := "not: valid: yaml: config: [[[["
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveConfig(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleSaveConfig(invalid yaml) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// writeTestFile is a helper to write content to a file path.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// --- Mutant-killing boundary tests ---

// Kills CONDITIONALS_BOUNDARY at config_handlers.go:101 (fi.Size() > maxConfigSize → >= maxConfigSize).
// A file exactly at 1MB must be accepted (200), not rejected as "too large" (500).
func TestHandleGetConfig_file_exactly_at_max_size(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	// Create a file exactly 1 MB (1 << 20 bytes).
	exactContent := strings.Repeat("x", 1<<20)
	if err := writeTestFile(configPath, exactContent); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	// File at exactly 1MB should be accepted (200), not rejected (500).
	if rec.Code == http.StatusInternalServerError {
		t.Errorf("handleGetConfig(exactly 1MB) status = %d, want 200 (not rejected as too large)",
			rec.Code)
	}
}

// Kills CONDITIONALS_NEGATION at config_handlers.go:137 (err != nil → err == nil on ReadAll).
// A valid PUT to /api/config must not return 400 "failed to read body".
// The mutant would enter the error branch on successful ReadAll.
func TestHandleSaveConfig_valid_body_not_rejected_as_read_error(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	// Write initial config so the file exists.
	if err := writeTestFile(configPath, "initial: true"); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Send valid YAML that will fail LoadFromBytes validation (no arr configured),
	// but the ReadAll step must succeed. The error should be 400 with a validation
	// message, NOT "failed to read body".
	body := `languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
providers:
  os:
    enabled: true
`
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveConfig(rec, req)

	// Should be 400 (validation error), not 400 with "failed to read body".
	if rec.Code == http.StatusBadRequest && strings.Contains(rec.Body.String(), "failed to read body") {
		t.Error("handleSaveConfig returned 'failed to read body' for valid body; ReadAll negation mutant detected")
	}
}

// --- handleGetAlerts persistent alert filtering ---

func TestHandleGetAlerts_rejects_unsupported_method(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPut, "/api/alerts", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetAlerts(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleGetAlerts(PUT) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleGetAlerts_delete_dispatches_to_dismiss(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	s.alerts.Record("sonarr", "test error")

	s.alerts.RLock()
	id := s.alerts.AlertsUnsafe()[0].ID
	s.alerts.RUnlock()

	// DELETE via handleGetAlerts should dispatch to handleDismissAlert.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodDelete, "/api/alerts?id="+strconv.Itoa(id), http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetAlerts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetAlerts(DELETE) status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify the alert was dismissed.
	s.alerts.RLock()
	dismissed := s.alerts.AlertsUnsafe()[0].Dismissed
	s.alerts.RUnlock()

	if !dismissed {
		t.Error("alert should be dismissed after DELETE dispatch")
	}
}

// --- handleGetActivity method check ---

// --- asyncAction conflict path ---

func TestHandleGetActivity_empty_returns_empty_array(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// No activities added — entries is nil.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/activity", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetActivity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetActivity() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Should return an empty JSON array, not null.
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("handleGetActivity() body = %q, want %q (empty array, not null)", body, "[]")
	}
}

func TestHandleGetConfig_directory_returns_500(t *testing.T) {
	// Uses t.Setenv, cannot be parallel.
	dir := t.TempDir()
	cfgFilePath = dir // Point at a directory, not a file.

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleGetConfig(directory) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleScan_conflict_when_already_running(t *testing.T) {
	t.Parallel()
	s := &Server{
		db:       &qhMockStore{},
		metrics:  metrics.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		ctx:      context.Background(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	// Simulate a scan already in progress.
	s.scanning.Store(true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScan(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("handleScan(already running) status = %d, want %d",
			rec.Code, http.StatusConflict)
	}
}

// --- handleSaveConfig POST method ---

func TestHandleSaveConfig_post_method_accepted(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// POST with invalid YAML should return 400 (validation error),
	// not 405 (method not allowed). This verifies POST is accepted.
	body := "not: valid: yaml: config: [[[["
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSaveConfig(rec, req)

	if rec.Code == http.StatusMethodNotAllowed {
		t.Errorf("handleSaveConfig(POST) status = %d, want POST to be accepted (not 405)",
			rec.Code)
	}
	// Should be 400 (invalid YAML), confirming POST was accepted.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("handleSaveConfig(POST, invalid yaml) status = %d, want %d",
			rec.Code, http.StatusBadRequest)
	}
}

// --- handleGetConfig redacts secrets ---

func TestHandleGetConfig_redacts_secrets(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	content := "providers:\n  os:\n    api_key: my-secret-key\n    enabled: true\n"
	if err := writeTestFile(configPath, content); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	cfgFilePath = configPath

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleGetConfig() status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if strings.Contains(body, "my-secret-key") {
		t.Error("handleGetConfig() response contains unredacted secret")
	}
	if !strings.Contains(body, "********") {
		t.Error("handleGetConfig() response missing redaction placeholder")
	}
}

// --- scanning.SortByTitle ---

func TestSortByTitle_mixed(t *testing.T) {
	t.Parallel()
	episodes := []scanning.ScanItem{
		{Series: &api.Series{Title: "Zorro"}},
		{Series: &api.Series{Title: "Archer"}},
	}
	movies := []scanning.ScanItem{
		{Movie: &api.Movie{Title: "Batman"}},
		{Movie: &api.Movie{Title: "Alien"}},
	}
	got := scanning.SortByTitle(episodes, movies)
	want := []string{"Alien", "Archer", "Batman", "Zorro"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if scanning.ScanItemTitle(got[i]) != w {
			t.Errorf("[%d] = %q, want %q",
				i, scanning.ScanItemTitle(got[i]), w)
		}
	}
}

func TestSortByTitle_case_insensitive(t *testing.T) {
	t.Parallel()
	episodes := []scanning.ScanItem{
		{Series: &api.Series{Title: "the Office"}},
	}
	movies := []scanning.ScanItem{
		{Movie: &api.Movie{Title: "The Matrix"}},
	}
	got := scanning.SortByTitle(episodes, movies)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if scanning.ScanItemTitle(got[0]) != "The Matrix" {
		t.Errorf("[0] = %q, want %q",
			scanning.ScanItemTitle(got[0]), "The Matrix")
	}
}

func TestSortByTitle_both_empty(t *testing.T) {
	t.Parallel()
	got := scanning.SortByTitle(nil, nil)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestSortByTitle_one_empty(t *testing.T) {
	t.Parallel()
	movies := []scanning.ScanItem{
		{Movie: &api.Movie{Title: "Zulu"}},
		{Movie: &api.Movie{Title: "Alpha"}},
	}
	got := scanning.SortByTitle(nil, movies)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if scanning.ScanItemTitle(got[0]) != "Alpha" {
		t.Errorf("[0] = %q, want %q",
			scanning.ScanItemTitle(got[0]), "Alpha")
	}
}

func TestSortByTitle_secondary_sort_by_season_episode(t *testing.T) {
	t.Parallel()
	episodes := []scanning.ScanItem{
		{Series: &api.Series{Title: "Show"}, Ep: &api.Episode{SeasonNumber: 2, EpisodeNumber: 1}},
		{Series: &api.Series{Title: "Show"}, Ep: &api.Episode{SeasonNumber: 1, EpisodeNumber: 3}},
		{Series: &api.Series{Title: "Show"}, Ep: &api.Episode{SeasonNumber: 1, EpisodeNumber: 1}},
	}
	got := scanning.SortByTitle(episodes, nil)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	s0, e0 := scanning.ScanItemSeasonEp(got[0])
	s1, e1 := scanning.ScanItemSeasonEp(got[1])
	s2, e2 := scanning.ScanItemSeasonEp(got[2])
	if s0 != 1 || e0 != 1 {
		t.Errorf("[0] = S%02dE%02d, want S01E01", s0, e0)
	}
	if s1 != 1 || e1 != 3 {
		t.Errorf("[1] = S%02dE%02d, want S01E03", s1, e1)
	}
	if s2 != 2 || e2 != 1 {
		t.Errorf("[2] = S%02dE%02d, want S02E01", s2, e2)
	}
}

// --- handleUI tests ---

func TestHandleUI_serves_index_at_root(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleUI(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleUI(/) status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("handleUI(/) Content-Type = %q, want text/html", ct)
	}
}

func TestHandleUI_serves_static_file(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// favicon.svg is tracked in internal/server/static/; CSS bundles
	// (style.css, login.css) are generated by the Dockerfile and not
	// available at test time, so we use a tracked asset instead.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/favicon.svg", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleUI(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleUI(/favicon.svg) status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "svg") {
		t.Errorf("handleUI(/favicon.svg) Content-Type = %q, want svg", ct)
	}
}

func TestHandleUI_spa_fallback_for_unknown_path(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/history/some-id", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleUI(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleUI(/history/some-id) status = %d, want %d", rec.Code, http.StatusOK)
	}
	// SPA fallback should serve index.html.
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("handleUI(/history/some-id) Content-Type = %q, want text/html (SPA fallback)", ct)
	}
}

func TestHandleUI_directory_path_serves_index(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// /icons/ is a directory in the embedded FS; should fall through to index.html.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/icons/", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleUI(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleUI(/icons/) status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("handleUI(/icons/) Content-Type = %q, want text/html (directory fallback)", ct)
	}
}

// --- asyncAction direct tests ---

func TestAsyncAction_non_post_returns_405(t *testing.T) {
	t.Parallel()
	var flag atomic.Bool
	called := false
	s := &Server{}

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/test", http.NoBody)
	rec := httptest.NewRecorder()
	s.asyncAction(context.Background(), rec, req, &flag, "busy", "started", func(context.Context) { called = true })

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("asyncAction(GET) status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if called {
		t.Error("asyncAction(GET) should not invoke fn")
	}
}

func TestAsyncAction_conflict_when_flag_set(t *testing.T) {
	t.Parallel()
	var flag atomic.Bool
	flag.Store(true)
	s := &Server{}

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/test", http.NoBody)
	rec := httptest.NewRecorder()
	s.asyncAction(context.Background(), rec, req, &flag, "custom busy msg", "started", func(context.Context) {})

	if rec.Code != http.StatusConflict {
		t.Errorf("asyncAction(conflict) status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "custom busy msg" {
		t.Errorf("asyncAction(conflict) error = %q, want %q", resp["error"], "custom busy msg")
	}
}

func TestAsyncAction_post_returns_accepted(t *testing.T) {
	t.Parallel()
	var flag atomic.Bool
	s := &Server{}

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/test", http.NoBody)
	rec := httptest.NewRecorder()
	s.asyncAction(context.Background(), rec, req, &flag, "busy", "custom started", func(context.Context) {})

	if rec.Code != http.StatusAccepted {
		t.Errorf("asyncAction(POST) status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "custom started" {
		t.Errorf("asyncAction(POST) status = %q, want %q", resp["status"], "custom started")
	}
}

// --- Property-based tests ---

func TestExtractAltTitles_property_no_primary_no_dupes(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		primary := rapid.String().Draw(t, "primary")
		n := rapid.IntRange(0, 10).Draw(t, "n")
		alts := make([]api.AlternateTitle, n)
		for i := range n {
			alts[i] = api.AlternateTitle{
				Title: rapid.String().Draw(t, fmt.Sprintf("alt_%d", i)),
			}
		}

		got := scanning.ExtractAltTitles(alts, primary)

		// Invariant 1: primary title never in output.
		for _, title := range got {
			if strings.EqualFold(title, primary) {
				t.Errorf("ExtractAltTitles output contains primary %q", primary)
			}
		}

		// Invariant 2: no case-insensitive duplicates.
		seen := make(map[string]bool)
		for _, title := range got {
			lower := strings.ToLower(title)
			if seen[lower] {
				t.Errorf("ExtractAltTitles output has duplicate %q", title)
			}
			seen[lower] = true
		}
	})
}

func TestSortByTitle_property_output_is_sorted(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		nEp := rapid.IntRange(0, 8).Draw(t, "nEp")
		nMov := rapid.IntRange(0, 8).Draw(t, "nMov")

		episodes := make([]scanning.ScanItem, nEp)
		for i := range nEp {
			episodes[i] = scanning.ScanItem{
				Series: &api.Series{
					Title: rapid.StringMatching(`[A-Za-z ]{1,20}`).Draw(t, fmt.Sprintf("ep_title_%d", i)),
				},
				Ep: &api.Episode{
					SeasonNumber:  rapid.IntRange(0, 10).Draw(t, fmt.Sprintf("ep_s_%d", i)),
					EpisodeNumber: rapid.IntRange(1, 50).Draw(t, fmt.Sprintf("ep_e_%d", i)),
				},
			}
		}
		movies := make([]scanning.ScanItem, nMov)
		for i := range nMov {
			movies[i] = scanning.ScanItem{
				Movie: &api.Movie{
					Title: rapid.StringMatching(`[A-Za-z ]{1,20}`).Draw(t, fmt.Sprintf("mov_title_%d", i)),
				},
			}
		}

		got := scanning.SortByTitle(episodes, movies)

		// Invariant: output length equals input length.
		if len(got) != nEp+nMov {
			t.Errorf("scanning.SortByTitle returned %d items, want %d", len(got), nEp+nMov)
		}

		// Invariant: output is sorted by (title, season, episode).
		for i := 1; i < len(got); i++ {
			prevTitle := strings.ToLower(scanning.ScanItemTitle(got[i-1]))
			currTitle := strings.ToLower(scanning.ScanItemTitle(got[i]))
			if prevTitle > currTitle {
				t.Errorf("scanning.SortByTitle not sorted at [%d]: %q > %q", i, prevTitle, currTitle)
			}
			if prevTitle == currTitle {
				ps, pe := scanning.ScanItemSeasonEp(got[i-1])
				cs, ce := scanning.ScanItemSeasonEp(got[i])
				if ps > cs || (ps == cs && pe > ce) {
					t.Errorf("scanning.SortByTitle not sorted at [%d]: S%02dE%02d > S%02dE%02d",
						i, ps, pe, cs, ce)
				}
			}
		}
	})
}

func TestHandleSaveConfig_oversized_body_returns_413(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("key: old\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	old := cfgFilePath
	cfgFilePath = configPath
	t.Cleanup(func() { cfgFilePath = old })

	s := newTestServer(&qhMockStore{}, &qhMockConfig{})

	// Body exactly 1 byte over maxConfigSize (1 MB + 1).
	body := bytes.NewReader(make([]byte, (1<<20)+1))
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	rec := httptest.NewRecorder()

	s.handleSaveConfig(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("handleSaveConfig(oversized body) status = %d, want %d",
			rec.Code, http.StatusRequestEntityTooLarge)
	}
}
