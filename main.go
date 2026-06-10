package main

//go:generate go run ./cmd/wire-codegen

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/cplieger/auth/ratelimit"
	authwebauthn "github.com/cplieger/auth/webauthn"
	"github.com/cplieger/health"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/arrapi"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/cliparse"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/config/schema"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/provider/embedded"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/server"
	"github.com/cplieger/subflux/internal/store"
	"github.com/cplieger/subflux/internal/wiring"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Compile-time interface satisfaction checks.
var (
	_ api.Store           = (*store.DB)(nil)
	_ authstore.AuthStore = (*store.DB)(nil)
	_ api.ConfigProvider  = (*config.Config)(nil)
	_ api.Scorer          = (*scorer.Engine)(nil)
	_ api.ArrClient       = (*arrapi.Client)(nil)
)

//go:embed config.example.yaml
var defaultConfig []byte

// marker is the package-level health marker. Initialised lazily via
// ensureMarker() at server startup so CLI subcommands (which never touch
// the marker) don't probe the filesystem.
var marker *health.Marker

// ensureMarker lazily constructs the package-level health marker.
func ensureMarker() *health.Marker {
	if marker == nil {
		marker = health.NewMarker(health.DefaultPath)
	}
	return marker
}

// --- Main ---

func main() {
	os.Exit(run())
}

// run is the actual entry point. main delegates to run so deferred
// cleanup (DB close, ratelimiter stop, etc.) runs before os.Exit. Exit
// codes follow POSIX convention: 0 success, 1 runtime failure (DB
// error, network error, etc.), 2 usage error (unknown command,
// missing required flag, invalid flag value).
//
// Note: the health probe (`subflux health`) intentionally bypasses
// this pattern via runProbe's own os.Exit for fast-path termination
// in distroless containers; that site stays as-is.
func run() int {
	// CLI health probe for Docker healthcheck (distroless has no curl/wget).
	// Checks for a marker file instead of making an HTTP request; no port needed.
	if len(os.Args) > 1 && os.Args[1] == "health" {
		health.RunProbe(health.DefaultPath)
	}

	// Root help: `subflux --help` or `subflux -h`. The no-arg case is
	// preserved as "run the server" so the daemon entrypoint stays
	// trivial (Dockerfile CMD ["/subflux"] keeps working).
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		cliparse.PrintRootHelp(os.Stdout, orderedSpecs())
		return 0
	}

	if len(os.Args) > 1 {
		if code, handled := handleCLI(os.Args[1]); handled {
			return code
		}
		// Unrecognized subcommand. Surface a typo suggestion (Levenshtein
		// distance ≤ 2 against any known subcommand) before exiting.
		// POSIX exit code 2 for usage errors.
		cmd := os.Args[1]
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		if hint := suggestCommand(cmd); hint != "" {
			fmt.Fprintln(os.Stderr, hint)
		}
		fmt.Fprintln(os.Stderr, "\nRun 'subflux --help' for the full command list.")
		return 2
	}

	return runServer()
}

// suggestCommand returns "did you mean 'X'?" when an unknown subcommand
// is within edit distance 2 of a known command; empty string otherwise.
func suggestCommand(s string) string {
	candidates := make([]string, 0, len(cliSpecs))
	for name := range cliSpecs {
		candidates = append(candidates, name)
	}
	if name, ok := cliparse.SuggestName(s, candidates); ok {
		return fmt.Sprintf("did you mean '%s'?", name)
	}
	return ""
}

// handleCLI dispatches CLI subcommands. Returns (code, handled): code
// is the exit code (0 on success, 1 on runtime failure, 2 on usage
// error); handled is true when the subcommand was recognized. Caller
// uses handled=false to surface its own "unknown command" message.
//
// Centralizes help and flag validation: when the subcommand is registered
// in cliSpecs, this function (a) prints command-specific help on
// --help/-h and returns code 0, (b) rejects unknown flags with a
// Levenshtein suggestion and returns code 2, and (c) enforces required
// flags and typed-flag parsing before the actual runCLIxxx executes.
// The runCLIxxx functions can therefore trust that os.Args is
// structurally valid and read flags directly from cliparse.ParseArgs
// without redundant defensive checks.
func handleCLI(cmd string) (code int, handled bool) {
	spec, registered := cliSpecs[cmd]
	if registered {
		if cliparse.HelpRequested(os.Args[2:]) {
			cliparse.PrintHelp(os.Stdout, &spec)
			return 0, true
		}
		params, _ := cliparse.ParseArgs(os.Args[2:])
		if err := cliparse.Validate(os.Args[2:], params, &spec); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			fmt.Fprintln(os.Stderr)
			cliparse.PrintHelp(os.Stderr, &spec)
			// POSIX exit code 2 for usage errors (missing required
			// args, unknown flags, malformed typed values).
			return 2, true
		}
	}
	switch cmd {
	case cmdSearch:
		return runCLISearch(), true
	case cmdStatus:
		return runCLIRemote("/api/state/stats"), true
	case cmdState:
		return runCLIState(), true
	case cmdBackoff:
		return runCLIRemote("/api/backoff"), true
	case cmdLocks:
		return runCLIRemote("/api/locks"), true
	case cmdProviders:
		return runCLIRemote("/api/providers"), true
	case cmdUnlock:
		return runCLIUnlock(), true
	case cmdScan:
		return runCLIAction("/api/scan", "scan started"), true
	case cmdTimeouts:
		return runCLIRemote("/api/providers/timeout"), true
	case cmdTimeoutsReset:
		return runCLIAction("/api/providers/timeout/reset",
			"provider timeouts reset, all providers re-enabled"), true
	case cmdScore:
		return runCLIScore(), true
	case cmdResetPassword:
		return runCLIResetPassword(), true
	case cmdEnablePwLogin:
		return runCLIEnablePasswordLogin(), true
	case cmdGenerateAPIKey:
		return runCLIGenerateAPIKey(), true
	default:
		return 0, false
	}
}

// --- Server ---

// Fixed paths inside the container. Users control the host-side mount.
const (
	configPath = config.DefaultConfigPath
	dbPath     = config.DefaultDBPath
)

// runServer loads configuration, opens the database, and runs the HTTP server
// until a termination signal is received. Returns an exit code (0 on
// clean shutdown, 1 on early failure).
func runServer() int {
	// Remove stale health file from a previous crash before any early exits.
	m := ensureMarker()
	m.Set(false)
	fmt.Fprintf(os.Stderr, "level=INFO msg=\"subflux starting\"\n")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create config dir: %v\n", err)
			return 1
		}
		if err := os.WriteFile(configPath, defaultConfig, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write default config: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr,
			"Created default config at %s - edit it with your settings\n",
			configPath)
	}

	cfg, err := config.Load(context.Background(), configPath)
	if err != nil {
		// Set up basic logging so unconfigured mode messages are visible.
		setupLogging("info", "json")

		slog.Warn("starting in unconfigured mode",
			"reason", err.Error(),
			"config_path", configPath)

		// Open the database so config saves work.
		db, dbErr := store.Open(context.Background(), dbPath)
		if dbErr != nil {
			slog.Error("failed to open database", "error", dbErr)
			return 1
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		defer db.Close(ctx)

		defer m.Cleanup()

		reg := newProviderRegistry()
		mx := metrics.New()
		srv := server.New(db, reg,
			server.WithDefaultConfig(defaultConfig),
			server.WithArrClientFactory(newArrClientFactory()),
			server.WithWire(newWireFunc(reg)),
			server.WithSchema(schema.Schema),
			server.WithConfigLoader(newConfigLoader()),
			server.WithSubtitleProc(syncing.NewSubtitleProcessor()),
			server.WithMetrics(mx),
			server.WithPort(config.ServerPort),
		)

		// Auth setup (unconfigured mode: no WebAuthn or OIDC).
		rateLimiter := ratelimit.NewRateLimiter(ctx, ratelimit.DefaultConfig())
		defer rateLimiter.Stop()
		srv.SetAuth(db, rateLimiter, nil, nil)

		serveAndWait(ctx, cancel, m, func() {
			srv.StartUnconfigured(ctx, func() { m.Set(true) })
		})
		return 0
	}

	setupLogging(string(cfg.LoggingLevel()), string(cfg.LoggingFormat()))
	return runConfiguredServer(cfg)
}

// runConfiguredServer runs the HTTP server with a valid configuration.
// Separated from runServer to avoid exitAfterDefer warnings from the
// unconfigured-mode defers in the same function scope. Returns an
// exit code (0 on clean shutdown, 1 on early failure).
func runConfiguredServer(cfg *config.Config) int {
	defer cfg.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		return 1
	}
	defer db.Close(ctx)

	m := ensureMarker()
	defer m.Cleanup()

	// Create arr clients from config.
	var sonarr, radarr api.ArrClient
	if sc := cfg.SonarrConfig(); sc.URL != "" {
		c, err := arrapi.NewClient(sc.URL, sc.APIKey)
		if err != nil {
			slog.Error("invalid sonarr config", "error", err)
			return 1
		}
		sonarr = c
		defer c.Close()
	}
	if rc := cfg.RadarrConfig(); rc.URL != "" {
		c, err := arrapi.NewClient(rc.URL, rc.APIKey)
		if err != nil {
			slog.Error("invalid radarr config", "error", err)
			return 1
		}
		radarr = c
		defer c.Close()
	}

	reg := newProviderRegistry()
	mx := metrics.New()
	srv := server.New(db, reg,
		server.WithDefaultConfig(defaultConfig),
		server.WithConfig(cfg, sonarr, radarr),
		server.WithArrClientFactory(newArrClientFactory()),
		server.WithWire(newWireFunc(reg)),
		server.WithSchema(schema.Schema),
		server.WithConfigLoader(newConfigLoader()),
		server.WithSubtitleProc(syncing.NewSubtitleProcessor()),
		server.WithMetrics(mx),
	)

	// Auth setup.
	rateLimiter := ratelimit.NewRateLimiter(ctx, ratelimit.DefaultConfig())
	defer rateLimiter.Stop()

	// WebAuthn (may fail if RP ID is not configured).
	var wa *webauthn.WebAuthn
	if rpID := cfg.WebAuthnRPID(); rpID != "" {
		var err error
		wa, err = authwebauthn.NewWebAuthn(rpID, "Subflux", []string{"https://" + rpID})
		if err != nil {
			slog.Warn("WebAuthn initialization failed", "error", err)
		}
	}

	// OIDC provider (lazy init on first request to avoid blocking startup).
	if cfg.OIDCEnabled() {
		oidcCfg := cfg.OIDCConfig()
		srv.SetOIDCLazy(oidcCfg)
	}

	srv.SetAuth(db, rateLimiter, wa, nil)

	slog.Info("authentication configured",
		"webauthn", wa != nil,
		"oidc", cfg.OIDCEnabled(),
		"password", true)

	serveAndWait(ctx, cancel, m, func() {
		srv.Start(ctx, func() { m.Set(true) })
	})
	return 0
}

// serveAndWait starts fn in a goroutine and blocks until ctx is cancelled
// or fn returns early (e.g. port bind failure). Returns after fn completes.
func serveAndWait(ctx context.Context, cancel context.CancelFunc, m *health.Marker, fn func()) {
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down", "cause", context.Cause(ctx))
	case <-done:
		if ctx.Err() == nil {
			slog.Error("server exited unexpectedly, shutting down")
			cancel()
		}
	}

	m.Set(false)
	<-done
}

// --- Environment ---

// newArrClientFactory returns a function that creates arr API clients.
// Used by the server for hot reload (creating new clients from updated config).
func newArrClientFactory() func(baseURL, apiKey string) (api.ArrClient, error) {
	return func(baseURL, apiKey string) (api.ArrClient, error) {
		return arrapi.NewClient(baseURL, apiKey)
	}
}

// newConfigLoader returns a ConfigLoader that parses and validates config YAML.
func newConfigLoader() api.ConfigLoader {
	return func(data []byte) (api.ConfigProvider, error) {
		return config.LoadFromBytes(context.Background(), data)
	}
}

// newWireFunc returns the wiring.Func that creates the search engine, scorer,
// and loaded providers from config. This is the composition root: the only
// place that imports concrete implementation packages for wiring.
func newWireFunc(reg api.ProviderRegistry) wiring.Func {
	return func(ctx context.Context, cfg api.ConfigProvider, db api.Store, m search.SearchMetrics) (api.SearchEngine, api.Scorer, []api.Provider, error) {
		providers, err := reg.LoadAll(ctx, cfg.ProviderConfigs())
		if err != nil {
			return nil, nil, nil, err
		}
		providers = provider.WrapRetryAll(providers, provider.DownloadRetryAttempts, provider.DownloadRetryInitBackoff)
		scores := cfg.Scores()
		sc := scorer.New(&scores)
		engine := search.New(providers,
			search.WithStore(db), search.WithConfig(cfg),
			search.WithMetrics(m), search.WithScorer(sc),
			search.WithSyncer(syncing.Syncer{MinConfidence: cfg.SyncConfig().SyncMinConfidence, LangMapper: classify.Alpha2FromAlpha3}),
			search.WithTracks(embedded.ProviderDirect{}))
		return engine, sc, providers, nil
	}
}

// envOr returns the value of the environment variable key, or fallback if unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- Logging ---

// setupLogging configures the global slog default with the given level and format.
// Unrecognized levels default to info; format "json" selects JSON output.
func setupLogging(level, format string) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
}

// --- CLI Auth Commands ---
//
// Implementations live in cli.go alongside the other CLI runners.
