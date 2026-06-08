package server

import (
	"net/http"

	"github.com/cplieger/subflux/internal/server/events"
)

// handleEvents delegates to events.HandleEvents with the server's config.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	maxClients := events.DefaultMaxSSEClients
	if st := s.state(); st != nil {
		if n := st.cfg.Search().MaxSSEClients; n > 0 {
			maxClients = n
		}
	}
	events.HandleEvents(s.events, maxClients, w, r)
}
