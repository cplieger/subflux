package main

//go:generate go run ./cmd/wire-codegen

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cplieger/auth/v2/ratelimit"
	"github.com/cplieger/health"
	"github.com/cplieger/slogx"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/cliparse"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/config/schema"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/metrics"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/server"
	"github.com/cplieger/subflux/internal/syncworker"
	"github.com/cplieger/subflux/internal/wiring"
	"github.com/cplieger/webhttp"
)

// Compile-time interface satisfaction checks.
var (
	_ api.Store           = (*boltstore.DB)(nil)
	_ authstore.AuthStore = (*authstore.Store)(nil)
	_ api.ConfigProvider  = (*config.Config)(nil)
	_ api.Scorer          = (*scorer.Engine)(nil)
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
// missing required flag, invalid flag value) or non-interactive
// disambiguation required (`subflux search` resolution matched several
// candidates; narrow the query with --imdb/--tmdb or --type).
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
// Hidden specs (sync-worker) stay dispatchable but are never suggested.
func suggestCommand(s string) string {
	candidates := make([]string, 0, len(cliSpecs))
	for name, spec := range cliSpecs {
		if spec.Hidden {
			continue
		}
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
func handleCLI(cmd string) (code int, handled bool) {
	return dispatchCLI(cmd, os.Args[2:])
}

// dispatchCLI is handleCLI with the argv slice injected for testability.
// For a subcommand registered in cliSpecs it (a) prints command-specific
// help on --help/-h (code 0), (b) parses and validates the args in the
// single cliparse.ParseAndValidate pass — unknown flags, stray
// positionals, missing required flags, and malformed typed values are
// POSIX usage errors (code 2) — and (c) hands the parsed params to the
// spec's Run. Runners never reparse os.Args: the one Params value is the
// whole flag surface.
func dispatchCLI(cmd string, args []string) (code int, handled bool) {
	spec, registered := cliSpecs[cmd]
	if !registered {
		return 0, false
	}
	if cliparse.HelpRequested(args) {
		cliparse.PrintHelp(os.Stdout, &spec)
		return 0, true
	}
	params, err := cliparse.ParseAndValidate(args, &spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Fprintln(os.Stderr)
		cliparse.PrintHelp(os.Stderr, &spec)
		// POSIX exit code 2 for usage errors (missing required
		// args, unknown flags, malformed typed values).
		return 2, true
	}
	return spec.Run(params), true
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
	// S19: install a real JSON slog default before the FIRST line, so every
	// log line subflux ever emits — including pre-config bootstrap and
	// failures — is a real, consistently formatted slog record. The
	// config-derived setupLogging re-applies below once the config loads,
	// and activation re-applies hot logging changes.
	setupLogging("info", "json")

	// Remove stale health file from a previous crash before any early exits.
	m := ensureMarker()
	m.Set(false)
	slog.Info("subflux starting")

	if err := ensureConfigFile(configPath, defaultConfig); err != nil {
		return 1
	}

	cfg, err := config.Load(context.Background(), configPath)
	if err != nil {
		slog.Warn("starting in unconfigured mode",
			"reason", err.Error(),
			"config_path", configPath)

		// Open the database so config saves work.
		db, dbErr := boltstore.Open(dbPath)
		if dbErr != nil {
			slog.Error("failed to open database", "error", dbErr)
			return 1
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		defer db.Close(ctx)

		// Build auth store over the shared bbolt handle and start its sweeper.
		authDB := authstore.New(db.BoltDB())
		if err := authDB.Open(); err != nil {
			slog.Error("failed to start auth sweeper", "error", err)
			return 1
		}
		defer authDB.Close()

		defer m.Cleanup()

		reg := newProviderRegistry()
		syncExec := newSyncExec()
		srv := server.New(db, reg,
			append(serverOptions(reg, syncExec), server.WithPort(config.ServerPort))...)

		// Auth setup. WebAuthn/OIDC are built by activation on the first
		// successful config save, never here.
		rateLimiter := ratelimit.NewRateLimiter(ctx, ratelimit.DefaultConfig())
		defer rateLimiter.Stop()
		if err := srv.SetAuth(authDB, rateLimiter); err != nil {
			slog.Error("failed to assemble authenticator", "error", err)
			return 1
		}

		startAdminSocket(ctx, srv)

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

	db, err := boltstore.Open(dbPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		return 1
	}
	defer db.Close(ctx)

	// Build auth store over the shared bbolt handle and start its sweeper,
	// seeded with the configured session timeouts so eviction matches the
	// request-path validator (hot reload re-applies them on change).
	authDB := authstore.New(db.BoltDB())
	authDB.SetSessionTimeouts(cfg.SessionIdleTimeout(), cfg.SessionAbsoluteTimeout())
	if err := authDB.Open(); err != nil {
		slog.Error("failed to start auth sweeper", "error", err)
		return 1
	}
	defer authDB.Close()

	m := ensureMarker()
	defer m.Cleanup()

	// Arr clients, WebAuthn, and OIDC are NOT built here: activation
	// (server.Start -> activate) owns the construction of every
	// config-derived capability in both boot modes, so cold boot and hot
	// config saves cannot drift.
	reg := newProviderRegistry()
	syncExec := newSyncExec()
	srv := server.New(db, reg,
		append(serverOptions(reg, syncExec), server.WithConfig(cfg))...)

	// Auth setup.
	rateLimiter := ratelimit.NewRateLimiter(ctx, ratelimit.DefaultConfig())
	defer rateLimiter.Stop()
	if err := srv.SetAuth(authDB, rateLimiter); err != nil {
		slog.Error("failed to assemble authenticator", "error", err)
		return 1
	}

	startAdminSocket(ctx, srv)

	serveAndWait(ctx, cancel, m, func() {
		srv.Start(ctx, func() { m.Set(true) })
	})
	return 0
}

// --- Admin socket (Unix-domain bootstrap plane) ---

// startAdminSocket binds the admin bootstrap plane: srv.AdminHandler() served
// on a Unix domain socket inside the private 0700 directory
// config.AdminSocketDir. The directory is the custody boundary (only
// same-container processes running as the server's UID can traverse it); the
// 0600 chmod on the socket file is cosmetic hardening on top.
//
// Failure is DEGRADED, not fatal (R1.4): recovery via docker exec needs a
// running server anyway, and a fatal would turn a mount misconfig into a
// crash loop. On any setup error the failure is logged at ERROR, recorded as
// a persistent operator alert, and TCP serving continues without the
// bootstrap plane.
func startAdminSocket(ctx context.Context, srv *server.Server) {
	ln, err := adminSocketListener(ctx, config.AdminSocketDir, config.AdminSocketPath)
	if err != nil {
		slog.Error("admin socket unavailable; bootstrap commands will fail",
			"path", config.AdminSocketPath, "error", err)
		srv.RecordPersistentAlert("admin-socket",
			"Admin bootstrap socket unavailable at "+config.AdminSocketPath+": "+err.Error()+
				". reset-password / generate-api-key will not work until this is fixed.")
		return
	}

	adminSrv := webhttp.NewServer(srv.AdminHandler(),
		webhttp.WithReadTimeout(10*time.Second),
		webhttp.WithWriteTimeout(60*time.Second),
		webhttp.WithReadHeaderTimeout(10*time.Second),
	)
	go func() {
		// Shutdown rides the same signal context as the TCP server.
		if err := webhttp.Run(ctx, adminSrv, ln, nil); err != nil {
			slog.Error("admin socket server error", "error", err)
		}
		// Crash-path belt only: closing a net-created unix listener already
		// unlinks the socket file; this covers exit paths where Close did
		// not run to completion.
		if err := os.Remove(config.AdminSocketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("admin socket cleanup", "error", err)
		}
	}()
	slog.Info("admin socket listening", "path", config.AdminSocketPath)
}

// adminSocketListener prepares the custody directory and binds the Unix
// socket. dir and path are parameters (not the config constants) so tests
// can exercise the custody rules against a temp directory.
func adminSocketListener(ctx context.Context, dir, path string) (net.Listener, error) {
	if err := ensureAdminSocketDir(dir); err != nil {
		return nil, err
	}
	// Stale-socket clear: Go refuses to bind over an existing socket file
	// (a previous unclean exit leaves one behind).
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	// Cosmetic hardening only — the 0700 directory already gates access.
	if err := os.Chmod(path, 0o600); err != nil {
		slog.Warn("admin socket chmod failed (directory custody still applies)",
			"path", path, "error", err)
	}
	return ln, nil
}

// ensureAdminSocketDir creates the admin socket directory with the atomic
// 0700 mkdir that IS the custody boundary: there is no post-listen chmod
// window to race. A pre-existing entry is accepted only when it is already
// compliant — a plain directory, owned by the effective UID, mode 0700.
// Anything else (symlink, foreign owner, wider mode) is a hard error, never
// chmod'd into compliance: a foreign object at this path means another
// principal owns the name, and taking it over would hand them the socket.
func ensureAdminSocketDir(dir string) error {
	err := os.Mkdir(dir, 0o700)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create admin socket dir: %w", err)
	}
	fi, lerr := os.Lstat(dir)
	if lerr != nil {
		return fmt.Errorf("inspect existing admin socket dir: %w", lerr)
	}
	if !fi.IsDir() {
		return fmt.Errorf("admin socket dir %s exists and is not a plain directory (mode %v)", dir, fi.Mode())
	}
	if fi.Mode().Perm() != 0o700 {
		return fmt.Errorf("admin socket dir %s has mode %v, want 0700", dir, fi.Mode().Perm())
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("admin socket dir %s: cannot determine owner", dir)
	}
	if int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("admin socket dir %s owned by uid %d, want effective uid %d", dir, st.Uid, os.Geteuid())
	}
	return nil
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

// ensureConfigFile writes the embedded default config to path when no config
// file exists yet. All outcomes are logged through the real slog default
// (S19): the pre-config bootstrap lines must be valid JSON like every other
// line subflux emits.
func ensureConfigFile(path string, def []byte) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		slog.Error("failed to create config dir", "path", filepath.Dir(path), "error", err)
		return err
	}
	if err := os.WriteFile(path, def, 0o600); err != nil {
		slog.Error("failed to write default config", "path", path, "error", err)
		return err
	}
	slog.Info("created default config; finish setup in the web UI or edit the file", "path", path)
	return nil
}

// serverOptions is the single builder for the option set shared by BOTH
// server assemblies (configured cold boot and unconfigured mode); the callers
// append only their mode-specific option (WithConfig / WithPort). One builder
// means the two assemblies cannot drift apart again.
func serverOptions(reg api.ProviderRegistry, syncExec syncing.SyncExec) []server.Option {
	return []server.Option{
		server.WithDefaultConfig(defaultConfig),
		server.WithArrClientFactories(newSonarrFactory(), newRadarrFactory()),
		server.WithWire(newWireFunc(reg, syncExec)),
		server.WithSchema(schema.Schema),
		server.WithConfigLoader(newConfigLoader()),
		server.WithSubtitleProc(syncing.NewSubtitleProcessorWithExec(syncExec)),
		server.WithMetrics(metrics.New()),
		server.WithLogSetup(setupLogging),
	}
}

// newSonarrFactory returns a function that creates Sonarr API clients, used by
// the server for hot reload and config-save connectivity checks.
func newSonarrFactory() func(baseURL, apiKey string) (api.SonarrClient, error) {
	return func(baseURL, apiKey string) (api.SonarrClient, error) {
		c, err := arrsvc.NewSonarr(baseURL, apiKey)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
}

// newRadarrFactory returns a function that creates Radarr API clients.
func newRadarrFactory() func(baseURL, apiKey string) (api.RadarrClient, error) {
	return func(baseURL, apiKey string) (api.RadarrClient, error) {
		c, err := arrsvc.NewRadarr(baseURL, apiKey)
		if err != nil {
			return nil, err
		}
		return c, nil
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
// place that imports concrete implementation packages for wiring. syncExec
// carries the process-isolation executor (P13) into every engine the wire
// builds — one client instance (one concurrency-one slot) survives hot
// reloads, so replacing the config never doubles the alignment concurrency.
func newWireFunc(reg api.ProviderRegistry, syncExec syncing.SyncExec) wiring.Func {
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
			search.WithSyncer(syncing.Syncer{MinConfidence: cfg.SyncConfig().SyncMinConfidence, LangMapper: classify.Alpha2FromAlpha3, Exec: syncExec}),
			search.WithSyncExec(syncExec),
			search.WithTracks(embedded.Detector{}))
		return engine, sc, providers, nil
	}
}

// newSyncExec builds the server-mode sync executor: the sync-worker process
// client (P13 isolation). Falls back to in-process sync — the pre-P13
// behavior — only when the running executable's path cannot be resolved.
func newSyncExec() syncing.SyncExec {
	client, err := syncworker.NewClient()
	if err != nil {
		slog.Warn("sync worker unavailable; sync runs in-process", "error", err)
		return syncing.InProcessExec{LangMapper: classify.Alpha2FromAlpha3}
	}
	return client
}

// runSyncWorker is the hidden `subflux sync-worker` subcommand: one sync job
// from stdin, result to stdout, logs to stderr (joining the parent's
// stream). Its cliSpecs entry carries Hidden: true — dispatchable but
// excluded from root help; an internal process-isolation vehicle, not a
// user command.
func runSyncWorker() int {
	setupLogging("info", "json")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return syncworker.RunWorker(ctx, os.Stdin, os.Stdout)
}

// --- Logging ---

// setupLogging configures the global slog default with the given level and
// format, both parsed through slogx (ParseLevel / ParseFormat) so validation
// and consumption share one normalization. An unrecognized or empty value
// falls back to the documented defaults (info / json).
func setupLogging(level, format string) {
	lvl, _ := slogx.ParseLevel(level, slog.LevelInfo)
	f, _ := slogx.ParseFormat(format, slogx.JSON)
	slogx.Setup(slogx.Options{Format: f, Level: lvl})
}

// --- CLI Auth Commands ---
//
// Implementations live in cli.go alongside the other CLI runners.
