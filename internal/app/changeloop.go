package app

import (
	"context"
	"log/slog"

	"github.com/cplieger/go/slogx"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/status"
)

// changeSource is what the loop reads; *settings.Service implements it.
type changeSource interface {
	Changed() <-chan struct{}
	Snapshot() settings.Snapshot
	Diverged() bool
}

var _ changeSource = (*settings.Service)(nil)

// changeLoop is settings.Service.Changed's one reader. Per commit, and per
// Diverged change, it applies the live snapshot: the log level, the
// config_mode gauge, the config alert while the file holds settings that are
// not live, and restart_pending while the snapshot asks for a log format
// other than the running handler's. Last it calls applied, which must not
// block; a nil applied is skipped.
type changeLoop struct {
	svc     changeSource
	level   *slog.LevelVar
	metrics *obs.Metrics
	alerts  *status.Store
	applied func()
	running slogx.Format
}

func (l *changeLoop) run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-l.svc.Changed():
			snap := l.svc.Snapshot()
			if srv, ok := snap.Server(); ok {
				l.level.Set(srv.LogLevel())
			}
			l.metrics.SetConfigMode(modeName(snap))
			l.alerts.Reconcile(status.AlertConfig, l.svc.Diverged(), "config.yaml holds settings that are not live; save the settings to replace it")
			l.alerts.Reconcile(status.AlertRestartPending, logFormat(snap) != l.running, "the log format changes at the next restart")
			if l.applied != nil {
				l.applied()
			}
		}
	}
}
