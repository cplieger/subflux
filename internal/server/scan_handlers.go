package server

import (
	"context"
	"net/http"

	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/serveradapter"
)

// initScanHandler creates the scanning.Handler and stores it on the Server.
// Called during server construction after all deps are available. The server's
// api.SonarrClient/api.RadarrClient satisfy the scan handler's narrower
// per-role surfaces; a nil (unconfigured) client stays a nil interface.
func (s *Server) initScanHandler() *scanning.Handler {
	return scanning.NewHandler(scanning.HandlerDeps{
		StateFunc: func() *scanning.HandlerState {
			ls := s.state()
			return &scanning.HandlerState{
				Cfg:    ls.cfg,
				Engine: ls.engine,
				Sonarr: ls.sonarr,
				Radarr: ls.radarr,
			}
		},
		CtxFunc:  func() context.Context { return s.ctx },
		ScanDeps: s.scanDeps,
		ScanLiveStateFunc: func() *scanning.LiveState {
			return s.scanLiveState(s.state())
		},
		Activity:        &serveradapter.ActivityAdapter{A: s.activity},
		ScanGuard:       &s.scanGuard,
		Alerts:          &serveradapter.AlertAdapter{A: s.alerts},
		Events:          &serveradapter.ScanEventAdapter{E: s.events},
		InvalidateStats: func() { s.queryH.InvalidateStats() },
		BGTracker:       &s.bgWg,
	})
}

// handleScanSeries delegates to the scanning.Handler.
func (s *Server) handleScanSeries(w http.ResponseWriter, r *http.Request) {
	s.scanH.HandleScanSeries(w, r)
}

// handleScanSeason delegates to the scanning.Handler.
func (s *Server) handleScanSeason(w http.ResponseWriter, r *http.Request) {
	s.scanH.HandleScanSeason(w, r)
}

// handleScanItem delegates to the scanning.Handler.
func (s *Server) handleScanItem(w http.ResponseWriter, r *http.Request) {
	s.scanH.HandleScanItem(w, r)
}

// handleScanMovie delegates to the scanning.Handler.
func (s *Server) handleScanMovie(w http.ResponseWriter, r *http.Request) {
	s.scanH.HandleScanMovie(w, r)
}
