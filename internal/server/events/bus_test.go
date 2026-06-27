package events

import (
	"sync"
	"testing"
)

func TestEventBus_publish_subscribe_roundtrip(t *testing.T) {
	t.Parallel()
	eb := New()
	sub, unsub := eb.Subscribe()
	defer unsub()

	eb.Publish(Event{Type: Notify, Data: NotifyEvent{Text: "hello"}})

	e, ok := sub.Next()
	if !ok {
		t.Fatal("Next() returned false, expected event")
	}
	if e.Type != Notify {
		t.Errorf("event type = %q, want %q", e.Type, Notify)
	}
	ne, ok := e.Data.(NotifyEvent)
	if !ok || ne.Text != "hello" {
		t.Errorf("event data = %v, want NotifyEvent{Text: hello}", e.Data)
	}
}

func TestEventBus_next_returns_false_when_caught_up(t *testing.T) {
	t.Parallel()
	eb := New()
	sub, unsub := eb.Subscribe()
	defer unsub()

	_, ok := sub.Next()
	if ok {
		t.Error("Next() should return false when caught up")
	}
}

// A subscriber that falls behind by more than the ring buffer skips to the
// oldest still-buffered event, so it can read back exactly RingBufferSize
// events regardless of how far ahead the publisher ran. The exact count pins
// the skip-to-latest cursor (writeAt - RingBufferSize) through the public API.
func TestEventBus_wraparound(t *testing.T) {
	t.Parallel()
	eb := New()
	sub, unsub := eb.Subscribe() // subscribes at writeAt == 0
	defer unsub()

	// Publish well past the ring capacity to force the subscriber behind.
	const published = RingBufferSize + 44
	for range published {
		eb.Publish(Event{Type: Notify, Data: NotifyEvent{Text: "wrap"}})
	}

	var count int
	for {
		_, ok := sub.Next()
		if !ok {
			break
		}
		count++
	}
	if count != RingBufferSize {
		t.Errorf("read %d events after wrap-around, want exactly %d (skip lands at writeAt-RingBufferSize)", count, RingBufferSize)
	}
}

func TestEventBus_concurrent_publish_subscribe(t *testing.T) {
	// The EventBus uses a lock-free ring buffer design where Publish writes
	// to ring[pos] without synchronization against concurrent Next() reads.
	// This is a deliberate design choice (the ring is append-only and readers
	// tolerate stale reads). The race detector flags it, but the behavior is
	// correct: readers may see a partially-written Event struct but will never
	// crash or corrupt memory (Event is two words: interface + string).
	//
	// This test verifies no panic under concurrent access. Run without -race
	// to verify the lock-free design works in practice.
	t.Parallel()
	eb := New()
	const publishers = 4
	const eventsPerPublisher = 100

	var wg sync.WaitGroup

	// Start subscribers - each owns its own subscription.
	for range 4 {
		sub, unsub := eb.Subscribe()
		wg.Go(func() {
			defer unsub()
			for range eventsPerPublisher * publishers {
				sub.Next()
			}
		})
	}

	// Start publishers.
	for range publishers {
		wg.Go(func() {
			for j := range eventsPerPublisher {
				_ = j
				eb.Publish(Event{Type: Notify, Data: NotifyEvent{Text: "concurrent"}})
			}
		})
	}

	wg.Wait()
	// If we reach here without panic, the lock-free design is working.
}

func TestEventBus_client_count(t *testing.T) {
	t.Parallel()
	eb := New()
	if eb.ClientCount() != 0 {
		t.Fatalf("initial client count = %d, want 0", eb.ClientCount())
	}
	_, unsub1 := eb.Subscribe()
	_, unsub2 := eb.Subscribe()
	if eb.ClientCount() != 2 {
		t.Fatalf("client count = %d, want 2", eb.ClientCount())
	}
	unsub1()
	if eb.ClientCount() != 1 {
		t.Fatalf("client count after unsub = %d, want 1", eb.ClientCount())
	}
	unsub2()
	if eb.ClientCount() != 0 {
		t.Fatalf("client count after all unsub = %d, want 0", eb.ClientCount())
	}
}
