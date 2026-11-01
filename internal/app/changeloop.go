package app

import (
	"context"
	"log/slog"

	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
)

// changeSource is what the loop reads; *settings.Service implements it.
type changeSource interface {
	Changed() <-chan struct{}
	Snapshot() settings.Snapshot
}

var _ changeSource = (*settings.Service)(nil)

// changeLoop is settings.Service.Changed's one reader. Per commit, and per
// Diverged change, it sets the log level and the config_mode gauge from the
// live snapshot, then calls applied, which must not block; a nil applied is
// skipped.
func changeLoop(ctx context.Context, svc changeSource, level *slog.LevelVar, m *obs.Metrics, applied func()) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-svc.Changed():
			snap := svc.Snapshot()
			if srv, ok := snap.Server(); ok {
				level.Set(srv.LogLevel())
			}
			m.SetConfigMode(modeName(snap))
			if applied != nil {
				applied()
			}
		}
	}
}
