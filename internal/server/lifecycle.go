package server

import (
	"context"
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
	"github.com/cplieger/webhttp"
)

// serveAndWait builds the global middleware chain, binds the HTTP listener, and
// serves until ctx is cancelled, then performs graceful shutdown via webhttp.Run.
func (s *Server) serveAndWait(ctx context.Context, addr string, mux *http.ServeMux, onReady func()) {
	// Seed the client-IP resolver from the live config before serving so the
	// access log, audit log, rate limiter, and session records resolve the real
	// client from the start. Hot reload re-applies it (see applyTrustedProxies).
	s.applyTrustedProxies()

	srv := newHTTPServer(s.buildHandler(mux))

	// Bind the listener up front so a port-in-use error surfaces synchronously
	// before webhttp.Run starts serving.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		slog.Error("failed to bind HTTP port", "addr", addr, "error", err)
		s.alerts.RecordPersistent("startup", "Failed to bind port: "+err.Error())
		return
	}

	go s.runAuthCleanup(ctx)

	if onReady != nil {
		s.ready.Set(true)
		onReady()
	}
	slog.Info("HTTP server listening", "addr", addr)

	// Flip readiness off the instant shutdown is signalled — before webhttp.Run
	// drains in-flight requests — so /api/health reports unready during the drain
	// window, preserving the pre-webhttp teardown ordering for load balancers.
	context.AfterFunc(ctx, func() {
		s.ready.Set(false)
		slog.Info("shutting down HTTP server", "sse_clients", s.events.ClientCount())
	})

	// onShutdown runs after Run drains in-flight requests (within the same grace
	// budget), matching the old ordering where the session batcher stopped after
	// srv.Shutdown returned.
	onShutdown := func(context.Context) {
		if s.sessBatcher != nil {
			s.sessBatcher.Stop()
		}
	}

	if err := webhttp.Run(ctx, srv, ln, onShutdown); err != nil {
		slog.Error("HTTP server error", "error", err)
	}

	// subflux's own background goroutines (scans, downloads, scheduler, poller)
	// drain on a separate bounded budget OUTSIDE Run, so a stuck goroutine can't
	// hang shutdown past 10s.
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

// buildHandler assembles the global middleware chain around mux via webhttp.Chain
// (first entry outermost). Extracted from serveAndWait so the chain has one
// definition that the streaming smoke tests can exercise end-to-end.
//
// http.NewCrossOriginProtection (Go 1.25+) stays OUTERMOST and is app-owned, not
// a webhttp feature: cross-origin state-changing requests are rejected before
// anything else runs (Sec-Fetch-Site, else Origin vs Host). GET/HEAD/OPTIONS and
// clients that send no Origin/Sec-Fetch-Site (arr webhooks, the subflux CLI) are
// permitted by default; their auth is the API key.
//
// The access logger comes next so it observes the final status and threads a
// request id into the context (the typed-code error helpers read it back via
// webhttp.WriteError). subflux owns this middleware (accessLog) rather than
// using webhttp.Logging: it builds on webhttp's exported request-id +
// StatusRecorder primitives but adds a client_ip field — resolved through the
// trusted-proxy set — that webhttp.Logging's fixed field shape cannot carry.
// /api/events is skipped: its open-to-close SSE lifetime would emit one
// misleading high-latency access line and RecordHTTP sample, and the skip
// leaves the SSE writer un-recorded — webhttp.Recoverer then wraps it in the
// StatusRecorder whose Unwrap keeps http.ResponseController (per-connection
// write-deadline clearing) reaching the real writer. Recoverer sits INSIDE the
// access logger so a recovered panic logs as its 500 (not the recorder's
// default 200) and increments the panic metric. SecurityHeaders and the
// app-owned Cache-Control: no-store setter run innermost, so every response —
// including 4xx/5xx envelopes — carries them.
func (s *Server) buildHandler(mux http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	return webhttp.Chain(mux,
		cop.Handler,
		s.accessLogMW("/api/events"),
		webhttp.Recoverer(
			webhttp.WithRecoverLogger(slog.Default()),
			webhttp.WithPanicHook(func(_ any, _ []byte) { s.metrics.RecordPanic() }),
		),
		securityHeadersMW(),
		cacheControlMW,
	)
}

// newHTTPServer creates the app's http.Server. webhttp.NewServer supplies the
// streaming-safe defaults (ReadHeaderTimeout 10s, IdleTimeout 120s,
// MaxHeaderBytes 1 MiB); subflux re-supplies explicit read/write bounds because
// its endpoints are bounded request/response handlers. The 60s WriteTimeout is
// deliberate: subflux is a streaming app that uses the INVERSE of webhttp's
// "omit WriteTimeout" guidance — a global WriteTimeout plus per-connection write
// deadline clearing (http.ResponseController) on the SSE / preview-video / scan
// routes, which reach the real writer through webhttp.StatusRecorder.Unwrap.
func newHTTPServer(handler http.Handler) *http.Server {
	return webhttp.NewServer(handler,
		webhttp.WithReadTimeout(10*time.Second),
		webhttp.WithWriteTimeout(60*time.Second),
		webhttp.WithReadHeaderTimeout(10*time.Second),
	)
}

// securityHeadersMW is subflux's response-security middleware. webhttp.SecurityHeaders
// supplies the baseline (X-Content-Type-Options: nosniff, X-Frame-Options: DENY,
// Referrer-Policy: strict-origin-when-cross-origin — all matching subflux's prior
// values); subflux layers on its computed CSP, the Permissions-Policy feature
// gate, and Cross-Origin-Opener-Policy: same-origin. Cache-Control is NOT a
// webhttp feature and is applied separately by cacheControlMW.
func securityHeadersMW() webhttp.Middleware {
	return webhttp.SecurityHeaders(
		webhttp.WithCSP(cspPolicy),
		webhttp.WithPermissionsPolicy("camera=(), microphone=(), geolocation=()"),
		webhttp.WithCOOP("same-origin"),
	)
}

// cacheControlMW sets Cache-Control: no-store on every response. subflux serves
// per-user, per-request data (coverage tables, config, auth/session state) that
// must never be cached by a browser or intermediary. webhttp ships no
// cache-control feature, so this stays a one-line app-owned middleware, applied
// innermost in the global chain.
func cacheControlMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// --- Top-level handlers ---

// keyStatus is the JSON "status" response key used by the admin-bootstrap
// responses ({"status":"ok"}). The health envelope now comes from
// webhttp.ReadinessHandler, which owns its own struct and key ordering.
const keyStatus = "status"

// handleHealth reports HTTP serving state via webhttp.ReadinessHandler: 200 with
// {"status":"ok"} when s.ready is set, 503 with {"status":"unready","reason":...}
// during startup or graceful shutdown. s.ready (a webhttp.Ready) flips true once
// the listener binds and serves, and back to false the instant shutdown is
// signalled.
//
// This is the serving-state gate for a load balancer (and the functional test
// suite's boot-wait probe). It is deliberately distinct from the health
// library's file-marker liveness probe (subflux health -> health.RunProbe) that
// answers "is the process alive?" for the Docker HEALTHCHECK; the two are never
// the same endpoint.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	webhttp.ReadinessHandler(&s.ready)(w, r)
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
		p = indexHTML
	}

	// Static assets are public: serve them directly if present.
	if p != indexHTML && p != loginHTML {
		if info, err := fs.Stat(staticSub, p); err == nil && !info.IsDir() {
			http.ServeFileFS(w, r, staticSub, p)
			return
		}
	}

	if _, _, err := s.authenticator.Authenticate(r); err != nil {
		if authlib.IsBrowserRequest(r) {
			http.ServeFileFS(w, r, staticSub, loginHTML)
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
	http.ServeFileFS(w, r, staticSub, indexHTML)
}
