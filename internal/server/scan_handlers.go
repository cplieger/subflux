package server

import (
	"context"

	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/serveradapter"
)

// initScanHandler creates the scanning.Handler and stores it on the Server.
// Called during server construction after all deps are available. The server's
// api.SonarrClient/api.RadarrClient satisfy the scan handler's narrower
// per-role surfaces; a nil (unconfigured) client stays a nil interface.
func (s *Server) initScanHandler() *scanning.Handler {
	return scanning.NewHandler(scanning.HandlerDeps{
		// ONE s.state() read feeds both views: a scan operation's handler
		// state and engine state must come from the same generation (the
		// scanning handler snapshots this pair once per operation).
		StateFunc: func() (*scanning.HandlerState, *scanning.LiveState) {
			ls := s.state()
			return &scanning.HandlerState{
				Cfg:    ls.cfg,
				Engine: ls.engine,
				Sonarr: ls.sonarr,
				Radarr: ls.radarr,
			}, s.scanLiveState(ls)
		},
		CtxFunc:         func() context.Context { return s.ctx },
		ScanDeps:        s.scanDeps,
		Activity:        &serveradapter.ActivityAdapter{A: s.activity},
		Stops:           &s.stops,
		ScanGuard:       &s.scanGuard,
		Alerts:          &serveradapter.AlertAdapter{A: s.alerts},
		Events:          &serveradapter.ScanEventAdapter{E: s.events},
		InvalidateStats: func() { s.queryH.InvalidateStats() },
		BGTracker:       &s.bgWg,
	})
}
