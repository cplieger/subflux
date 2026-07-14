package server

import (
	"net/http"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/events"
)

// handleEvents delegates to events.HandleEvents. The client cap lives on the
// hub itself (set at construction, re-applied by hot reload via
// sseClientCap), so no per-request config read is needed here.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	events.HandleEvents(s.events, w, r)
}

// sseClientCap resolves the configured SSE client cap, falling back to the
// default when cfg is nil (unconfigured mode) or the value is unset.
func sseClientCap(cfg api.ConfigProvider) int {
	if cfg != nil {
		if n := cfg.Search().MaxSSEClients; n > 0 {
			return n
		}
	}
	return events.DefaultMaxSSEClients
}
