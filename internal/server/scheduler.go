package server

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/server/scheduler"
	"github.com/cplieger/subflux/internal/server/serveradapter"
)

// authCleanupInterval is how often auth ceremonies and session debounce
// state are pruned.
const authCleanupInterval = scheduler.AuthCleanupInterval

// runScheduler runs the periodic scan and upgrade tickers until ctx is cancelled.
func (s *Server) runScheduler(ctx context.Context) {
	deps := s.schedulerDeps()
	scheduler.Run(ctx, deps)
}

// runFullScan delegates to the scheduler package's RunFullScan.
func (s *Server) runFullScan(ctx context.Context) {
	deps := s.schedulerDeps()
	scheduler.RunFullScan(ctx, deps)
}

// schedulerDeps builds the scheduler.Deps from Server fields.
func (s *Server) schedulerDeps() *scheduler.Deps {
	return &scheduler.Deps{
		DB:               s.db,
		Metrics:          s.metrics,
		ReconcileMetrics: s.metrics,
		Events:           &serveradapter.ScanEventAdapter{E: s.events},
		Activity:         &serveradapter.ActivityAdapter{A: s.activity},
		Alerts:           &serveradapter.AlertAdapter{A: s.alerts},
		ShowSkipCache:    s.showSkipCache,
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

// runAuthCleanup runs periodic cleanup of auth ceremonies and the session
// activity debouncer. Expired sessions and OIDC states are now evicted by
// the auth store's built-in sweeper (internal/authstore/sweeper.go), so
// this goroutine only handles the non-auth-store concerns.
func (s *Server) runAuthCleanup(ctx context.Context) {
	ticker := time.NewTicker(authCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.ceremonies.Cleanup()
			s.sessDebounce.prune(time.Now())
		case <-ctx.Done():
			return
		}
	}
}
