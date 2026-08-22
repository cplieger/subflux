package server

import (
	"testing"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/events"
)

// The EventBus transport (fan-out, replay, eviction, caps) lives in
// github.com/cplieger/webhttp/v2/sse and is tested there; the typed wrapper's
// wire contract is pinned in internal/server/events. These tests keep the
// server-level construction path honest.

// TestSSEClientCap_resolves_the_configured_cap pins the projection the SSE
// hub is wired from: a configured positive cap is used as-is, and both
// "unconfigured mode" (nil config) and an explicit zero fall back to the
// package default rather than to a cap of zero.
func TestSSEClientCap_resolves_the_configured_cap(t *testing.T) {
	t.Parallel()
	const base = "sonarr:\n  url: \"http://s:8989\"\n  api_key: \"k\"\n" +
		"languages:\n  default:\n    - code: en\n"
	tests := []struct {
		name    string
		search  string
		nilConf bool
		want    int
	}{
		{name: "nil_config", nilConf: true, want: events.DefaultMaxSSEClients},
		{name: "configured_value", search: "search:\n  max_sse_clients: 7\n", want: 7},
		{name: "explicit_zero", search: "search:\n  max_sse_clients: 0\n", want: events.DefaultMaxSSEClients},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cfg *config.Config
			if !tt.nilConf {
				loaded, err := config.LoadFromBytes(t.Context(), []byte(base+tt.search))
				if err != nil {
					t.Fatalf("load config: %v", err)
				}
				cfg = loaded
			}
			if got := sseClientCap(cfg); got != tt.want {
				t.Errorf("sseClientCap(%q) = %d, want %d", tt.search, got, tt.want)
			}
		})
	}
}

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
