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
	"github.com/cplieger/subflux/internal/mediarefresh"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/obs"
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
}

// DefaultPaths are the container layout.
var DefaultPaths = Paths{Config: "/config/config.yaml", Store: "/config/subflux.bolt", Health: health.DefaultPath}

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

// Test seams: what status persists through, and the activate function boot
// and every Save call.
var (
	statusPersister = func(s *statedb.Store) status.Persister { return s }
	wrapActivate    = func(f activateFunc) activateFunc { return f }
)

// Run boots and serves until ctx ends or a component returns, then drains.
// It returns a boot failure (the store, the activation of the loaded
// configuration and the TCP bind included), the component's return that
// began the drain ("<name> exited" when it returned nil), or a drain
// overrun; nil when ctx's end began the drain. The store stays open after an
// overrun, for a request or job still on it. cmd/subflux exits 1 on any
// error.
func Run(ctx context.Context, cfg Config) error { //nolint:gocritic // hugeParam: callers build Config inline and Run reads it once
	snap := settings.Load(cfg.Paths.Config, registries)
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
	sv, err := serve(ctx, &cfg, &process{snap: snap, store: store, marker: marker, level: level, format: format})
	if err != nil {
		return errors.Join(err, store.Close())
	}
	latch.Set(true)
	sv.beginDrain = latch.BeginDrain
	return supervise(ctx, sv)
}

// process is what Run sets up before serve: the loaded snapshot, the open
// store, the health marker and the log handler's level and format.
type process struct {
	snap   settings.Snapshot
	store  *statedb.Store
	marker health.Signal
	level  *slog.LevelVar
	format slogx.Format
}

// serve builds every component over the open store and binds the HTTP port,
// returning the supervision Run drains; on an error nothing is running.
func serve(ctx context.Context, cfg *Config, p *process) (*supervision, error) {
	snap, store := p.snap, p.store
	st, err := status.New(ctx, statusPersister(store))
	if err != nil {
		slog.Error("app: could not open the job records", "error", err)
		return nil, fmt.Errorf("app: %w", err)
	}
	if cfg.Status != nil {
		cfg.Status(st)
	}
	m := obs.New()
	m.SetConfigMode(modeName(snap))
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
	activate := wrapActivate(func(ctx context.Context, a settings.Active) error {
		act := activationOf(&a)
		if aerr := store.Activate(ctx, act); aerr != nil {
			return aerr
		}
		retainSources(st, act.Sources)
		// Last, so a refused activation leaves the writer's section as it was.
		writer.SetConfig(a.Media)
		return nil
	})
	deps := sourceDeps(slog.Default())
	svc := settings.NewService(cfg.Paths.Config, registries, snap, activate, saveCheck(deps))
	if cfg.Settings != nil {
		cfg.Settings(svc)
	}
	orders := cfg.Orders
	if orders == nil {
		orders = airedOrders(deps, store)
	}
	sources := newSourcesLoop(&sourcesWiring{
		snapshot: svc.Snapshot, deps: deps, orders: orders, store: store, status: st, metrics: m,
		live: live, built: cfg.Scanner, beforeApply: cfg.SourcesApply,
	})
	if a, ok := snap.Active(); ok {
		if bootErr := activateLoaded(ctx, &a, activate, sources); bootErr != nil {
			return nil, bootErr
		}
	}

	ln := cfg.Listener
	if ln == nil {
		if ln, err = (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddr); err != nil {
			slog.Error("app: could not bind the HTTP port", "addr", listenAddr, "error", err)
			return nil, fmt.Errorf("app: listen on %s: %w", listenAddr, err)
		}
	}
	srv := httpserver.New(handler(m, p.marker, cfg.Routes), httpserver.WithSlogErrorLog(slog.LevelWarn))
	srv.ReadTimeout = readTimeout
	srv.ReadHeaderTimeout = readHeaderTimeout
	srv.WriteTimeout = writeTimeout
	srv.MaxHeaderBytes = maxHeaderBytes
	loop := &changeLoop{svc: svc, level: p.level, metrics: m, alerts: st, applied: sources.signal, running: p.format}
	slog.Info("HTTP server listening", "addr", ln.Addr().String())
	return &supervision{
		producers: []component{
			httpProducer(srv, ln),
			{name: "settings change loop", run: loop.run},
			{name: "media sources", run: sources.run},
			{name: "media refresh", run: refresher.Run},
			{name: "alert metrics", run: func(ctx context.Context) error { return alertMetrics(ctx, st, m) }},
		},
		sinks:            []component{{name: "job records", run: st.Run}, {name: "media writer", run: writer.Run}},
		shutdownSettings: svc.Shutdown,
		closeStore:       store.Close,
		drainBudget:      drainBudget,
		httpGrace:        httpGrace,
	}, nil
}

// activationOf is the part of a the store keys coverage on.
func activationOf(a *settings.Active) statedb.Activation {
	return statedb.Activation{Languages: a.Languages, Sources: sourceIDs(a), IgnoredCodecs: a.Embedded.Ignored()}
}

// activateLoaded activates the loaded settings a and builds the media
// sources over them, either failing boot.
func activateLoaded(ctx context.Context, a *settings.Active, activate activateFunc, sources *sourcesLoop) error {
	if err := activate(ctx, *a); err != nil {
		slog.Error("app: could not activate the loaded settings", "error", err)
		return fmt.Errorf("app: activate the loaded settings: %w", err)
	}
	if err := sources.apply(); err != nil {
		slog.Error("app: could not build the media sources", "error", err)
		return fmt.Errorf("app: build the media sources: %w", err)
	}
	return nil
}

// modeName is snap's mode as config_mode labels it.
func modeName(snap settings.Snapshot) string {
	b, err := snap.Mode().MarshalText()
	if err != nil {
		return ""
	}
	return string(b)
}
