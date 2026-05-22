package server

import (
	"context"

	"subflux/internal/server/polling"
)

// runPoller delegates to the polling.Poller subsystem.
func (s *Server) runPoller(ctx context.Context) {
	s.poller.Run(ctx)
}

// pollerLiveState converts the server's liveState to polling.LiveState.
func (s *Server) pollerLiveState() *polling.LiveState {
	ls := s.state()
	return &polling.LiveState{
		Cfg:    ls.cfg,
		Engine: ls.engine,
		Sonarr: ls.sonarr,
		Radarr: ls.radarr,
	}
}
