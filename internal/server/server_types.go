package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/cplieger/auth/v2/ratelimit"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/server/coveragehandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/filehandlers"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/mediahandlers"
	"github.com/cplieger/subflux/internal/server/polling"
	"github.com/cplieger/subflux/internal/server/previewhandlers"
	"github.com/cplieger/subflux/internal/server/queryhandlers"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/showskip"
	"github.com/cplieger/subflux/internal/server/synchandlers"
	"github.com/cplieger/subflux/internal/wiring"
	"github.com/cplieger/webhttp"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/sync/semaphore"
)

// --- Types ---

// liveState holds all fields that are atomically swapped during activation
// (cold boot and hot reload share the swap; see activate.go). WebAuthn and
// the OIDC slot ride the snapshot so auth capabilities resolve per request
// from the CURRENT config instead of a boot-time copy.
type liveState struct {
	cfg       api.ConfigProvider
	engine    api.SearchEngine
	scorer    api.Scorer
	sonarr    api.SonarrClient
	radarr    api.RadarrClient
	webauthn  *webauthn.WebAuthn
	oidc      *oidcSlot
	providers []api.Provider
}

// storeFacade groups the narrow store interfaces.
type storeFacade struct {
	query queryhandlers.QueryStore
	sync  synchandlers.SyncStore
}

// authDeps groups authentication and session management dependencies.
// WebAuthn and the OIDC provider deliberately do NOT live here: they are
// config-derived capabilities carried by the live snapshot (liveState) and
// resolved per request, so a hot config edit takes effect without a restart.
type authDeps struct {
	authStore     authstore.AuthStore
	adminDB       authhandlers.AuthAdminStore
	secDB         authhandlers.SecurityStore
	oidcDB        authhandlers.OIDCStore
	authenticator sessionAuthenticator
	rateLimiter   ratelimit.Checker
	ceremonies    *authhandlers.CeremonyStore
	authH         *authhandlers.Handler
}

// pollDeps groups history-polling subsystem dependencies.
type pollDeps struct {
	pollCache *polling.PollCache
	poller    *polling.Poller
}

// scanSubsystem groups scanning subsystem dependencies.
type scanSubsystem struct {
	scanH         *scanning.Handler
	showSkipCache *showskip.Cache
	// stops tracks the live stop callbacks of running background scans
	// (activity-id keyed); composed with the activity log by the cancel
	// endpoint and the activity GET's cancellable merge.
	stops     activity.StopRegistry
	scanGuard scanning.ScanGuard
}

// previewDeps groups video/poster preview subsystem dependencies.
type previewDeps struct {
	ffmpegSem    *semaphore.Weighted
	posterClient *posterClient
}

// Server is the main application server.
type Server struct {
	stores storeFacade
	ctx    context.Context
	pollDeps
	previewDeps
	db           api.Store
	subtitleProc api.SubtitleProcessor
	metrics      Metrics
	registry     api.ProviderRegistry
	manualH      *manualops.Handler
	previewH     *previewhandlers.Handler
	loadConfig   api.ConfigLoader
	newSonarr    func(baseURL, apiKey string) (api.SonarrClient, error)
	newRadarr    func(baseURL, apiKey string) (api.RadarrClient, error)
	wire         wiring.Func
	activity     *activity.Log
	live         atomic.Pointer[liveState]
	queryH       *queryhandlers.Handler
	schemaFunc   api.SchemaFunc
	configH      *confighandlers.Handler
	alerts       *activity.AlertLog
	events       *events.EventBus
	coverageH    *coveragehandlers.Handler
	syncH        *synchandlers.Handler
	fileH        *filehandlers.Handler
	mediaH       *mediahandlers.Handler
	scanSubsystem
	authDeps
	// logSetup re-runs the process-global logging setup on a logging-section
	// change (injected by the composition root via WithLogSetup; the server
	// package never owns slog configuration itself).
	logSetup func(level, format string)
	// launchWorkers overrides the background-worker launch inside the
	// workersOnce latch. Nil means the real worker set (launchWorkerSet);
	// tests inject a counter to assert launch cardinality.
	launchWorkers func()
	defaultConfig []byte
	// hostGateInner is the middleware chain INSIDE the Host allowlist gate,
	// assembled once by buildHandler before the listener opens;
	// applyHostAllowlist re-wraps it with the live config's HostPolicy.
	hostGateInner http.Handler
	// hostGated is the current HostPolicy-wrapped handler chain, swapped
	// atomically by applyHostAllowlist on every activation (an immutable
	// webhttp.HostPolicy cannot be mutated in place, so hot reload re-wraps)
	// and read locklessly per request by the handler buildHandler returns.
	hostGated atomic.Pointer[http.Handler]
	// routeRegs records every route registration (group + pattern) made by
	// registerRoutes; the wirespec consistency test compares it against the
	// endpoint table.
	routeRegs  []routeReg
	bgWg       sync.WaitGroup
	serverPort int
	reloadMu   sync.Mutex
	// workersOnce is the background-worker latch: the worker set launches at
	// most once per process, after the first successful activation.
	workersOnce sync.Once
	ready       webhttp.Ready
	configured  atomic.Bool
	scanning    atomic.Bool
}

// Option configures a Server during construction.
type Option func(*Server)

// WithConfig sets the initial configuration. Config-only by design: arr
// clients (and every other config-derived capability) are constructed by
// activation in both boot modes, never handed in from outside.
func WithConfig(cfg api.ConfigProvider) Option {
	return func(s *Server) {
		ls := &liveState{cfg: cfg}
		s.live.Store(ls)
		s.configured.Store(true)
	}
}

// WithWire sets the provider wiring function.
func WithWire(w wiring.Func) Option { return func(s *Server) { s.wire = w } }

// WithSchema sets the config schema function.
func WithSchema(f api.SchemaFunc) Option { return func(s *Server) { s.schemaFunc = f } }

// WithConfigLoader sets the config loader.
func WithConfigLoader(l api.ConfigLoader) Option { return func(s *Server) { s.loadConfig = l } }

// WithSubtitleProc sets the subtitle processor.
func WithSubtitleProc(p api.SubtitleProcessor) Option { return func(s *Server) { s.subtitleProc = p } }

// WithMetrics sets the metrics recorder.
func WithMetrics(m Metrics) Option { return func(s *Server) { s.metrics = m } }

// WithPort sets the HTTP listen port (unconfigured mode).
func WithPort(port int) Option { return func(s *Server) { s.serverPort = port } }

// WithArrClientFactories sets the factories for creating Sonarr and Radarr API
// clients, used by hot reload and config-save connectivity checks.
func WithArrClientFactories(
	newSonarr func(baseURL, apiKey string) (api.SonarrClient, error),
	newRadarr func(baseURL, apiKey string) (api.RadarrClient, error),
) Option {
	return func(s *Server) {
		s.newSonarr = newSonarr
		s.newRadarr = newRadarr
	}
}

// WithDefaultConfig sets the embedded default config bytes.
func WithDefaultConfig(cfg []byte) Option { return func(s *Server) { s.defaultConfig = cfg } }

// WithLogSetup injects the process-global logging setup so activation can
// re-apply a changed logging section without a restart. Owned by the
// composition root (main.go's setupLogging).
func WithLogSetup(f func(level, format string)) Option {
	return func(s *Server) { s.logSetup = f }
}
