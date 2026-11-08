package app

import (
	"io"
	"log/slog"
	"strings"

	"github.com/cplieger/go/slogx"
	"github.com/cplieger/subflux/internal/settings"
)

// setupLogging installs the one process handler: the server section's level
// and format whenever the snapshot answers its server section (Failed
// included), else JSON at INFO. Later level changes go through the returned
// LevelVar, so every logger captured before them follows; the format is
// fixed for the process, and setupLogging returns it.
func setupLogging(snap settings.Snapshot, out io.Writer) (*slog.LevelVar, slogx.Format) {
	opts := slogx.Options{Output: out, Format: logFormat(snap), Level: slog.LevelInfo}
	if srv, ok := snap.Server(); ok {
		opts.Level = srv.LogLevel()
	}
	return slogx.Setup(opts), opts.Format
}

// logFormat is the handler format snap asks for: its server section's when
// Server answers true, else JSON.
func logFormat(snap settings.Snapshot) slogx.Format {
	srv, ok := snap.Server()
	if !ok {
		return slogx.JSON
	}
	f, _ := slogx.ParseFormat(srv.LogFormat(), slogx.JSON)
	return f
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
