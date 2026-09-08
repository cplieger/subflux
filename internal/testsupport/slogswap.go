package testsupport

import (
	"log"
	"log/slog"
	"testing"
)

// SwapDefaultLogger installs l as slog's default for the rest of the test and
// restores slog's default, log.Writer and log.Flags on cleanup.
//
// All three matter: slog.SetDefault also aims the standard log package at the
// installed handler and SKIPS that redirect for slog's own defaultHandler
// (which would deadlock on log's mutex), so reinstalling the previous logger
// cannot undo it, and slog's stock handler emits through log.Output. Restore
// slog first, or a previous non-defaultHandler logger re-runs the redirect.
func SwapDefaultLogger(t testing.TB, l *slog.Logger) {
	t.Helper()
	prev, prevWriter, prevFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(l)
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
}
