package server

import (
	"testing"

	"subflux/internal/server/events"
)

// --- EventBus.Subscribe ---

func TestEventBus_Subscribe_returns_sub_and_unsub(t *testing.T) {
	t.Parallel()
	eb := events.New()

	sub, unsub := eb.Subscribe()
	if sub == nil {
		t.Fatal("Subscribe() returned nil subscription")
	}
	if unsub == nil {
		t.Fatal("Subscribe() returned nil unsub func")
	}

	if eb.ClientCount() != 1 {
		t.Errorf("ClientCount() = %d after Subscribe, want 1", eb.ClientCount())
	}

	unsub()

	if eb.ClientCount() != 0 {
		t.Errorf("ClientCount() = %d after unsub, want 0", eb.ClientCount())
	}
}

func TestEventBus_Subscribe_multiple_clients(t *testing.T) {
	t.Parallel()
	eb := events.New()

	_, unsub1 := eb.Subscribe()
	_, unsub2 := eb.Subscribe()
	_, unsub3 := eb.Subscribe()

	if eb.ClientCount() != 3 {
		t.Errorf("ClientCount() = %d, want 3", eb.ClientCount())
	}

	unsub2()
	if eb.ClientCount() != 2 {
		t.Errorf("ClientCount() = %d after unsub2, want 2", eb.ClientCount())
	}

	unsub1()
	unsub3()
	if eb.ClientCount() != 0 {
		t.Errorf("ClientCount() = %d after all unsub, want 0", eb.ClientCount())
	}
}

// --- EventBus.Publish ---

func TestEventBus_Publish_delivers_to_subscriber(t *testing.T) {
	t.Parallel()
	eb := events.New()

	sub, unsub := eb.Subscribe()
	defer unsub()

	evt := events.Event{Type: events.CoverageUpdate, Data: events.CoverageEvent{MediaType: "episode", MediaID: "test"}}
	eb.Publish(evt)

	got, ok := sub.Next()
	if !ok {
		t.Fatal("expected event from ring buffer, got nothing")
	}
	if got.Type != events.CoverageUpdate {
		t.Errorf("received event type = %q, want %q", got.Type, events.CoverageUpdate)
	}
}

func TestEventBus_Publish_delivers_to_all_subscribers(t *testing.T) {
	t.Parallel()
	eb := events.New()

	sub1, unsub1 := eb.Subscribe()
	defer unsub1()
	sub2, unsub2 := eb.Subscribe()
	defer unsub2()

	evt := events.Event{Type: "scan:start"}
	eb.Publish(evt)

	for i, sub := range []*events.Subscription{sub1, sub2} {
		got, ok := sub.Next()
		if !ok {
			t.Errorf("subscriber %d: expected event, got nothing", i)
			continue
		}
		if got.Type != "scan:start" {
			t.Errorf("subscriber %d: type = %q, want %q", i, got.Type, "scan:start")
		}
	}
}

func TestEventBus_Publish_no_subscribers_does_not_panic(t *testing.T) {
	t.Parallel()
	eb := events.New()
	eb.Publish(events.Event{Type: "activity"})
}

func TestEventBus_Publish_slow_client_skips_old_events(t *testing.T) {
	t.Parallel()
	eb := events.New()

	sub, unsub := eb.Subscribe()
	defer unsub()

	// Overflow the ring buffer (256 slots).
	for i := range events.RingBufferSize + 10 {
		_ = i
		eb.Publish(events.Event{Type: "scan:progress", Data: events.ScanEvent{Action: "test"}})
	}

	// Subscriber should skip to recent events, not block.
	got, ok := sub.Next()
	if !ok {
		t.Fatal("expected event after overflow, got nothing")
	}
	// The first readable event should be from the overflow region.
	if _, ok := got.Data.(events.ScanEvent); !ok {
		t.Errorf("first event data type = %T, want events.ScanEvent", got.Data)
	}
}

// --- EventBus.ClientCount ---

func TestEventBus_ClientCount_zero_initially(t *testing.T) {
	t.Parallel()
	eb := events.New()
	if eb.ClientCount() != 0 {
		t.Errorf("ClientCount() = %d, want 0", eb.ClientCount())
	}
}

// --- unsub idempotency ---

func TestEventBus_Unsub_idempotent(t *testing.T) {
	t.Parallel()
	eb := events.New()

	_, unsub := eb.Subscribe()
	unsub()
	unsub() // must not panic (double close guarded by sync.Once)

	if eb.ClientCount() != 0 {
		t.Errorf("ClientCount() = %d after double unsub, want 0", eb.ClientCount())
	}
}
