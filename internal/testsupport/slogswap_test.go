package testsupport

import (
	"bytes"
	"log"
	"log/slog"
	"testing"
)

// TestSwapDefaultLogger_restores_all_three_globals is the pin on the whole
// restore. slog.SetDefault redirects the standard log package's writer and
// zeroes its flags, and reinstalling a defaultHandler-backed logger does not
// undo either, so a restore that only reinstalls slog's default leaves log
// writing into a buffer nobody can reach — which silences every later slog call
// in the process, because slog's stock handler emits through log.Output.
//
// The previous logger here is deliberately NOT slog's defaultHandler, so the
// cleanup's own slog.SetDefault re-runs the redirect: this also pins that the
// log globals are restored AFTER slog's, not before.
//
// No t.Parallel: it asserts on process-global logging state.
func TestSwapDefaultLogger_restores_all_three_globals(t *testing.T) {
	prevLogger, prevWriter, prevFlags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})

	// Install the previous logger FIRST: SetDefault overwrites log's writer and
	// flags, so the sentinel state has to be staged after it to be the state
	// SwapDefaultLogger is asked to restore. Lshortfile is non-zero, so
	// SetDefault's log.SetFlags(0) is observable.
	wantLogger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	slog.SetDefault(wantLogger)
	sentinel := &bytes.Buffer{}
	log.SetOutput(sentinel)
	log.SetFlags(log.Lshortfile)

	t.Run("swapped", func(t *testing.T) {
		SwapDefaultLogger(t, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

		// Without these two the restore assertions below could pass while
		// restoring nothing.
		if log.Writer() == sentinel {
			t.Error("slog.SetDefault() left log's writer alone; the writer restore is unpinned")
		}
		if got := log.Flags(); got != 0 {
			t.Errorf("log.Flags() = %d after slog.SetDefault(), want 0; the flags restore is unpinned", got)
		}
	})

	if got := log.Writer(); got != sentinel {
		t.Errorf("log.Writer() = %#v after cleanup, want the sentinel buffer", got)
	}
	if got := log.Flags(); got != log.Lshortfile {
		t.Errorf("log.Flags() = %d after cleanup, want %d", got, log.Lshortfile)
	}
	if got := slog.Default(); got != wantLogger {
		t.Errorf("slog.Default() = %p after cleanup, want %p", got, wantLogger)
	}
}
