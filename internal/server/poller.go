package server

import (
	"context"

	"github.com/cplieger/subflux/internal/server/polling"
)

// runPoller delegates to the polling.Poller subsystem.
func (s *Server) runPoller(ctx context.Context) {
	s.poller.Run(ctx)
}

// pollerLiveState converts the server's liveState to polling.LiveState.
//
// Cfg is assigned only when the live config exists. That is not defensive
// noise: LiveState.Cfg is polling's own narrow interface, and assigning a nil
// *config.Config into an interface field yields a NON-nil interface value whose
// first method call panics. NewPoller runs at CONSTRUCTION — before any
// activation — and reads Cfg.PollInterval() behind an `ls.Cfg != nil` check, so
// laundering the nil here segfaults an unconfigured boot. The other liveState
// adapters need no such guard because their consumers run either behind
// requireConfigured (routes.go rule 4) or after the post-activation worker
// latch, where the config is non-nil by construction.
func (s *Server) pollerLiveState() *polling.LiveState {
	ls := s.state()
	pls := &polling.LiveState{
		Engine: ls.engine,
		Sonarr: ls.sonarr,
		Radarr: ls.radarr,
	}
	if ls.cfg != nil {
		pls.Cfg = ls.cfg
	}
	return pls
}
