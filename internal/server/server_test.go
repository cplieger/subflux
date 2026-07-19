package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/scheduler"
	"github.com/cplieger/webhttp"
)

var errMock = errors.New("mock error")

// securityChain wraps h with the same response-security middleware serveAndWait
// installs (webhttp.SecurityHeaders with subflux's CSP / Permissions-Policy /
// COOP, then Cache-Control: no-store), so the header tests exercise the real
// composition rather than a stand-in.
func securityChain(h http.Handler) http.Handler {
	return webhttp.Chain(h, securityHeadersMW(), cacheControlMW)
}

func TestHandleHealth_returns_ok(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	s.ready.Set(true)

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
	handler := securityChain(inner)

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
		{"Permissions-Policy", "camera=(), microphone=(), geolocation=()"},
		{"Cross-Origin-Opener-Policy", "same-origin"},
	}

	for _, tt := range tests {
		got := rec.Header().Get(tt.header)
		if got != tt.want {
			t.Errorf("securityChain() %s = %q, want %q", tt.header, got, tt.want)
		}
	}

	// Content-Security-Policy is computed from the embedded HTML at
	// startup (buildCSPPolicy), so assert its directives rather than a
	// brittle exact string. The inline anti-FOUC theme-init script must be
	// allowed via a script-src sha256 hash, or the browser blocks it and
	// the page flashes the wrong theme.
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self' 'sha256-",
		"style-src 'self' 'unsafe-inline'",
		"media-src 'self' blob:",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("Content-Security-Policy = %q, missing %q", csp, want)
		}
	}
}

func TestSecurityHeaders_passes_through_to_next_handler(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Custom", "test")
		w.WriteHeader(http.StatusTeapot)
	})
	handler := securityChain(inner)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("securityChain() status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	if got := rec.Header().Get("X-Custom"); got != "test" {
		t.Errorf("inner handler header X-Custom = %q, want %q", got, "test")
	}
}

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

func TestHandleScan_post_returns_accepted_with_activity_id(t *testing.T) {
	t.Parallel()
	// handleScan launches the scan in a goroutine which needs sonarr/radarr
	// clients; we only test the synchronous accept sequence: 202 with the
	// activity id present AT accept time, entry created with the full-scan
	// scope, stop registered before the response.
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
	var resp scanning.ScanAccepted
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "scan started" {
		t.Errorf("handleScan(POST) status = %q, want %q", resp.Status, "scan started")
	}
	if resp.ActivityID == "" {
		t.Fatal("handleScan(POST) returned no activity_id at accept time")
	}
	entry, ok := s.activity.Get(resp.ActivityID)
	if !ok {
		t.Fatalf("activity entry %q not found after accept", resp.ActivityID)
	}
	if entry.Kind != activity.ScanKindFull {
		t.Errorf("entry.Kind = %q, want %q", entry.Kind, activity.ScanKindFull)
	}
	if entry.RequiredRole != auth.RoleAdmin {
		t.Errorf("entry.RequiredRole = %q, want admin (full scans are admin-cancellable only)", entry.RequiredRole)
	}
	if entry.Source != activity.SourceManual {
		t.Errorf("entry.Source = %q, want manual (POST /api/scan is a manual trigger)", entry.Source)
	}
}

func TestHandleScan_duplicate_start_returns_running_scan_id(t *testing.T) {
	t.Parallel()
	// R1.5: a start while a full scan is already running answers 202 with
	// the RUNNING scan's activity id and starts no second scan. Scheduled
	// and manual full scans both run through scheduler.PrepareFullScan, so
	// a scheduled-scan entry answers a manual duplicate too — pinned here
	// with a SourceScheduled entry.
	s := &Server{
		db:       &qhMockStore{},
		metrics:  metrics.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		ctx:      context.Background(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	// Simulate a running full scan: the guard flag plus the active
	// full-scan entry PrepareFullScan leaves while a scan runs.
	runningID, existing := s.activity.StartScan(scheduler.FullScanAction, scheduler.FullScanDetail,
		activity.SourceScheduled, activity.ScanScope{Kind: activity.ScanKindFull}, auth.RoleAdmin)
	if existing {
		t.Fatal("test setup: full-scan entry unexpectedly existed")
	}
	s.scanning.Store(true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScan(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("handleScan(duplicate start) status = %d, want %d (idempotent 202 per R1.5)",
			rec.Code, http.StatusAccepted)
	}
	var resp scanning.ScanAccepted
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ActivityID != runningID {
		t.Errorf("duplicate start activity_id = %q, want the running scan's id %q",
			resp.ActivityID, runningID)
	}
	if resp.Status != "scan already running" {
		t.Errorf("duplicate start status = %q, want %q", resp.Status, "scan already running")
	}
	if n := len(s.activity.Entries()); n != 1 {
		t.Errorf("activity entries = %d after duplicate start, want 1 (no second scan)", n)
	}
	if !s.scanning.Load() {
		t.Error("duplicate start cleared the running scan's guard flag")
	}
}

func TestHandleScan_conflict_only_in_guard_window_without_entry(t *testing.T) {
	t.Parallel()
	// Degenerate fallback: the guard flag is held but no active full-scan
	// entry exists (the owner's sub-microsecond accept instant). Conflict
	// is the honest answer, and the handler must not steal the flag.
	s := &Server{
		db:       &qhMockStore{},
		metrics:  metrics.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		ctx:      context.Background(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})
	s.scanning.Store(true)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScan(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("handleScan(flag held, no entry) status = %d, want %d",
			rec.Code, http.StatusConflict)
	}
	if !s.scanning.Load() {
		t.Error("handleScan stole the guard flag in the fallback window")
	}
}

// --- handleScan method guard (fork of the former generic asyncAction) ---

func TestHandleScan_non_post_returns_405(t *testing.T) {
	t.Parallel()
	s := &Server{
		db:       &qhMockStore{},
		metrics:  metrics.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		ctx:      context.Background(),
	}
	s.live.Store(&liveState{cfg: &qhMockConfig{}})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleScan(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleScan(GET) status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if s.scanning.Load() {
		t.Error("handleScan(GET) must not flip the scanning flag")
	}
}

// TestBuildHandler_preserves_streaming_writer smoke-tests that the global
// middleware chain (webhttp.Logging skip + Recoverer's StatusRecorder +
// SecurityHeaders + Cache-Control) still hands a streaming handler a writer that
// implements http.Flusher and whose http.ResponseController can clear the
// per-connection write deadline (reaching the real writer via
// StatusRecorder.Unwrap). This guards the SSE (/api/events, skipped by Logging)
// and preview-video (/api/preview/video, logged) routes against the webhttp swap.
func TestBuildHandler_preserves_streaming_writer(t *testing.T) {
	t.Parallel()

	type probe struct {
		deadlineErr error
		flusher     bool
	}

	for _, path := range []string{"/api/events", "/api/preview/video"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			s := &Server{metrics: metrics.New()}
			ch := make(chan probe, 1)

			mux := http.NewServeMux()
			mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
				_, fl := w.(http.Flusher)
				de := http.NewResponseController(w).SetWriteDeadline(time.Time{})
				ch <- probe{flusher: fl, deadlineErr: de}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(": ok\n\n"))
				http.NewResponseController(w).Flush()
			})

			ts := httptest.NewServer(s.buildHandler(mux))
			defer ts.Close()

			resp, err := ts.Client().Get(ts.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
			p := <-ch
			if !p.flusher {
				t.Errorf("%s: streaming handler writer does not implement http.Flusher", path)
			}
			if p.deadlineErr != nil {
				t.Errorf("%s: SetWriteDeadline via ResponseController failed: %v", path, p.deadlineErr)
			}
		})
	}
}
