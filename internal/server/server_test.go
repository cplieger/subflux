package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/server/activity"
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
