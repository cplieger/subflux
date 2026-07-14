package events

import (
	"net/http"
)

// DefaultMaxSSEClients is the upper bound on concurrent SSE connections
// when no config is loaded or the configured value is zero.
const DefaultMaxSSEClients = 32

// HandleEvents streams server-sent events to the browser. Admission — the
// client cap and the shutdown-drain refusal (both 503 with the standard
// webhttp error envelope) — is enforced by the sse hub atomically at
// subscribe time; the cap is set at construction and re-applied on config
// hot reload via EventBus.SetMaxClients. Headers, keepalives, Last-Event-ID
// replay, and frame encoding are the sse library's.
func HandleEvents(bus *EventBus, w http.ResponseWriter, r *http.Request) {
	bus.hub.Serve(w, r)
}
