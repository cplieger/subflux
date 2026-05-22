package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"subflux/internal/api"
	"subflux/internal/auth"
	"subflux/internal/authstore"
	"subflux/internal/ratelimit"
	"subflux/internal/server/activity"
	"subflux/internal/server/authhandlers"
	"subflux/internal/server/confighandlers"
	"subflux/internal/server/coveragehandlers"
	"subflux/internal/server/events"
	"subflux/internal/server/filehandlers"
	"subflux/internal/server/manualops"
	"subflux/internal/server/mediahandlers"
	"subflux/internal/server/polling"
	"subflux/internal/server/previewhandlers"
	"subflux/internal/server/queryhandlers"
	"subflux/internal/server/scanning"
	"subflux/internal/server/showskip"
	"subflux/internal/server/synchandlers"
	"subflux/internal/wiring"

	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/sync/semaphore"
)

// --- Types ---

// liveState holds all fields that are atomically swapped during hot reload.
type liveState struct {
	cfg       api.ConfigProvider
	engine    api.SearchEngine
	scorer    api.Scorer
	sonarr    api.ArrClient
	radarr    api.ArrClient
	providers []api.Provider
}

// storeFacade groups the narrow store interfaces.
type storeFacade struct {
	file  filehandlers.FileStore
	query queryhandlers.QueryStore
	cov   coveragehandlers.CoverageStore
	sync  syncStore
	dl    downloadStore
}

// authDeps groups authentication and session management dependencies.
type authDeps struct {
	authStore     authstore.AuthStore
	adminDB       authhandlers.AuthAdminStore
	secDB         authhandlers.SecurityStore
	oidcDB        authhandlers.OIDCStore
	authenticator sessionAuthenticator
	rateLimiter   ratelimit.Checker
	webauthn      *webauthn.WebAuthn
	oidcProvider  *auth.OIDCProvider
	oidcCfg       *api.OIDCConfig
	ceremonies    *authhandlers.CeremonyStore
	sessDebounce  *sessionActivityDebouncer
	sessBatcher   *sessionActivityBatcher
	authH         *authhandlers.Handler
	oidcInit      oidcLazyInit
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
	scanGuard     scanning.ScanGuard
}

// previewDeps groups video/poster preview subsystem dependencies.
type previewDeps struct {
	ffmpegSem    *semaphore.Weighted
	posterClient *http.Client
}

// Server is the main application server.
type Server struct {
	authDeps
	pollDeps
	scanSubsystem
	previewDeps

	db           api.Store
	subtitleProc api.SubtitleProcessor
	metrics      Metrics
	registry     api.ProviderRegistry
	wire         wiring.Func
	schemaFunc   api.SchemaFunc
	loadConfig   api.ConfigLoader
	newArrClient func(baseURL, apiKey string) (api.ArrClient, error)
	ctx          context.Context

	stores storeFacade

	live atomic.Pointer[liveState]

	queryH    *queryhandlers.Handler
	configH   *confighandlers.Handler
	manualH   *manualops.Handler
	previewH  *previewhandlers.Handler
	coverageH *coveragehandlers.Handler
	fileH     *filehandlers.Handler
	mediaH    *mediahandlers.Handler
	syncH     *synchandlers.Handler

	alerts   *activity.AlertLog
	activity *activity.Log
	events   *events.EventBus

	defaultConfig []byte

	bgWg       sync.WaitGroup
	reloadMu   sync.Mutex
	ready      atomic.Bool
	configured atomic.Bool
	scanning   atomic.Bool
	serverPort int
}

// Option configures a Server during construction.
type Option func(*Server)

// WithConfig sets the initial configuration and arr clients.
func WithConfig(cfg api.ConfigProvider, sonarr, radarr api.ArrClient) Option {
	return func(s *Server) {
		ls := &liveState{cfg: cfg, sonarr: sonarr, radarr: radarr}
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

// WithArrClientFactory sets the factory for creating arr API clients.
func WithArrClientFactory(f func(baseURL, apiKey string) (api.ArrClient, error)) Option {
	return func(s *Server) { s.newArrClient = f }
}

// WithDefaultConfig sets the embedded default config bytes.
func WithDefaultConfig(cfg []byte) Option { return func(s *Server) { s.defaultConfig = cfg } }
