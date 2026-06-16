package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	pathpkg "path"
	"strings"
	"sync/atomic"
	"time"

	authlib "github.com/cplieger/auth"
	"github.com/cplieger/subflux/internal/api"
)

// serveAndWait binds the HTTP listener, starts the server, and blocks
// until ctx is cancelled, then performs graceful shutdown.
func (s *Server) serveAndWait(ctx context.Context, addr string, mux *http.ServeMux, onReady func()) {
	// Outer-to-inner: http.NewCrossOriginProtection (Go 1.25+) is the
	// outermost layer so cross-origin state-changing requests are rejected
	// before any other middleware runs. It checks Sec-Fetch-Site (sent by
	// all modern browsers) and falls back to Origin vs Host comparison.
	// GET/HEAD/OPTIONS are always allowed (covers /api/events SSE,
	// /metrics scrape, healthcheck). Non-browser clients (arr webhook
	// callers, the subflux CLI's remote commands) send no Origin /
	// Sec-Fetch-Site, which the middleware permits by default; their
	// auth is enforced by API key.
	//
	// Inside that, api.RequestLogger threads a request id into
	// r.Context() so the typed-code error helpers (BadRequestC etc.)
	// auto-populate the `request_id` field of the JSON envelope.
	// securityHeaders runs innermost so headers are set on every
	// response, including 4xx error envelopes that consume the id.
	handler := http.NewCrossOriginProtection().Handler(api.RequestLogger(securityHeaders(mux), s.metrics.RecordHTTP))
	srv := newHTTPServer(handler)

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		slog.Error("failed to bind HTTP port", "addr", addr, "error", err)
		s.alerts.RecordPersistent("startup", "Failed to bind port: "+err.Error())
		return
	}

	go func() {
		slog.Info("HTTP server listening", "addr", addr)
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "error", err)
		}
	}()

	go s.runAuthCleanup(ctx)

	if onReady != nil {
		s.ready.Store(true)
		onReady()
	}

	<-ctx.Done()
	s.ready.Store(false)
	slog.Info("shutting down HTTP server",
		"sse_clients", s.events.ClientCount())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("HTTP server shutdown error", "error", err)
	}
	if s.sessBatcher != nil {
		s.sessBatcher.Stop()
	}

	// Wait for background goroutines (scans, downloads, scheduler, poller)
	// to finish with a bounded timeout to avoid hanging on stuck goroutines.
	bgDone := make(chan struct{})
	go func() {
		s.bgWg.Wait()
		close(bgDone)
	}()
	select {
	case <-bgDone:
		slog.Info("all background goroutines stopped")
	case <-time.After(10 * time.Second):
		slog.Warn("timed out waiting for background goroutines to stop")
	}
}

// newHTTPServer creates an http.Server with standard timeouts and limits.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}
}

// securityHeaders adds standard security headers to all responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; media-src 'self' blob:")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// --- Top-level handlers ---

// keyStatus is the JSON response key shared by the health envelope and the
// admin-bootstrap responses ({"status":"ok"} / {"status":"unready",...}).
const keyStatus = "status"

// handleHealth returns the liveness+readiness status. Emits the
// canonical JSON envelope shared across the homelab's custom Go apps
// (vibekit, vibecli, subflux, registry-stats, plex-exporter): 200 with
// {"status":"ok"} when ready, 503 with {"status":"unready",...} during
// startup or graceful shutdown. Same wire shape, different mechanisms
// per app — here `s.ready` flips after the listener binds and serves,
// and back to false on shutdown.
//
// The file-based health check (/tmp/.healthy) is the Docker probe;
// this endpoint is for HTTP-level liveness/readiness checks (also used
// by the functional test suite to wait for boot).
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			keyStatus: "unready",
			"reason":  "starting up or shutting down",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{keyStatus: "ok"})
}

// asyncActionResponse is the typed 202 Accepted response for async actions.
type asyncActionResponse struct {
	Status string `json:"status"`
}

// asyncAction starts fn in a background goroutine, returning 202 if started,
// 409 if already running, 405 on non-POST requests.
func (s *Server) asyncAction(ctx context.Context, w http.ResponseWriter, r *http.Request,
	flag *atomic.Bool, busyMsg, startedMsg string, fn func(context.Context),
) {
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	if !flag.CompareAndSwap(false, true) {
		api.ConflictC(w, r, api.CodeScanInProgress, busyMsg)
		return
	}
	s.bgWg.Go(func() {
		defer flag.Store(false)
		fn(ctx)
	})
	api.WriteJSONStatus(w, http.StatusAccepted, asyncActionResponse{Status: startedMsg})
}

// handleScan triggers a full scan via asyncAction, guarded by s.scanning.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	s.asyncAction(s.ctx, w, r, &s.scanning,
		"scan already in progress", "scan started",
		func(ctx context.Context) { s.runFullScan(ctx) })
}

// handleUI serves the embedded web UI with SPA fallback.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(pathpkg.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}

	// Static assets are public: serve them directly if present.
	if p != "index.html" && p != "login.html" {
		if info, err := fs.Stat(staticSub, p); err == nil && !info.IsDir() {
			http.ServeFileFS(w, r, staticSub, p)
			return
		}
	}

	if _, _, err := s.authenticator.Authenticate(r); err != nil {
		if authlib.IsBrowserRequest(r) {
			http.ServeFileFS(w, r, staticSub, "login.html")
			return
		}
		api.UnauthorizedC(w, r, api.CodeUnauthorized, authlib.ErrUnauthenticated.Error())
		return
	}

	// Authenticated: serve the requested static file or SPA fallback.
	if info, err := fs.Stat(staticSub, p); err == nil && !info.IsDir() {
		http.ServeFileFS(w, r, staticSub, p)
		return
	}
	http.ServeFileFS(w, r, staticSub, "index.html")
}
