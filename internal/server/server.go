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

	"github.com/cplieger/auth/v5"
	authoidc "github.com/cplieger/auth/v5/oidc"
	"github.com/cplieger/auth/v5/ratelimit"
	authwebauthn "github.com/cplieger/auth/v5/webauthn"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/server/coveragehandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/filehandlers"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/polling"
	"github.com/cplieger/subflux/internal/server/queryhandlers"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/scheduler"
	"github.com/cplieger/subflux/internal/server/showskip"
	"github.com/cplieger/subflux/internal/server/synchandlers"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/webhttp/v2"
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

// setupPath is the setup wizard's own address (cleaned, so no leading slash).
// The wizard is a page-state of login.html rather than a document of its own,
// so without an address a reload mid-setup resolves to whichever bundle the
// session state implies — which, once the admin exists, is the app. handleUI
// serves login.html here for either auth state and login.ts re-enters the
// wizard from it. Mirrored client-side as SETUP_PATH in static-src/constants.ts.
const setupPath = "setup"

// mustSub extracts a subdirectory from an embedded FS, panicking on failure.
func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("embed: " + err.Error())
	}
	return sub
}

// TransportMetrics records the HTTP surface's own behaviour and exposes the
// scrape endpoint.
type TransportMetrics interface {
	RecordHTTP(rm webhttp.RequestMetric)
	RecordPanic()
	Handler() http.HandlerFunc
}

// StoreMetrics records bbolt store observability (Requirement 17): how large
// the file and its freelist have grown, and that a hot backup completed.
type StoreMetrics interface {
	RecordStoreFileSize(bytes int64)
	RecordStoreFreelistBytes(bytes int64)
	RecordBackupSuccess(dur time.Duration)
}

// ModeMetrics reports which mode the process is serving in: 1 when a valid
// configuration is active, 0 unconfigured.
type ModeMetrics interface {
	SetConfigured(ok bool)
}

// DurabilityMetrics reports the poll-cursor durability gauge: the count of
// cursors whose durable persist is failing.
type DurabilityMetrics interface {
	SetPollCursorsDirty(n int)
}

// Metrics is the observability surface the server package wires. It is a
// composition of roles rather than a flat method list: the first group is the
// seams the subsystems declare for themselves, the rest are the server's own.
// A consumer takes the one role it records against; this union exists only
// because the composition root wires a single concrete recorder to all of
// them, and it is the only place their sum is named.
type Metrics interface {
	search.Metrics
	scanning.ScanMetrics
	polling.PollerMetrics
	queryhandlers.MetricsReader
	scheduler.ReconcileMetrics

	TransportMetrics
	StoreMetrics
	ModeMetrics
	DurabilityMetrics
}

// Store is the persistence surface New requires: the union of the narrow
// interfaces its consumers declare. It is the same shape as AuthStore below
// and it exists for the same reason — this is the ONE site that fans a single
// concrete store out to every handler family, the scan and poll subsystems,
// the scheduler and the search engine, so the union belongs here rather than
// in a types package that implements none of it.
//
// It is composed by EMBEDDING, never by re-listing. That is the whole
// difference from the 36-method whole-store interface this replaces: the width
// is derived
// from what the consumers ask for, so adding a method to filehandlers.FileStore
// widens this automatically and removing one narrows it. A hand-written list
// can only drift, and drifted for 36 methods of which the widest consumer used
// seven.
//
// Nothing outside internal/server can take this: composition happens here, and
// every package below is handed the narrow interface it declared.
type Store interface {
	// Handler families.
	queryhandlers.QueryStore
	synchandlers.SyncStore
	coveragehandlers.CoverageStore
	filehandlers.FileStore
	manualops.DownloadStore
	manualStore
	resolve.FileStore

	// Subsystems.
	polling.PollerStore
	scanning.ScanStore
	scanning.BackoffPrefixReader
	scheduler.Store
	search.Store

	// Coverage: the stats endpoint's two aggregate reads, and the one row read
	// the bound missing-count closure performs.
	queryhandlers.StatsStore
	coverage.FileReader

	// The store-backed capabilities the server itself operates: the hot
	// snapshot for the backup loop and the file gauges for /metrics. Both used
	// to be reached by type-asserting the wide interface and silently doing
	// nothing when it failed; declaring them here makes the requirement a
	// compile error instead of a Debug log.
	BackupInto(ctx context.Context, dest string) error
	StoreFileStats() (fileBytes, freelistBytes int64)

	// Read and write the history-poll watermark. Held by the server because
	// polling reads it through the write-through PollCache the server builds,
	// not directly.
	PollTimestamp(ctx context.Context, key subflux.PollKey) (time.Time, error)
	SetPollTimestamp(ctx context.Context, key subflux.PollKey, t time.Time) error

	// Drop search_attempts rows for languages and providers a config edit
	// removed. Called by activation, which is the server's own concern.
	CleanupDrift(ctx context.Context, drift subflux.ConfigDrift) error
}

// New creates a Server with the given options. db and reg are required.
func New(db Store, reg confighandlers.SchemaRegistry, opts ...Option) *Server {
	s := &Server{
		db: db,
		stores: storeFacade{
			query: db,
			sync:  db,
		},
		registry:      reg,
		events:        events.New(events.DefaultMaxSSEClients),
		activity:      activity.New(50),
		alerts:        activity.NewAlertLog(100),
		ceremonies:    authhandlers.NewCeremonyStore(),
		showSkipCache: showskip.New(1 * time.Hour),
		ffmpegSem:     semaphore.NewWeighted(3),
		posterClient:  newPosterClient(),
	}
	for _, o := range opts {
		o(s)
	}
	if s.live.Load() == nil {
		s.live.Store(&liveState{})
	}
	// The arr-read wrapper's shared half: built here (not per activation) so
	// the wave-admission ceiling spans reloads and both arr sides. Waves run
	// under the server lifetime context and register with bgWg.
	s.arrReads = arrsvc.NewReadGate(func() context.Context { return s.lifetime }, &s.bgWg)
	// Metrics is REQUIRED, and it is checked here rather than at Start because
	// initHandlers below binds it into four child Deps by value. Expressed as an
	// Option it read as voluntary, and it never was: twenty-two sites dereference
	// it unguarded, including the middleware chain and the /metrics mount. The
	// single nil check that used to sit in initHandlers is what made a missing
	// recorder look survivable — it postponed the panic to the first request
	// instead of preventing it. Same shape as search.New's five required options.
	if s.metrics == nil {
		panic("server.New: WithMetrics is required")
	}
	// Apply the configured SSE client cap when a config option supplied one
	// (hot reload re-applies later changes).
	s.events.SetMaxClients(sseClientCap(s.state().cfg))
	s.initHandlers()
	return s
}

// authBypass reports whether auth.disable_auth is currently set. It reads the
// live config so toggling disable_auth via hot-reload takes effect without a
// restart (the Authenticator calls it per request). Unconfigured mode has no
// config and therefore no bypass.
func (s *Server) authBypass() bool {
	cfg := s.state().cfg
	return cfg != nil && cfg.AuthDisabled()
}

// sessionActivityThrottle is the minimum interval between session-activity
// writes per session (the library SessionVerifier's per-hash throttle). The
// store write is an in-memory map update, so this is contention hygiene, not
// disk-churn protection.
const sessionActivityThrottle = 60 * time.Second

// AuthStore is the persistence surface SetAuth requires: the union of the
// narrow interfaces its consumers declare — the library's own Authenticator
// contract plus the four handler-side roles. The auth library publishes no
// composite of its own (v4 merged the store SPI into its root package and
// exposes it as role interfaces), so the union is declared here, at the one
// site that fans a single concrete store out to all five consumers.
type AuthStore interface {
	auth.AuthenticatorStore
	authhandlers.AccountStore
	authhandlers.AuthAdminStore
	authhandlers.SecurityStore
	authhandlers.OIDCStore
}

// SetAuth configures authentication dependencies on the server. WebAuthn and
// OIDC are deliberately absent from the signature: both are config-derived
// capabilities that activation builds into the live snapshot, resolved per
// request through the handler's resolver funcs. SetAuth returns an error only
// when the assembled authenticator configuration is rejected by the auth
// library's construction validation (a programming error, not runtime state).
func (s *Server) SetAuth(store AuthStore, rl ratelimit.Checker) error {
	s.authStore = store

	// The library authenticator on its default verifier chain (session cookie,
	// then API key), configured with subflux's three policies: the per-request
	// LAN/HTTPS session cookie, subflux's 401 envelope, and live session
	// timeouts resolved from the hot-reloadable config per request (a
	// startup-time copy would silently ignore later edits). Activity writes
	// are throttled in the verifier, replacing the former debouncer+batcher
	// subsystem.
	authn, err := auth.New(store,
		auth.WithBypass(s.authBypass),
		auth.WithCookie(authhandlers.SessionCookie),
		auth.WithUnauthorizedResponse(authhandlers.UnauthorizedResponse),
		auth.WithIdleTimeout(config.DefaultSessionIdleTimeout),
		auth.WithAbsTimeout(config.DefaultSessionAbsoluteTimeout),
		auth.WithActivityThrottle(sessionActivityThrottle),
		auth.WithTimeoutSource(func() auth.SessionTimeouts {
			if ls := s.state(); ls != nil && ls.cfg != nil {
				return auth.SessionTimeouts{
					Idle:     ls.cfg.SessionIdleTimeout(),
					Absolute: ls.cfg.SessionAbsoluteTimeout(),
				}
			}
			return auth.SessionTimeouts{
				Idle:     config.DefaultSessionIdleTimeout,
				Absolute: config.DefaultSessionAbsoluteTimeout,
			}
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
		WebAuthnResolver: func() *authwebauthn.RelyingParty { return s.state().webauthn },
		OIDCResolver:     s.oidcProvider,
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
	s.requireServiceable()
	s.lifetime = ctx
	s.bgWg.Go(func() { s.awaitWorkerLaunch(ctx) })
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

	addr := fmt.Sprintf(":%d", config.ServerPort)

	if cfg := s.state().cfg; cfg != nil && cfg.AuthDisabled() {
		slog.Warn("AUTHENTICATION DISABLED via auth.disable_auth config; all requests treated as admin")
		s.alerts.RecordPersistent("security",
			"Authentication is DISABLED (auth.disable_auth): all requests are treated as admin. Remove this setting to restore login.")
	}

	s.serveAndWait(ctx, addr, mux, onReady)
}

// StartUnconfigured starts the HTTP server without a valid config.
func (s *Server) StartUnconfigured(ctx context.Context, onReady func()) {
	s.requireServiceable()
	s.lifetime = ctx
	s.bgWg.Go(func() { s.awaitWorkerLaunch(ctx) })

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

// oidcProvider resolves the OIDC provider from the CURRENT snapshot's lazy slot.
// A nil slot means OIDC is disabled in the live config; otherwise the slot
// performs (or reuses) its lazy discovery. Because the slot is per config,
// an issuer edit activates a fresh slot and can never serve a provider
// discovered under the previous config.
func (s *Server) oidcProvider() *authoidc.Provider {
	slot := s.state().oidc
	if slot == nil {
		return nil
	}
	return slot.get(s.lifetime)
}

// GoBackground runs fn on the server's background goroutine set — the one set
// serveAndWait drains, on a bounded budget, after HTTP shutdown completes. The
// composition root uses it for the process-lifetime goroutines it owns rather
// than the server (the admin bootstrap socket), so a goroutine started outside
// this package still has an observable completion.
//
// Registration must happen before shutdown begins; the set is only waited on
// after the request context is cancelled, so an fn that serves until ctx ends
// is joined rather than abandoned.
func (s *Server) GoBackground(fn func()) { s.bgWg.Go(fn) }

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
