package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	authwebauthn "github.com/cplieger/auth/v5/webauthn"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/activityhandlers"
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
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/showskip"
	"github.com/cplieger/subflux/internal/server/storeops"
	"github.com/cplieger/subflux/internal/server/synchandlers"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/wiring"
	"github.com/cplieger/webhttp/v2"
	"golang.org/x/sync/semaphore"
)

// --- Types ---

// SonarrClient and RadarrClient are the arr surfaces New requires: the union of
// the narrow interfaces their consumers declare, the same shape and the same
// reason as Store and AuthStore in server.go. This is the ONE site that fans a
// single concrete arr client out to every handler family, the scan and poll
// subsystems, the scheduler and the reference resolver, so the union belongs
// here rather than in a types package that implements none of it.
//
// Composed by EMBEDDING, never by re-listing, so the width is derived: adding a
// method to polling.PollSonarrClient widens this automatically and removing one
// narrows it. The declaration these replace was a hand-written nine-method list
// in internal/subflux, and the ten embedded surfaces below already summed to exactly
// those nine — the list happened to be right, and nothing in the code would have
// noticed if it had stopped being.
//
// The width, measured: 9 of *arrsvc.Sonarr's 19 exported methods and 7 of
// *arrsvc.Radarr's 16, so unlike liveState.cfg below these ARE narrowing. No
// single consumer reads more than 5 of the 9 or 4 of the 7 — the poller, which
// is the only one that both polls history and looks items up by ID.
//
// An interface rather than the concrete *arrsvc.Sonarr, which is the opposite
// call from cfg, and the difference is what the type is: the arr client is this
// process's only network boundary to Sonarr and Radarr, and this field is the
// one seam where it enters. internal/server's own suite drives handler behaviour
// through it with in-process fakes; a concrete HTTP client here would turn every
// one of those into a test that needs a fake server speaking arrapi's JSON.
// *config.Config, by contrast, is data a test can just build.
//
// Exported because package main's arr factories name it in their return type,
// which is the same reason scanning.ScanCfg is exported for the scheduler.
type SonarrClient interface {
	coveragehandlers.CoverageSonarrClient
	filehandlers.FileSonarrClient
	manualops.ManualSonarrClient
	manualops.ResolveSonarrClient
	mediahandlers.MediaSonarrClient
	polling.PollSonarrClient
	queryhandlers.StatsSonarrClient
	resolve.SonarrEpisodes
	scanning.ScanHandlerSonarr
	scanning.ScanSonarrClient

	// The connectivity check a config save runs before activating a changed
	// arr. confighandlers declares it as its own one-method surface, which is
	// why it is embedded here rather than listed: Ping is the only method of
	// these nine that no scan, poll or handler path calls.
	confighandlers.ArrPinger
}

// RadarrClient is SonarrClient's movie-side twin; see that doc for the width
// and the placement. Seven methods, from the same ten consumers plus the ping.
type RadarrClient interface {
	coveragehandlers.CoverageRadarrClient
	filehandlers.FileRadarrClient
	manualops.ManualRadarrClient
	manualops.ResolveRadarrClient
	mediahandlers.MediaRadarrClient
	polling.PollRadarrClient
	queryhandlers.StatsRadarrClient
	resolve.RadarrMovie
	scanning.ScanHandlerRadarr
	scanning.ScanRadarrClient

	confighandlers.ArrPinger
}

// liveState holds all fields that are atomically swapped during activation
// (cold boot and hot reload share the swap; see activate.go). WebAuthn and
// the OIDC slot ride the snapshot so auth capabilities resolve per request
// from the CURRENT config instead of a boot-time copy.
//
// cfg is the CONCRETE *config.Config, and that is the deliberate shape rather
// than the absence of one. This package is the composition root for the
// configuration: it reads 22 of the type's 37 exported methods itself, and it
// hands the same value to fourteen subpackages whose own narrow interfaces
// need 13 more. A server-owned interface here would therefore have to declare
// 35 of 37 — everything except Close and Validate, which only main.go and
// config's own validator call. An "interface" that reproduces its single
// implementation minus two methods is that type with two methods missing, and
// it bought exactly one thing: six sites in this package type-asserting back
// to *config.Config to reach the nine methods it could not name.
//
// A nil cfg means unconfigured mode. With a concrete pointer that test is
// honest: an interface field holding a typed-nil *config.Config passed
// `!= nil` and then panicked on the first call.
type liveState struct {
	cfg       *config.Config
	engine    *search.Engine
	scorer    *scorer.Engine
	sonarr    SonarrClient
	radarr    RadarrClient
	webauthn  *authwebauthn.RelyingParty
	oidc      *oidcSlot
	providers []provider.Provider
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
	authStore     AuthStore
	authenticator sessionAuthenticator
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
	// lifetime is the process-scoped context, written once by Start or
	// StartUnconfigured from the context the process hands the server and
	// never derived from an *http.Request. The name says which context it is:
	// it lives as long as the process, so nothing read from it is cancelled by
	// a client hanging up.
	//
	// It is read only by the three server-scoped uses the concurrency audit
	// ratified: the S12 full-scan launcher (a client disconnect must never
	// cancel a scan, so background runs take their context from here and not
	// from the handler that started them), the ServerCtx/CtxFunc seams the
	// handler families declare for work that outlives a request, and the lazy
	// OIDC discovery every request shares. The background worker set no
	// longer reads it: it is handed the context at start (awaitWorkerLaunch).
	lifetime context.Context
	pollDeps
	previewDeps
	db           Store
	subtitleProc synchandlers.SubtitleProcessor
	metrics      Metrics
	registry     confighandlers.SchemaRegistry
	manualH      *manualops.Handler
	previewH     *previewhandlers.Handler
	loadConfig   confighandlers.ConfigLoader
	newSonarr    func(baseURL, apiKey string) (SonarrClient, error)
	newRadarr    func(baseURL, apiKey string) (RadarrClient, error)
	wire         wiring.Func
	activity     *activity.Log
	live         atomic.Pointer[liveState]
	queryH       *queryhandlers.Handler
	schemaFunc   subflux.SchemaFunc
	configH      *confighandlers.Handler
	alerts       *activity.AlertLog
	events       *events.EventBus
	coverageH    *coveragehandlers.Handler
	activityH    *activityhandlers.Handler
	storeOps     *storeops.Runner
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
	// workersOnce latch. Nil means the real worker set, launched by
	// awaitWorkerLaunch on the context handed to Start; tests inject a
	// counter to assert launch cardinality.
	launchWorkers func()
	// workerLaunch carries the "an activation succeeded, launch the workers"
	// signal from activate to the goroutine Start owns. It is a signal and
	// not a direct call so the worker set can only ever run on the process
	// context Start was handed, never on the context of the HTTP request
	// that happened to carry the first successful config save. Reach it
	// through workerLaunchSignal, never directly: a nil channel would make
	// the signal vanish into the select's default arm.
	workerLaunch  chan struct{}
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
	// workerLaunchOnce guards the lazy construction of workerLaunch.
	workerLaunchOnce sync.Once
	ready            webhttp.Ready
	configured       atomic.Bool
	scanning         atomic.Bool
}

// Option configures a Server during construction.
type Option func(*Server)

// WithConfig sets the initial configuration. Config-only by design: arr
// clients (and every other config-derived capability) are constructed by
// activation in both boot modes, never handed in from outside.
func WithConfig(cfg *config.Config) Option {
	return func(s *Server) {
		ls := &liveState{cfg: cfg}
		s.live.Store(ls)
		s.configured.Store(true)
	}
}

// WithWire sets the provider wiring function.
func WithWire(w wiring.Func) Option { return func(s *Server) { s.wire = w } }

// WithSchema sets the config schema function.
func WithSchema(f subflux.SchemaFunc) Option { return func(s *Server) { s.schemaFunc = f } }

// WithConfigLoader sets the config loader. Typed as the config handlers' own
// func type rather than re-declared here: this package never calls it, it only
// carries the value from the composition root to confighandlers, which does.
func WithConfigLoader(l confighandlers.ConfigLoader) Option {
	return func(s *Server) { s.loadConfig = l }
}

// WithSubtitleProc sets the subtitle processor.
// WithSubtitleProc injects the SRT processor. Typed as the sync handlers' own
// interface rather than re-declared here: the server calls none of its methods,
// it only carries the value to synchandlers and previewhandlers, and
// previewhandlers takes a narrower view of the same value.
func WithSubtitleProc(p synchandlers.SubtitleProcessor) Option {
	return func(s *Server) { s.subtitleProc = p }
}

// WithMetrics sets the metrics recorder. REQUIRED despite being an Option:
// twenty-two sites dereference s.metrics unguarded, so omitting it panics rather
// than degrading. requireServiceable asserts it at Start.
func WithMetrics(m Metrics) Option { return func(s *Server) { s.metrics = m } }

// WithPort sets the HTTP listen port (unconfigured mode).
func WithPort(port int) Option { return func(s *Server) { s.serverPort = port } }

// WithArrClientFactories sets the factories for creating Sonarr and Radarr API
// clients, used by hot reload and config-save connectivity checks.
func WithArrClientFactories(
	newSonarr func(baseURL, apiKey string) (SonarrClient, error),
	newRadarr func(baseURL, apiKey string) (RadarrClient, error),
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
