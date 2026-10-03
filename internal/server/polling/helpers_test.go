package polling

import (
	"context"

	"github.com/cplieger/subflux/internal/testsupport"
)

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
		Media:      testsupport.MediaWriter(),
		Presence:   testsupport.MediaPresence("/"),
	}
}

// importOne resolves and runs one import the way a batch does, without the
// batch's write test.
func importOne(ctx context.Context, p *Poller, ls *LiveState, path string,
	buildFn func() (*ImportResult, error), refreshFn func(context.Context, int) error,
) importResult {
	pi := p.resolveImport(ctx, ls, path, buildFn, refreshFn)
	return p.runImport(ctx, ls, &pi)
}
