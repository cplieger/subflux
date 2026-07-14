// Package events is subflux's typed server-sent-events layer: the sealed
// Event/EventData types the app publishes, marshaled onto the shared
// webhttp/sse broadcast hub. The transport (fan-out, replay ring with
// Last-Event-ID resume, keepalives, proxy-defensive headers, slow-client
// eviction) is the library's; this package owns only the subflux event
// vocabulary and its wire encoding.
package events

import (
	"encoding/json"
	"log/slog"

	"github.com/cplieger/webhttp/sse"
)

// EventBus publishes subflux's typed events to connected SSE clients.
// A nil *EventBus is safe to publish to (no-op), so optional wiring needs
// no guards.
type EventBus struct {
	hub *sse.Hub
}

// New creates the event bus with the given concurrent-client cap (<= 0 means
// DefaultMaxSSEClients). The underlying hub keeps a replay ring, so a browser
// that reconnects after a transient drop resumes via the standard
// Last-Event-ID header instead of silently missing events. The cap is
// enforced by the hub atomically at admission; SetMaxClients re-applies a
// hot-reloaded value without rebuilding the hub.
func New(maxClients int) *EventBus {
	if maxClients <= 0 {
		maxClients = DefaultMaxSSEClients
	}
	return &EventBus{hub: sse.NewHub(sse.WithMaxClients(maxClients))}
}

// SetMaxClients applies a new client cap (<= 0 means DefaultMaxSSEClients) to
// the running hub — used by config hot reload. Existing connections above a
// lowered cap are not evicted. No-op when the bus is nil.
func (eb *EventBus) SetMaxClients(n int) {
	if eb == nil {
		return
	}
	if n <= 0 {
		n = DefaultMaxSSEClients
	}
	eb.hub.SetMaxClients(n)
}

// Shutdown drains the hub: every connected stream is cancelled and later
// connection attempts are refused with 503, so graceful shutdown is not held
// open by long-lived SSE requests. No-op when the bus is nil.
func (eb *EventBus) Shutdown() {
	if eb == nil {
		return
	}
	eb.hub.Shutdown()
}

// Publish broadcasts an event to every connected client. The wire payload
// is the JSON-encoded Event (type + data), sent as a NAMED SSE event
// (`event: <type>`) so the browser dispatches it to the matching
// addEventListener handler. No-op when the bus is nil.
func (eb *EventBus) Publish(e Event) {
	if eb == nil {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		slog.Warn("SSE: failed to marshal event", "type", e.Type, "error", err)
		return
	}
	eb.hub.Publish(sse.Event{Name: string(e.Type), Data: data})
}

// ClientCount returns the number of connected SSE clients.
func (eb *EventBus) ClientCount() int {
	return eb.hub.ClientCount()
}
