// Package app is subflux's composition root: the one boot path, the
// supervision of every long-running component, and the drain.
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/cplieger/health"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/webhttp/v4"
)

// Paths are the files the process reads and writes.
type Paths struct {
	Config string
	// Health is the liveness marker the health subcommand stats.
	Health string
}

// DefaultPaths are the container layout.
var DefaultPaths = Paths{Config: "/config/config.yaml", Health: health.DefaultPath}

// Config is what Run boots from. Every field but Paths is optional.
type Config struct {
	Registries settings.Registries
	// Listener, when set, replaces binding the HTTP port (tests).
	Listener net.Listener
	// LogOutput is where the process logs; nil is stderr.
	LogOutput io.Writer
	// Routes, when set, adds routes to the mux before serving (tests).
	Routes func(*http.ServeMux)
	// Settings, when set, receives the settings service once it is built, so
	// a test can save through it.
	Settings func(*settings.Service)
	Paths    Paths
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

// Run boots and serves until ctx ends or a component returns, then drains.
// It returns a boot failure (the TCP bind included), the component's return
// that began the drain ("<name> exited" when it returned nil), or a drain
// overrun; nil when ctx's end began the drain. cmd/subflux exits 1 on any
// error.
func Run(ctx context.Context, cfg Config) error { //nolint:gocritic // hugeParam: callers build Config inline and Run reads it once
	snap := settings.Load(cfg.Paths.Config, cfg.Registries)
	level := setupLogging(snap, cfg.LogOutput)
	logLoad(snap)
	marker := health.NewMarker(cfg.Paths.Health)
	latch := health.NewLatch(marker)
	latch.Set(false)
	defer marker.Cleanup()

	m := obs.New()
	m.SetConfigMode(modeName(snap))
	svc := settings.NewService(cfg.Paths.Config, cfg.Registries, snap, nil, nil)
	if cfg.Settings != nil {
		cfg.Settings(svc)
	}

	ln := cfg.Listener
	if ln == nil {
		var err error
		if ln, err = (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddr); err != nil {
			slog.Error("app: could not bind the HTTP port", "addr", listenAddr, "error", err)
			return fmt.Errorf("app: listen on %s: %w", listenAddr, err)
		}
	}
	srv := webhttp.NewServer(handler(m, marker, cfg.Routes),
		webhttp.WithReadTimeout(readTimeout),
		webhttp.WithReadHeaderTimeout(readHeaderTimeout),
		webhttp.WithWriteTimeout(writeTimeout),
		webhttp.WithMaxHeaderBytes(maxHeaderBytes),
		webhttp.WithSlogErrorLog(slog.LevelWarn),
	)
	producers := []component{
		httpComponent(srv, ln),
		{name: "settings change loop", run: func(ctx context.Context) error {
			return changeLoop(ctx, svc, level, m, nil)
		}},
	}
	slog.Info("HTTP server listening", "addr", ln.Addr().String())
	latch.Set(true)
	return supervise(ctx, &supervision{
		producers:        producers,
		beginDrain:       latch.BeginDrain,
		shutdownSettings: svc.Shutdown,
		drainBudget:      drainBudget,
		httpGrace:        httpGrace,
	})
}

// modeName is snap's mode as config_mode labels it.
func modeName(snap settings.Snapshot) string {
	b, err := snap.Mode().MarshalText()
	if err != nil {
		return ""
	}
	return string(b)
}
