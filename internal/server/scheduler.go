package server

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/scheduler"
)

// authCleanupInterval is how often auth ceremonies and session debounce
// state are pruned.
const authCleanupInterval = scheduler.AuthCleanupInterval

// runScheduler runs the periodic scan and upgrade tickers until ctx is cancelled.
func (s *Server) runScheduler(ctx context.Context) {
	deps := s.schedulerDeps()
	scheduler.Run(ctx, deps)
}

// schedulerDeps builds the scheduler.Deps from Server fields.
func (s *Server) schedulerDeps() *scheduler.Deps {
	return &scheduler.Deps{
		DB:                    s.db,
		ScanDB:                s.db,
		Backoff:               s.db,
		Metrics:               s.metrics,
		ReconcileMetrics:      s.metrics,
		Events:                s.events,
		Activity:              s.activity,
		Alerts:                s.alerts,
		RecordStoreWriteError: s.recordStoreWriteError,
		Stops:                 &s.stops,
		ShowSkipCache:         s.showSkipCache,
		StateFunc: func() *scheduler.LiveState {
			ls := s.state()
			return &scheduler.LiveState{
				Cfg:       ls.cfg,
				Engine:    ls.engine,
				Sonarr:    ls.sonarr,
				Radarr:    ls.radarr,
				Providers: ls.providers,
			}
		},
		ScanningFlag:        &s.scanning,
		DeleteSubtitleFiles: s.deleteSubtitleFiles,
	}
}

// runAuthCleanup runs periodic cleanup of pending auth ceremonies. Expired
// sessions and OIDC states are evicted by the auth store's built-in sweeper
// (internal/authstore/sweeper.go), and session-activity throttling state is
// pruned inside the library's session verifier, so this goroutine only
// handles the ceremony store.
func (s *Server) runAuthCleanup(ctx context.Context) {
	ticker := time.NewTicker(authCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.ceremonies.Cleanup()
		case <-ctx.Done():
			return
		}
	}
}

// activityPruneInterval is how often the activity retention ticker runs; a
// completed entry is removed within [PruneAge, PruneAge + this) of ending.
const activityPruneInterval = 60 * time.Second

// runActivityPrune drives activity retention on a ticker. The prune POLICY
// (what leaves, and the remove events it fires) stays on Log.PruneCompleted;
// this goroutine — the server's, on bgWg — is its one driver.
func (s *Server) runActivityPrune(ctx context.Context) {
	ticker := time.NewTicker(activityPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.activity.PruneCompleted(activity.DefaultPruneAge)
		case <-ctx.Done():
			return
		}
	}
}
