package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	pathpkg "path"
	"strings"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/scheduler"
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

	// The pre-drain hook flips readiness off before webhttp.Run drains in-flight
	// requests, so /api/health reports unready during the drain window,
	// preserving the pre-webhttp teardown ordering for load balancers.
	preDrain := func(context.Context) {
		s.ready.Set(false)
		slog.Info("shutting down HTTP server", "sse_clients", s.events.ClientCount())
		// Drain the SSE hub: cancel every live stream and refuse reconnects
		// with 503. Without this, long-lived event streams count as in-flight
		// requests and hold webhttp.Run's graceful drain open for the whole
		// grace budget on every shutdown.
		s.events.Shutdown()
	}

	if err := webhttp.Run(ctx, srv, ln, nil, webhttp.WithPreDrain(preDrain)); err != nil {
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
// The outermost layer is the allowed_hosts exact-match Host allowlist (see the
// tail of this function); http.NewCrossOriginProtection (Go 1.25+) comes next
// and is app-owned, not a webhttp feature: cross-origin state-changing requests
// are rejected before anything else runs (Sec-Fetch-Site, else Origin vs Host).
// GET/HEAD/OPTIONS and clients that send no Origin/Sec-Fetch-Site (arr
// webhooks, the subflux CLI) are permitted by default; their auth is the API
// key.
//
// The access logger comes next so it observes the final status and threads a
// request id into the context (the typed-code error helpers read it back via
// webhttp.WriteError). It is webhttp.Logging configured to reproduce subflux's
// access line exactly: WithClientIPFunc(authhandlers.ClientIP) adds the
// client_ip field resolved through the hot-reloadable trusted-proxy set (a
// field the fixed-shape line does not carry on its own), and
// WithRecordMetric(s.metrics.RecordHTTP) fires the per-request metric hook.
// /api/events is skipped via WithSkipPaths: its open-to-close SSE lifetime would
// emit one misleading high-latency access line and RecordHTTP sample, and the
// skip leaves the SSE writer un-recorded — webhttp.Recoverer then wraps it in
// the StatusRecorder whose Unwrap keeps http.ResponseController (per-connection
// write-deadline clearing) reaching the real writer. Recoverer sits INSIDE the
// access logger so a recovered panic logs as its 500 (not the recorder's
// default 200) and increments the panic metric. SecurityHeaders and the
// app-owned Cache-Control: no-store setter run innermost, so every response —
// including 4xx/5xx envelopes — carries them.
func (s *Server) buildHandler(mux http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	inner := webhttp.Chain(mux,
		cop.Handler,
		// Strip X-Forwarded-Proto from requests not arriving via a configured
		// trusted proxy, BEFORE anything consults it (the per-request session
		// cookie posture trusts the header; see authhandlers.SessionCookie).
		// Runs outermost-after-COP so every downstream consumer sees the
		// sanitized value.
		authhandlers.SanitizeForwardedProto,
		webhttp.Logging(
			webhttp.WithLogger(slog.Default()),
			webhttp.WithSkipPaths("/api/events"),
			webhttp.WithClientIPFunc(authhandlers.ClientIP),
			webhttp.WithRecordMetric(s.metrics.RecordHTTP),
		),
		webhttp.Recoverer(
			webhttp.WithRecoverLogger(slog.Default()),
			webhttp.WithPanicHook(func(_ any, _ []byte) { s.metrics.RecordPanic() }),
		),
		securityHeadersMW(),
		cacheControlMW,
	)

	// The allowed_hosts gate (webhttp.HostPolicy) wraps the chain OUTERMOST,
	// above even the cross-origin check, per webhttp's placement contract: a
	// DNS-rebinding request makes Origin and Host AGREE, so COP admits it —
	// only the exact-Host check breaks that chain (CWE-346). Because a
	// HostPolicy is immutable, hot reload swaps the whole wrapped chain
	// (applyHostAllowlist); the returned handler dereferences the current one
	// per request instead of capturing one policy forever.
	s.hostGateInner = inner
	s.applyHostAllowlist()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*s.hostGated.Load()).ServeHTTP(w, r)
	})
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

// cacheControlMW applies the scoped cache policy (see staticcache.go):
// per-user data (API responses, HTML entrypoints, SPA fallback) is never
// cached (`no-store`), while the embedded static assets carry a
// startup-computed ETag with `no-cache` so browsers revalidate with cheap
// 304s instead of re-downloading the whole frontend every page load. The
// ETag/gzip mechanics are webhttp.StaticHandler's; this middleware owns only
// the no-store default the asset handler overrides, applied innermost in the
// global chain (every response carries one of the two directives).
var cacheControlMW = cacheControlFor()

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

// handleScan triggers a full library scan: 202 + activity_id, 405 on
// non-POST. A duplicate start while a full scan is already running — manual
// or scheduled; both flavors run through scheduler.PrepareFullScan and carry
// the ScanKindFull scope — is idempotent (R1.5): it answers 202 with the
// RUNNING scan's activity id and starts no second scan. The s.scanning
// CompareAndSwap keeps the ownership semantics for actually starting; the
// activity log's active full-scan entry is where the running id lives, so
// the guard pair (flag, entry) needs no extra shared state. The
// scan-specific fork of the former generic asyncAction helper (this was its
// only caller): the accept sequence — activity start, stop registration,
// scan:start — is hoisted into scheduler.PrepareFullScan so the 202 body
// can carry the activity id.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	if !s.scanning.CompareAndSwap(false, true) {
		// Already running: idempotent duplicate answer carrying the live
		// scan's id. PrepareFullScan creates the entry immediately after
		// the owner's CAS and it stays active until just before the flag
		// clears, so this lookup misses only in two sub-microsecond
		// windows (owner accept, owner teardown).
		if id, ok := s.activity.ActiveScan(activity.ScanScope{Kind: activity.ScanKindFull}); ok {
			api.WriteJSONStatus(w, http.StatusAccepted,
				scanning.ScanAccepted{ActivityID: id, Status: "scan already running"})
			return
		}
		// No active entry: either the previous scan is tearing down (entry
		// terminal, flag not yet cleared) — retry the acquisition once —
		// or the owner is inside its accept instant, where conflict is the
		// honest answer (a re-click lands after the window).
		if !s.scanning.CompareAndSwap(false, true) {
			api.ConflictC(w, r, api.CodeScanInProgress, "scan already in progress")
			return
		}
	}
	actID, run := scheduler.PrepareFullScan(s.schedulerDeps(), activity.SourceManual)
	s.bgWg.Go(func() {
		defer s.scanning.Store(false)
		run(s.ctx)
	})
	api.WriteJSONStatus(w, http.StatusAccepted,
		scanning.ScanAccepted{ActivityID: actID, Status: "scan started"})
}

// handleUI serves the embedded web UI with SPA fallback. Real static files
// go through the shared webhttp.StaticHandler (ETag revalidation +
// construction-time gzip variants; see staticcache.go); any stale
// pre-migration .gz sibling in a dev tree is not addressable and falls
// through to the fallbacks like any unknown path.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(pathpkg.Clean(r.URL.Path), "/")
	if p == "" {
		p = indexHTML
	}
	servable := p != indexHTML && p != loginHTML && !strings.HasSuffix(p, ".gz")

	// Static assets are public: serve them directly if present.
	if servable {
		if info, err := fs.Stat(staticSub, p); err == nil && !info.IsDir() {
			staticAssets.ServeHTTP(w, r)
			return
		}
	}

	if _, _, err := s.authenticator.Authenticate(r); err != nil {
		if auth.IsBrowserRequest(r) {
			http.ServeFileFS(w, r, staticSub, loginHTML)
			return
		}
		api.UnauthorizedC(w, r, api.CodeUnauthorized, auth.ErrUnauthenticated.Error())
		return
	}

	// Authenticated: an explicit /login.html request is honored (the OIDC
	// link-on-login ceremony lands there); everything else that isn't a
	// real asset falls back to the SPA entrypoint.
	if p == loginHTML {
		http.ServeFileFS(w, r, staticSub, loginHTML)
		return
	}
	http.ServeFileFS(w, r, staticSub, indexHTML)
}
