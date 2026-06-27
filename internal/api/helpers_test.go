package api

import (
	"bytes"
	"log/slog"
	"testing"
)

// captureSlog redirects the default slog logger to an in-memory buffer at
// DEBUG level for the duration of the test, restoring the previous default on
// cleanup. The log-guard tests assert on the captured output because the
// guarded slog.Debug / slog.Error calls emit through slog.Default(), which is
// the only observable that distinguishes a boolean guard flipping.
//
// A test using this helper must NOT call t.Parallel: it swaps the process-wide
// default logger, so it has to run during the serial phase of the package's
// test run.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}
