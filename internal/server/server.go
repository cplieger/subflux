// Package server provides the HTTP server and history poller for subtitle management.
//
// The package is split across several files by concern:
//   - server.go       — New() constructor and lifecycle (this file)
//   - server_types.go — Server struct, embedded dep groups, Option functions
//   - server_init.go  — initHandlers (handler family construction)
//   - middleware.go   — requireAuth / requireRole / requireConfigured
//   - routes.go       — routeGroup + registerRoutes + permission model
//   - poller.go       — Sonarr/Radarr history polling + import processing
//   - scheduler.go    — full-scan pipeline + DB maintenance + auth cleanup
//   - *_handlers.go   — per-concern HTTP handler families
package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/auth/v2"
	authoidc "github.com/cplieger/auth/v2/oidc"
	"github.com/cplieger/auth/v2/ratelimit"
	"github.com/cplieger/httpx/v3"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/queryhandlers"
	"github.com/cplieger/subflux/internal/server/showskip"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/sync/semaphore"
)

//go:embed static
var staticFS embed.FS

var staticSub = mustSub(staticFS, "static")

// indexHTML and loginHTML are the embedded HTML entrypoints served by
// handleUI and hashed into the CSP. Named constants because the literals
// recur across handleUI and the CSP builder (goconst).
const (
	indexHTML = "index.html"
	loginHTML = "login.html"
)

// mustSub extracts a subdirectory from an embedded FS, panicking on failure.
func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("embed: " + err.Error())
	}
	return sub
}

// Metrics is the observability interface consumed by the server package.
type Metrics interface {
	RecordSearch(provider api.ProviderID, dur time.Duration, err error)
	RecordDownload(provider api.ProviderID, err error)
	AdaptiveSkip()
	RecordEmbeddedDetectorError()
	RecordScan(items, found int, dur time.Duration)
	RecordImport(source api.PollKey)
	RecordHTTP(method, path string, status int, d time.Duration)
	RecordPanic()
	TotalSearches() int64
	Handler() http.HandlerFunc

	// Store observability (Requirement 17).
	RecordStoreFileSize(bytes int64)
	RecordStoreFreelistBytes(bytes int64)
	RecordReconcile(deleted int, reset int64, dur time.Duration)
	RecordBackupSuccess(dur time.Duration)

	// Mode observability: 1 when a valid configuration is active, 0 unconfigured.
	SetConfigured(ok bool)

	// Poll-cursor durability: count of cursors whose durable persist is failing.
	SetPollCursorsDirty(n int)
}

// sleepCtx pauses for d, returning early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	return httpx.SleepCtx(ctx, d)
}

// New creates a Server with the given options. db and reg are required.
func New(db api.Store, reg api.ProviderRegistry, opts ...Option) *Server {
	s := &Server{
		db: db,
		stores: storeFacade{
			query: db,
			sync:  db,
		},
		registry: reg,
		events:   events.New(events.DefaultMaxSSEClients),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		authDeps: authDeps{
			ceremonies: authhandlers.NewCeremonyStore(),
		},
		scanSubsystem: scanSubsystem{
			showSkipCache: showskip.New(1 * time.Hour),
		},
		previewDeps: previewDeps{
			ffmpegSem:    semaphore.NewWeighted(3),
			posterClient: newPosterClient(),
		},
		ctx: context.Background(),
	}
	for _, o := range opts {
		o(s)
	}
	if s.live.Load() == nil {
		s.live.Store(&liveState{})
	}
	// Apply the configured SSE client cap when a config option supplied one
	// (hot reload re-applies later changes).
	s.events.SetMaxClients(sseClientCap(s.state().cfg))
	s.initHandlers()
	return s
}

// authBypass reports whether auth.disable_auth is currently set. It reads the
// live config so toggling disable_auth via hot-reload takes effect without a
// restart (the Authenticator calls it per request).
func (s *Server) authBypass() bool {
	cfg, ok := s.state().cfg.(*config.Config)
	return ok && cfg.AuthDisabled()
}

// sessionActivityThrottle is the minimum interval between session-activity
// writes per session (the library SessionVerifier's per-hash throttle). The
// store write is an in-memory map update, so this is contention hygiene, not
// disk-churn protection.
const sessionActivityThrottle = 60 * time.Second

// SetAuth configures authentication dependencies on the server. WebAuthn and
// OIDC are deliberately absent from the signature: both are config-derived
// capabilities that activation builds into the live snapshot, resolved per
// request through the handler's resolver funcs. SetAuth returns an error only
// when the assembled authenticator configuration is rejected by the auth
// library's construction validation (a programming error, not runtime state).
func (s *Server) SetAuth(store authstore.AuthStore, rl ratelimit.Checker) error {
	s.authStore = store
	s.adminDB = store
	s.secDB = store
	s.oidcDB = store
	s.rateLimiter = rl

	// The library authenticator on its default verifier chain (session cookie,
	// then API key), configured with subflux's three policies: the per-request
	// LAN/HTTPS session cookie, subflux's 401 envelope, and live session
	// timeouts resolved from the hot-reloadable config per request (a
	// startup-time copy would silently ignore later edits). Activity writes
	// are throttled in the verifier, replacing the former debouncer+batcher
	// subsystem.
	authn, err := auth.NewAuthenticator(store,
		auth.WithBypass(s.authBypass),
		auth.WithCookie(authhandlers.SessionCookie),
		auth.WithUnauthorizedResponse(authhandlers.UnauthorizedResponse),
		auth.WithIdleTimeout(config.DefaultSessionIdleTimeout),
		auth.WithAbsTimeout(config.DefaultSessionAbsoluteTimeout),
		auth.WithActivityThrottle(sessionActivityThrottle),
		auth.WithTimeoutSource(func() (idle, absolute time.Duration) {
			if ls := s.state(); ls != nil && ls.cfg != nil {
				return ls.cfg.SessionIdleTimeout(), ls.cfg.SessionAbsoluteTimeout()
			}
			return config.DefaultSessionIdleTimeout, config.DefaultSessionAbsoluteTimeout
		}),
	)
	if err != nil {
		return fmt.Errorf("assemble authenticator: %w", err)
	}
	s.authenticator = authn

	s.authH = &authhandlers.Handler{
		Store:       store,
		AdminDB:     store,
		SecDB:       store,
		OidcDB:      store,
		RateLimiter: rl,
		// Snapshot resolution: WebAuthn and OIDC ride the live state, so a
		// hot config edit swaps what these return without re-wiring the
		// handler (a direct field would stay stale forever).
		WebAuthnResolver: func() *webauthn.WebAuthn { return s.state().webauthn },
		OIDCResolver:     s.getOIDC,
		Ceremonies:       s.ceremonies,
		Config: func() authhandlers.AuthConfig {
			if ls := s.state(); ls != nil && ls.cfg != nil {
				return ls.cfg
			}
			return nil
		},
		Configured: func() bool { return s.configured.Load() },
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
	return nil
}

// Start activates the boot config and starts the HTTP server. Cold boot and
// hot first-save go through the SAME activation operation (activate.go); the
// only cold-boot special case is the failure policy — an activation failure
// falls back to settings mode instead of refusing to serve, so a config that
// boots today keeps booting.
func (s *Server) Start(ctx context.Context, onReady func()) {
	s.ctx = ctx
	ls := s.state()

	if err := s.activate(ctx, ls.cfg, activateCold); err != nil {
		slog.Error("activation failed, falling back to settings mode", "error", err)
		s.alerts.RecordPersistent("startup",
			"Configuration activation failed: "+err.Error()+
				". Open Settings to fix the configuration.")
		s.configured.Store(false)
		// Mirror the configured predicate for the subflux_configured gauge
		// (finalize sets it true on the success path).
		s.metrics.SetConfigured(false)
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	addr := fmt.Sprintf(":%d", ls.cfg.ServerPort())

	if cfg, ok := s.state().cfg.(*config.Config); ok && cfg.AuthDisabled() {
		slog.Warn("AUTHENTICATION DISABLED via auth.disable_auth config; all requests treated as admin")
		s.alerts.RecordPersistent("security",
			"Authentication is DISABLED (auth.disable_auth): all requests are treated as admin. Remove this setting to restore login.")
	}

	s.serveAndWait(ctx, addr, mux, onReady)
}

// StartUnconfigured starts the HTTP server without a valid config.
func (s *Server) StartUnconfigured(ctx context.Context, onReady func()) {
	s.ctx = ctx

	slog.Warn("starting in unconfigured mode; " +
		"scans and searches are disabled until a valid configuration is saved")
	s.alerts.RecordPersistent("startup",
		"No valid configuration. Open Settings to configure Subflux.")
	s.metrics.SetConfigured(false)

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	addr := fmt.Sprintf(":%d", s.serverPort)

	s.serveAndWait(ctx, addr, mux, onReady)
}

// getOIDC resolves the OIDC provider from the CURRENT snapshot's lazy slot.
// A nil slot means OIDC is disabled in the live config; otherwise the slot
// performs (or reuses) its lazy discovery. Because the slot is per config,
// an issuer edit activates a fresh slot and can never serve a provider
// discovered under the previous config.
func (s *Server) getOIDC() *authoidc.Provider {
	slot := s.state().oidc
	if slot == nil {
		return nil
	}
	return slot.get(s.ctx)
}

// state returns the current live state snapshot.
func (s *Server) state() *liveState { return s.live.Load() }

// queryLiveState adapts the server's liveState to the queryhandlers.LiveState.
func (s *Server) queryLiveState() *queryhandlers.LiveState {
	ls := s.live.Load()
	return &queryhandlers.LiveState{
		Cfg:       ls.cfg,
		Engine:    ls.engine,
		Sonarr:    ls.sonarr,
		Radarr:    ls.radarr,
		Providers: ls.providers,
	}
}
