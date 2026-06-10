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
	"sync"
	"sync/atomic"
	"time"

	authoidc "github.com/cplieger/auth/oidc"
	"github.com/cplieger/auth/ratelimit"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/httputil"
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
	RecordScan(items, found int, dur time.Duration)
	RecordImport(source api.PollKey)
	TotalSearches() int64
	Handler() http.HandlerFunc
}

// sleepCtx pauses for d, returning early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	return httputil.SleepCtx(ctx, d)
}

// oidcRetryBackoff is the minimum interval between OIDC discovery attempts.
const oidcRetryBackoff = 30 * time.Second

// oidcLazyInit holds the mutex + last-attempt timestamp for OIDC retry.
type oidcLazyInit struct {
	mu          sync.Mutex
	lastAttempt atomic.Int64
}

// New creates a Server with the given options. db and reg are required.
func New(db api.Store, reg api.ProviderRegistry, opts ...Option) *Server {
	s := &Server{
		db: db,
		stores: storeFacade{
			file:  db,
			query: db,
			cov:   db,
			sync:  db,
			dl:    db,
		},
		registry: reg,
		events:   events.New(),
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
		authDeps: authDeps{
			ceremonies:   authhandlers.NewCeremonyStore(),
			sessDebounce: newSessionActivityDebouncer(),
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

// SetAuth configures authentication dependencies on the server.
func (s *Server) SetAuth(store authstore.AuthStore, rl ratelimit.Checker, wa *webauthn.WebAuthn, oidc *authoidc.Provider) {
	s.authStore = store
	s.adminDB = store
	s.secDB = store
	s.oidcDB = store
	s.rateLimiter = rl
	s.webauthn = wa
	s.oidcProvider = oidc

	idle := config.DefaultSessionIdleTimeout
	abs := config.DefaultSessionAbsoluteTimeout
	if ls := s.state(); ls != nil && ls.cfg != nil {
		idle = ls.cfg.SessionIdleTimeout()
		abs = ls.cfg.SessionAbsoluteTimeout()
	}
	s.authenticator = &authhandlers.Authenticator{
		Store:       store,
		IdleTimeout: idle,
		AbsTimeout:  abs,
		Bypass:      s.authBypass,
	}

	s.authH = &authhandlers.Handler{
		Store:        store,
		AdminDB:      store,
		SecDB:        store,
		OidcDB:       store,
		RateLimiter:  rl,
		WebAuthn:     wa,
		OIDCResolver: s.getOIDC,
		Ceremonies:   s.ceremonies,
		Config: func() authhandlers.AuthConfig {
			if ls := s.state(); ls != nil && ls.cfg != nil {
				return ls.cfg
			}
			return nil
		},
		Configured: func() bool { return s.configured.Load() },
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// SetOIDCLazy stores the OIDC config for lazy provider initialization.
func (s *Server) SetOIDCLazy(cfg api.OIDCConfig) {
	s.oidcCfg = &cfg
}

// toAuthOIDCConfig adapts subflux's api.OIDCConfig to the auth/oidc package's
// Config type (structurally identical, distinct named types).
func toAuthOIDCConfig(c api.OIDCConfig) authoidc.Config {
	return authoidc.Config{
		IssuerURL:    c.IssuerURL,
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURI:  c.RedirectURI,
		AutoRedirect: c.AutoRedirect,
	}
}

// Start initializes providers and starts the HTTP server and scheduler.
func (s *Server) Start(ctx context.Context, onReady func()) {
	s.ctx = ctx
	if batchDB, ok := s.db.(sessionBatchUpdater); ok {
		s.sessBatcher = newSessionActivityBatcher(ctx, batchDB)
	}
	ls := s.state()
	engine, sc, providers, err := s.wire(ctx, ls.cfg, s.db, s.metrics)
	if err != nil {
		slog.Error("failed to initialize providers, falling back to settings mode",
			"error", err)
		s.alerts.RecordPersistent("startup",
			"Provider initialization failed: "+err.Error()+
				". Open Settings to fix the configuration.")
		s.configured.Store(false)
	} else {
		s.live.Store(&liveState{
			cfg:       ls.cfg,
			providers: providers,
			engine:    engine,
			scorer:    sc,
			sonarr:    ls.sonarr,
			radarr:    ls.radarr,
		})
		s.configured.Store(true)

		slog.Info("subflux initialized",
			"providers", len(providers),
			"languages", ls.cfg.LanguageCodes(),
			"sonarr", ls.cfg.SonarrConfig().URL != "",
			"radarr", ls.cfg.RadarrConfig().URL != "")
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	addr := fmt.Sprintf(":%d", ls.cfg.ServerPort())

	if s.configured.Load() {
		s.bgWg.Add(3)
		go func() { defer s.bgWg.Done(); s.runScheduler(ctx) }()
		go func() { defer s.bgWg.Done(); s.runPoller(ctx) }()
		go func() { defer s.bgWg.Done(); s.runBackup(ctx) }()
	}

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
	if batchDB, ok := s.db.(sessionBatchUpdater); ok {
		s.sessBatcher = newSessionActivityBatcher(ctx, batchDB)
	}

	slog.Warn("starting in unconfigured mode; " +
		"scans and searches are disabled until a valid configuration is saved")
	s.alerts.RecordPersistent("startup",
		"No valid configuration. Open Settings to configure Subflux.")

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	addr := fmt.Sprintf(":%d", s.serverPort)

	s.serveAndWait(ctx, addr, mux, onReady)
}

// getOIDC returns the OIDC provider, lazily initializing it on first call.
func (s *Server) getOIDC() *authoidc.Provider {
	if s.oidcProvider != nil {
		return s.oidcProvider
	}
	if s.oidcCfg == nil {
		return nil
	}

	last := s.oidcInit.lastAttempt.Load()
	if last != 0 && time.Since(time.Unix(0, last)) < oidcRetryBackoff {
		return nil
	}

	s.oidcInit.mu.Lock()
	defer s.oidcInit.mu.Unlock()

	if s.oidcProvider != nil {
		return s.oidcProvider
	}

	s.oidcInit.lastAttempt.Store(time.Now().UnixNano())
	p, err := authoidc.NewProvider(s.ctx, toAuthOIDCConfig(*s.oidcCfg))
	if err != nil {
		slog.Error("lazy OIDC provider initialization failed (will retry after backoff)",
			"error", err, "retry_after", oidcRetryBackoff)
		return nil
	}
	s.oidcProvider = p
	return p
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
