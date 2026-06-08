package server

import (
	"context"
	"net/http"

	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/serveradapter"
)

// ScanHandlerClient is the narrow interface consumed by scan handlers.
// It documents the exact 3 methods scan_handlers.go calls on sonarr/radarr,
// enabling focused test fakes without implementing the full 14-method ArrClient.
type ScanHandlerClient = scanning.HandlerClient

// initScanHandler creates the scanning.Handler and stores it on the Server.
// Called during server construction after all deps are available.
func (s *Server) initScanHandler() *scanning.Handler {
	return scanning.NewHandler(scanning.HandlerDeps{
		StateFunc: func() *scanning.HandlerState {
			ls := s.state()
			var sonarr, radarr scanning.HandlerClient
			if ls.sonarr != nil {
				sonarr = ls.sonarr
			}
			if ls.radarr != nil {
				radarr = ls.radarr
			}
			return &scanning.HandlerState{
				Cfg:    ls.cfg,
				Engine: ls.engine,
				Sonarr: sonarr,
				Radarr: radarr,
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
