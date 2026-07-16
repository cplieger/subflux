package polling

import (
	"log/slog"

	"github.com/cplieger/slogx/capture"
)

// hasRecord reports whether a record with the given level and message was
// captured, used to assert which of the poller's WARN/ERROR observable log
// branches fired.
func hasRecord(rec *capture.Recorder, level slog.Level, msg string) bool {
	for _, r := range rec.Records() {
		if r.Level == level && r.Message == msg {
			return true
		}
	}
	return false
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
