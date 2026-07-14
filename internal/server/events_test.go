package server

import (
	"testing"

	"github.com/cplieger/subflux/internal/server/events"
)

// The EventBus transport (fan-out, replay, eviction, caps) lives in
// github.com/cplieger/webhttp/sse and is tested there; the typed wrapper's
// wire contract is pinned in internal/server/events. These tests keep the
// server-level construction path honest.

func TestEventBusConstruction(t *testing.T) {
	t.Parallel()
	eb := events.New(0)
	if eb == nil {
		t.Fatal("events.New(0) returned nil")
	}
	if got := eb.ClientCount(); got != 0 {
		t.Errorf("ClientCount() = %d on a fresh bus, want 0", got)
	}
	// Publishing with no subscribers must be a safe no-op.
	eb.Publish(events.Event{Type: events.Notify, Data: events.NotifyEvent{
		Level: events.NotifyInfo, Text: "boot",
	}})
}
