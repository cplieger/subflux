// Package app is subflux's composition root: the one boot path, the
// supervision of every long-running component, and the drain.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/cplieger/go/health"
	"github.com/cplieger/go/httpserver"
	"github.com/cplieger/go/measure"
	"github.com/cplieger/go/slogx"
	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/adminsock"
	"github.com/cplieger/subflux/internal/backup"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediarefresh"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider/providers"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// Paths are the files the process reads and writes.
type Paths struct {
	Config string
	// Store is the state file; statedb locks Store + ".lock" beside it.
	Store string
	// Health is the liveness marker the health subcommand stats.
	Health string
	// SocketDir holds Socket, the admin socket the local commands call.
	SocketDir, Socket string
}

// DefaultPaths are the container layout.
var DefaultPaths = Paths{
	Config: "/config/config.yaml", Store: "/config/subflux.bolt", Health: health.DefaultPath,
	SocketDir: adminsock.Dir, Socket: adminsock.Path,
}

// Config is what Run boots from. Every field but Paths is optional.
type Config struct {
	// Listener, when set, replaces binding the HTTP port (tests).
	Listener net.Listener
	// LogOutput is where the process logs; nil is stderr.
	LogOutput io.Writer
	// Routes, when set, adds routes to the mux before serving (tests).
	Routes func(*http.ServeMux)
	// Settings, when set, receives the settings service once it is built, so
	// a test can save through it.
	Settings func(*settings.Service)
	// Gather, when set, receives the metrics registry's Gather (tests).
	Gather func(func(context.Context) ([]measure.Family, error))
	// Status, when set, receives the status store once it is built (tests).
	Status func(*status.Store)
	// Accounts, when set, receives the account service once it is built,
	// before boot activates the loaded settings (tests).
	Accounts func(*accounts.Service)
	// Scanner, when set, receives the media sources' Scanner once the first
	// Active settings build it (tests).
	Scanner func(*scan.Scanner)
	// Orders, when set, replaces the aired order join over SkyHook (tests).
	Orders mediasource.OrderJoin
	// Store, when set, receives the open state store (tests).
	Store func(*statedb.Store)
	// Writer, when set, receives the media writer once it is built (tests).
	Writer func(*mediawrite.Writer)
	// SourcesApply, when set, runs before the media sources apply each
	// settings commit after boot, and that apply waits for it to return
	// (tests).
	SourcesApply func(context.Context)
	Paths        Paths
}

// The HTTP server's bounds; a streaming route clears its own write deadline
// through http.ResponseController.
const (
	readHeaderTimeout = 10 * time.Second
	writeTimeout      = 60 * time.Second
	maxHeaderBytes    = 64 << 10
)

// The process's address, budgets and read bound; tests shorten them.
var (
	listenAddr = ":8374"
	// drainBudget is the whole shutdown, both phases, below compose's 10 s
	// stop grace.
	drainBudget = 8 * time.Second
	// httpGrace bounds the first phase: the producers, HTTP shutdown among
	// them, and the settings service.
	httpGrace   = 5 * time.Second
	readTimeout = 10 * time.Second
)

// activateFunc makes a parsed configuration live in the store.
type activateFunc = func(context.Context, settings.Active) error

// Test seams: what status persists through, the activate function boot
// and every Save call, and the method guard each activation runs.
var (
	statusPersister = func(s *statedb.Store) status.Persister { return s }
	wrapActivate    = func(f activateFunc) activateFunc { return f }
	wrapGuard       = func(g statedb.MethodGuard) statedb.MethodGuard { return g }
)

// Run boots and serves until ctx ends or a component returns, then drains.
// It returns a boot failure (the store, the activation of the loaded
// configuration and the TCP bind included), the component's return that
// began the drain ("<name> exited" when it returned nil), or a drain
// overrun; nil when ctx's end began the drain. The store stays open after an
// overrun, for a request or job still on it. cmd/subflux exits 1 on any
// error.
func Run(ctx context.Context, cfg Config) error { //nolint:gocritic // hugeParam: callers build Config inline and Run reads it once
	table := providers.New()
	reg := registries(table, cfg.Paths.Store)
	snap := settings.Load(cfg.Paths.Config, reg)
	level, format := setupLogging(snap, cfg.LogOutput)
	logLoad(snap)
	marker := health.NewMarker(cfg.Paths.Health)
	latch := health.NewLatch(marker)
	latch.Set(false)
	defer marker.Cleanup()

	store, err := statedb.Open(cfg.Paths.Store)
	if err != nil {
		slog.Error("app: could not open the state store", "path", cfg.Paths.Store, "error", err)
		return fmt.Errorf("app: %w", err)
	}
	sv, err := serve(ctx, &cfg, &process{snap: snap, registries: reg, providers: table, store: store, marker: marker, level: level, format: format})
	if err != nil {
		return errors.Join(err, store.Close())
	}
	latch.Set(true)
	sv.beginDrain = latch.BeginDrain
	return supervise(ctx, sv)
}

// process is what Run sets up before serve: the loaded snapshot, the
// registries and provider table it was parsed with, the open store, the
// health marker and the log handler's level and format.
type process struct {
	marker     health.Signal
	providers  *providers.Table
	store      *statedb.Store
	level      *slog.LevelVar
	snap       settings.Snapshot
	registries settings.Registries
	format     slogx.Format
}

// serve builds every component over the open store and binds the HTTP port,
// returning the supervision Run drains; on an error nothing is running.
func serve(ctx context.Context, cfg *Config, p *process) (_ *supervision, rerr error) {
	store := p.store
	st, err := status.New(ctx, statusPersister(store))
	if err != nil {
		slog.Error("app: could not open the job records", "error", err)
		return nil, fmt.Errorf("app: %w", err)
	}
	if cfg.Status != nil {
		cfg.Status(st)
	}
	m := obs.New()
	det := clientaddr.NewDetector()
	if cfg.Gather != nil {
		cfg.Gather(m.Gather)
	}
	if cfg.Store != nil {
		cfg.Store(store)
	}
	refresher := mediarefresh.New(mediarefresh.Deps{Store: store, Logger: slog.Default()})
	live := newLiveSources(refresher, mediawrite.Deps{Store: store, Alerts: st, Policy: store.Policy, Logger: slog.Default()})
	writer := live.writer
	if cfg.Writer != nil {
		cfg.Writer(writer)
	}
	// accts is built after the settings service its getters read; activate,
	// activateAuth and the save check call it only from svc.Boot and
	// svc.Save, after it is set.
	var accts *accounts.Service
	guard := func() statedb.MethodGuard { return wrapGuard(accts.MethodGuard()) }
	methods := func(ctx context.Context, c accounts.Config) []formschema.FieldError {
		return accts.PreCheckMethods(ctx, c)
	}
	activate := wrapActivate(newActivate(store, st, writer, guard))
	deps := sourceDeps(slog.Default())
	svc := settings.NewService(cfg.Paths.Config, p.registries, p.snap, activate, newActivateAuth(store, guard), saveCheck(deps, methods))
	if cfg.Settings != nil {
		cfg.Settings(svc)
	}
	if accts, err = newAccounts(ctx, store, svc, m); err != nil {
		slog.Error("app: could not build the account service", "error", err)
		return nil, fmt.Errorf("app: %w", err)
	}
	if cfg.Accounts != nil {
		cfg.Accounts(accts)
	}
	backups := backup.New(backup.Deps{Store: store, Status: st, Metrics: m, Logger: slog.Default(), Config: func() backup.Config {
		a, _ := svc.Snapshot().Active()
		return a.Backup
	}})
	sock := adminsock.New(adminsock.Deps{Accounts: accts, Settings: passwordLogin{svc}, Store: store, Logger: slog.Default()})
	orders := cfg.Orders
	if orders == nil {
		orders = airedOrders(deps, store)
	}
	sources := newSourcesLoop(&sourcesWiring{
		snapshot: svc.Snapshot, deps: deps, orders: orders, store: store, status: st, metrics: m,
		live: live, built: cfg.Scanner, beforeApply: cfg.SourcesApply,
	})
	// A failure from here on returns before the drain could stop accts.
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, accts.Run(canceled(ctx)))
		}
	}()
	if bootErr := bootSettings(ctx, svc, sources); bootErr != nil {
		return nil, bootErr
	}
	m.SetConfigMode(modeName(svc.Snapshot()))
	bootAuth, _ := svc.Booted().Auth()
	st.Reconcile(status.AlertAuthDisabled, accounts.BypassEnabled(bootAuth), "authentication is disabled by auth.disable_auth: every request acts as the admin")
	loop := &changeLoop{
		svc: svc, level: p.level, metrics: m, alerts: st, exposure: det, applied: sources.signal,
		running: p.format, bootBypass: accounts.BypassEnabled(bootAuth),
	}
	loop.server, loop.serverOK = svc.Snapshot().Server()
	loop.reconcileRestart(svc.Snapshot())

	ln, err := listen(ctx, cfg.Listener)
	if err != nil {
		return nil, err
	}
	srv := httpserver.New(handler(m, p.marker, cfg.Routes), httpserver.WithSlogErrorLog(slog.LevelWarn))
	srv.ReadTimeout = readTimeout
	srv.ReadHeaderTimeout = readHeaderTimeout
	srv.WriteTimeout = writeTimeout
	srv.MaxHeaderBytes = maxHeaderBytes
	producers := []component{
		httpProducer(srv, ln),
		{name: "settings change loop", run: loop.run},
		{name: "media sources", run: sources.run},
		{name: "media refresh", run: refresher.Run},
		{name: "alert metrics", run: func(ctx context.Context) error { return alertMetrics(ctx, st, m) }},
		{name: "backups", run: backups.Run},
		{name: "store health", run: func(ctx context.Context) error { return storeHealth(ctx, store, st) }},
	}
	if sockLn, ok := bindAdminSocket(ctx, cfg.Paths.SocketDir, cfg.Paths.Socket, st); ok {
		producers = append(producers, component{name: "admin socket", run: func(ctx context.Context) error { return sock.Serve(ctx, sockLn) }})
	}
	slog.Info("HTTP server listening", "addr", ln.Addr().String())
	return &supervision{
		producers: producers,
		sinks: []component{
			{name: "job records", run: st.Run}, {name: "media writer", run: writer.Run}, {name: "accounts", run: accts.Run},
		},
		shutdownSettings: svc.Shutdown,
		closeProviders:   p.providers.Close,
		closeStore:       store.Close,
		drainBudget:      drainBudget,
		httpGrace:        httpGrace,
	}, nil
}

// bootSettings activates the loaded settings through svc.Boot, which may turn them
// Failed on a method guard refusal, and builds the media sources over the
// settings then in force when they are Active; either error fails boot.
func bootSettings(ctx context.Context, svc *settings.Service, sources *sourcesLoop) error {
	if err := svc.Boot(ctx); err != nil {
		slog.Error("app: could not activate the loaded settings", "error", err)
		return fmt.Errorf("app: activate the loaded settings: %w", err)
	}
	if _, ok := svc.Snapshot().Active(); !ok {
		return nil
	}
	if err := sources.apply(); err != nil {
		slog.Error("app: could not build the media sources", "error", err)
		return fmt.Errorf("app: build the media sources: %w", err)
	}
	return nil
}

// listen is ln, or the HTTP port bound when ln is nil.
func listen(ctx context.Context, ln net.Listener) (net.Listener, error) {
	if ln != nil {
		return ln, nil
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddr)
	if err != nil {
		slog.Error("app: could not bind the HTTP port", "addr", listenAddr, "error", err)
		return nil, fmt.Errorf("app: listen on %s: %w", listenAddr, err)
	}
	return ln, nil
}

// bindAdminSocket binds the admin socket at path in dir. A failure logs
// ERROR and raises admin_socket, and boot continues without the local
// commands.
func bindAdminSocket(ctx context.Context, dir, path string, st *status.Store) (net.Listener, bool) {
	ln, err := adminsock.Listen(ctx, dir, path)
	if err != nil {
		slog.Error("app: could not bind the admin socket; the local commands are unavailable", "path", path, "error", err)
		st.Reconcile(status.AlertAdminSocket, true, "the admin socket could not bind, so the local commands are unavailable: "+err.Error())
		return nil, false
	}
	return ln, true
}

// authHTTPTimeout bounds each breached-password and OIDC call.
const authHTTPTimeout = 10 * time.Second

// newAccounts builds the account service over store, its getters reading
// svc's snapshot in force and disable_auth svc's booted one.
func newAccounts(ctx context.Context, store *statedb.Store, svc *settings.Service, m *obs.Metrics) (*accounts.Service, error) {
	secrets, err := store.AuthSecrets(ctx)
	if err != nil {
		return nil, err
	}
	server := func() settings.Server {
		s, _ := svc.Snapshot().Server()
		return s
	}
	return accounts.New(ctx, accounts.Deps{
		Store:   store.Accounts(),
		Secrets: secrets,
		Config:  func() (accounts.Config, bool) { return svc.Snapshot().Auth() },
		ServerParsed: func() bool {
			_, ok := svc.Snapshot().Server()
			return ok
		},
		AllowedHosts: func() []string { return server().AllowedHosts() },
		Resolver:     func() clientaddr.Resolver { return clientaddr.New(server().TrustedProxies()) },
		Forwarder:    httpserver.NewForwarderCheck().IsForwarder,
		Metrics:      m,
		Breach:       &http.Client{Timeout: authHTTPTimeout},
		OIDC:         &http.Client{Timeout: authHTTPTimeout},
		Logger:       slog.Default(),
		Boot: func() accounts.Config {
			c, _ := svc.Booted().Auth()
			return c
		},
	})
}

// canceled is a context already ended, carrying ctx's values: a component's
// Run handed it stops at once.
func canceled(ctx context.Context) context.Context {
	c, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cancel()
	return c
}

// modeName is snap's mode as config_mode labels it.
func modeName(snap settings.Snapshot) string {
	b, err := snap.Mode().MarshalText()
	if err != nil {
		return ""
	}
	return string(b)
}
