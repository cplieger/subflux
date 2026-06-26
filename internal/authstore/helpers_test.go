package authstore

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// Shared test helpers for the authstore package. The slog-capture helpers below
// let the audit-log and cleanup-log tests across several source files assert on
// the package's observable logging behaviour without each file re-deriving the
// capturing handler.

// logRec is one captured slog record: its message plus a copy of its attributes
// (keyed by name) so a test can inspect attribute values.
type logRec struct {
	attrs map[string]slog.Value
	msg   string
}

// capHandler is a slog.Handler that records every log line into a shared slice
// under a mutex. It is enabled at every level so DEBUG lines (the cleanup
// guards) are captured.
type capHandler struct {
	mu   *sync.Mutex
	recs *[]logRec
}

func (capHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h capHandler) Handle(_ context.Context, r slog.Record) error {
	rec := logRec{msg: r.Message, attrs: make(map[string]slog.Value)}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, rec)
	h.mu.Unlock()
	return nil
}

func (h capHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h capHandler) WithGroup(string) slog.Handler      { return h }

// captureLogs installs a capturing slog default handler for the duration of the
// test and returns a snapshot getter. The previous default is restored on
// cleanup. Tests using it must not call t.Parallel (slog.SetDefault is
// process-global).
func captureLogs(t *testing.T) func() []logRec {
	t.Helper()
	mu := &sync.Mutex{}
	recs := &[]logRec{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capHandler{mu: mu, recs: recs}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []logRec {
		mu.Lock()
		defer mu.Unlock()
		out := make([]logRec, len(*recs))
		copy(out, *recs)
		return out
	}
}

// countMsg counts captured records with the given message.
func countMsg(recs []logRec, msg string) int {
	n := 0
	for i := range recs {
		if recs[i].msg == msg {
			n++
		}
	}
	return n
}
