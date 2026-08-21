// Scan orchestration logic moved to internal/server/scheduler/scheduler.go.
// The scanning.RunFullScan call is now made by the scheduler package.
// This file retains only the initScanHandler helper used by server.go.

package server

import (
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/scanning"
)

// scanDeps builds the scanning.Deps from Server fields.
// Used by initScanHandler for the HTTP scan handler.
func (s *Server) scanDeps() *scanning.Deps {
	if s.showSkipCache != nil {
		s.showSkipCache.Prune()
	}
	return &scanning.Deps{
		DB:            s.db,
		Backoff:       s.db,
		Metrics:       s.metrics,
		Events:        s.events,
		Activity:      s.activity,
		Alerts:        s.alerts,
		ShowSkipCache: s.showSkipCache,
		ClearCaches:   provider.ClearCaches,
	}
}

// scanLiveState converts the server's liveState to scanning.LiveState.
func scanLiveState(ls *liveState) *scanning.LiveState {
	return &scanning.LiveState{
		Cfg:         ls.cfg,
		Engine:      ls.engine,
		Sonarr:      ls.sonarr,
		Radarr:      ls.radarr,
		Providers:   ls.providers,
		ShowCounter: provider.ResolveShowCounter(ls.providers),
	}
}
