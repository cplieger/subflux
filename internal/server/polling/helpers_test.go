package polling

// Level-scoped log assertions go through capture.Recorder.CountLevel
// directly (the former in-package hasRecord walk is gone): the poller's
// WARN/ERROR branch messages are package-unique prefixes, so
// CountLevel(level, msg) > 0 asserts exactly "this branch fired".

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
