package polling

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// logLine is a single captured slog record (level + message), used to assert
// which of the poller's WARN/ERROR observable log branches fired.
type logLine struct {
	msg   string
	level slog.Level
}

// logSink is a slog.Handler that records every emitted record so a test can
// assert on the poller's log output.
type logSink struct {
	lines []logLine
	mu    sync.Mutex
}

func (s *logSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *logSink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, logLine{level: r.Level, msg: r.Message})
	return nil
}

func (s *logSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *logSink) WithGroup(string) slog.Handler      { return s }

// has reports whether a record with the given level and message was captured.
func (s *logSink) has(level slog.Level, msg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.lines {
		if l.level == level && l.msg == msg {
			return true
		}
	}
	return false
}

// captureLogs installs a recording slog default for the test and restores the
// previous default on cleanup. Callers must NOT use t.Parallel() (the default
// logger is process-global).
func captureLogs(t *testing.T) *logSink {
	t.Helper()
	sink := &logSink{}
	prev := slog.Default()
	slog.SetDefault(slog.New(sink))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return sink
}

// fullDeps builds a Deps wired to the standard mock collaborators and the given
// store, used by the poll-import and poll-cycle tests.
func fullDeps(store PollerStore) Deps {
	return Deps{
		PollCache:  newTestPollCache(),
		Store:      store,
		Metrics:    &mockMetrics{},
		Alerts:     &mockAlerts{},
		Events:     &mockEvents{},
		StatsCache: &mockStatsCache{},
	}
}
