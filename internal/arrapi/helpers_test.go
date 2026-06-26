package arrapi

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// --- slog capture for operator-facing log assertions ---

// logRecord is a captured slog record: its message plus a flattened map of
// its attributes.
type logRecord struct {
	attrs map[string]any
	msg   string
}

// logCaptureHandler records every slog record emitted while installed as the
// default logger, so tests can assert on the documented INFO summary lines
// (the pre-scan totals subflux logs before a scan).
type logCaptureHandler struct {
	recs []logRecord
	mu   sync.Mutex
}

func (h *logCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *logCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	rec := logRecord{msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.recs = append(h.recs, rec)
	h.mu.Unlock()
	return nil
}

func (h *logCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *logCaptureHandler) WithGroup(string) slog.Handler      { return h }

// find returns the first captured record with the given message.
func (h *logCaptureHandler) find(msg string) (logRecord, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.recs {
		if h.recs[i].msg == msg {
			return h.recs[i], true
		}
	}
	return logRecord{}, false
}

// captureLogs redirects the default slog logger to an in-memory handler for
// the duration of the test and restores it afterward. Callers must NOT use
// t.Parallel(): slog.Default is a global, so the capture is only isolated
// while the test runs in the sequential phase (parallel tests are parked).
func captureLogs(t *testing.T) *logCaptureHandler {
	t.Helper()
	h := &logCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// logAttrInt extracts an integer attribute from a captured record, failing the
// test if the key is missing or not integer-valued.
func logAttrInt(t *testing.T, rec logRecord, key string) int64 {
	t.Helper()
	v, ok := rec.attrs[key]
	if !ok {
		t.Fatalf("log attr %q missing; have %v", key, rec.attrs)
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		t.Fatalf("log attr %q = %T (%v), want integer", key, v, v)
		return 0
	}
}
