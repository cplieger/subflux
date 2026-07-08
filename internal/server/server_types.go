package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	authoidc "github.com/cplieger/auth/v2/oidc"
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
	oidcProvider  *authoidc.Provider
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
	newArrClient func(baseURL, apiKey string) (api.ArrClient, error)
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
	defaultConfig []byte
	bgWg          sync.WaitGroup
	serverPort    int
	queryHOnce    sync.Once
	mediaHOnce    sync.Once
	fileHOnce     sync.Once
	coverageHOnce sync.Once
	reloadMu      sync.Mutex
	ready         webhttp.Ready
	configured    atomic.Bool
	scanning      atomic.Bool
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
