package server

import (
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/events"
)

// sseClientCap resolves the configured SSE client cap, falling back to the
// default when cfg is nil (unconfigured mode) or the value is unset.
func sseClientCap(cfg *config.Config) int {
	if cfg != nil {
		if n := cfg.Search().MaxSSEClients; n > 0 {
			return n
		}
	}
	return events.DefaultMaxSSEClients
}
