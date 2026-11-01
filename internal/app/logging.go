package app

import (
	"io"
	"log/slog"
	"strings"

	"github.com/cplieger/slogx"
	"github.com/cplieger/subflux/internal/settings"
)

// setupLogging installs the one process handler: the server section's level
// and format whenever the snapshot answers its server section (Failed
// included), else JSON at INFO. Later level changes go through the returned
// LevelVar, so every logger captured before them follows.
func setupLogging(snap settings.Snapshot, out io.Writer) *slog.LevelVar {
	opts := slogx.Options{Output: out, Format: slogx.JSON, Level: slog.LevelInfo}
	if srv, ok := snap.Server(); ok {
		opts.Level = srv.LogLevel()
		if f, ok := slogx.ParseFormat(srv.LogFormat(), slogx.JSON); ok {
			opts.Format = f
		}
	}
	return slogx.Setup(opts)
}

// logLoad logs what Load found: field paths, never values.
func logLoad(snap settings.Snapshot) {
	switch snap.Mode() {
	case settings.ModeActive:
		slog.Info("settings loaded")
	case settings.Failed:
		errs, _ := snap.Failure()
		paths := make([]string, len(errs))
		for i, e := range errs {
			paths[i] = e.Path
		}
		slog.Warn("settings failed to load; serving the platform sections that parsed", "fields", strings.Join(paths, ", "))
	default:
		slog.Info("no settings file; serving unconfigured")
	}
}
