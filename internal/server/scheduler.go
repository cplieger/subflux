package server

import (
	"context"
	"log/slog"
	"time"

	"subflux/internal/server/scheduler"
	"subflux/internal/server/serveradapter"
)

// authCleanupInterval is how often expired sessions and stale auth state
// are purged from the database.
const authCleanupInterval = scheduler.AuthCleanupInterval

// oidcStateTTL is how long an OIDC authorization flow can remain pending.
const oidcStateTTL = scheduler.OIDCStateTTL

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
		DB:            s.db,
		Metrics:       s.metrics,
		Events:        &serveradapter.ScanEventAdapter{E: s.events},
		Activity:      &serveradapter.ActivityAdapter{A: s.activity},
		Alerts:        &serveradapter.AlertAdapter{A: s.alerts},
		ShowSkipCache: s.showSkipCache,
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

// runAuthCleanup runs periodic cleanup of auth ceremonies, expired sessions,
// and expired OIDC states.
func (s *Server) runAuthCleanup(ctx context.Context) {
	ticker := time.NewTicker(authCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.ceremonies.Cleanup()
			if s.authStore == nil {
				continue
			}
			cfg := s.state().cfg
			if cfg == nil {
				continue
			}
			if _, err := s.authStore.CleanupExpiredSessions(ctx, time.Now(),
				cfg.SessionIdleTimeout(), cfg.SessionAbsoluteTimeout()); err != nil {
				slog.Debug("session cleanup failed", "error", err)
			}
			if _, err := s.authStore.CleanupExpiredOIDCStates(ctx, time.Now(), oidcStateTTL); err != nil {
				slog.Debug("oidc state cleanup failed", "error", err)
			}
			s.sessDebounce.prune(time.Now())
		case <-ctx.Done():
			return
		}
	}
}
