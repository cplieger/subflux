package events

import "testing"

func FuzzEventBusPublishSubscribe(f *testing.F) {
	f.Add("coverage_update", "media-1", uint8(3))
	f.Add("", "", uint8(0))
	f.Add("scan_complete", "tvdb-123-s01e01", uint8(10))

	f.Fuzz(func(t *testing.T, evType string, text string, count uint8) {
		eb := New()
		sub, unsub := eb.Subscribe()
		defer unsub()

		n := int(count) % 32
		for range n {
			eb.Publish(Event{Type: EventType(evType), Data: NotifyEvent{Level: NotifyInfo, Text: text}})
		}

		received := 0
		for {
			_, ok := sub.Next()
			if !ok {
				break
			}
			received++
		}

		if received != n {
			t.Errorf("received %d events, published %d", received, n)
		}
	})
}

func FuzzEventBusRingOverflow(f *testing.F) {
	f.Add(uint16(300))
	f.Add(uint16(256))
	f.Add(uint16(0))

	f.Fuzz(func(t *testing.T, count uint16) {
		eb := New()
		sub, unsub := eb.Subscribe()
		defer unsub()

		n := int(count)
		for i := range n {
			eb.Publish(Event{Type: Notify, Data: NotifyEvent{Text: string(rune(i % 128))}})
		}

		received := 0
		for {
			_, ok := sub.Next()
			if !ok {
				break
			}
			received++
		}

		// Subscriber can't receive more than RingBufferSize if it fell behind.
		expected := n
		if expected > RingBufferSize {
			expected = RingBufferSize
		}
		if received != expected {
			t.Errorf("received %d, expected %d (published %d)", received, expected, n)
		}
	})
}
