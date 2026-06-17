package events

import "testing"

// Kills bus.go:74:16 ARITHMETIC_BASE and INVERT_NEGATIVES on
// "s.readAt = w - RingBufferSize".
//
// When a subscriber has fallen behind by more than the ring buffer, Next()
// resets its read cursor to w - RingBufferSize (the oldest still-buffered
// event) and then advances it by one. Subscribing at writeAt == 0 and then
// publishing 300 events makes w == 300, so the correct cursor lands at
// 300 - 256 + 1 == 45. Any mutation of the "w - RingBufferSize" expression
// (e.g. -> w + RingBufferSize) moves the cursor off 45 and past writeAt, which
// also makes a subsequent Next() wrongly report "caught up".
func Test_gk_subflux_u30_NextSkipToLatestCursor(t *testing.T) {
	eb := New()
	sub, unsub := eb.Subscribe() // readAt = writeAt = 0
	defer unsub()

	for range 300 {
		eb.Publish(Event{})
	}

	if _, ok := sub.Next(); !ok {
		t.Fatalf("first Next() ok = false, want true")
	}
	if sub.readAt != 45 {
		t.Errorf("after skip-to-latest, sub.readAt = %d, want 45 (w=300; w-RingBufferSize+1)", sub.readAt)
	}
	if _, ok := sub.Next(); !ok {
		t.Errorf("second Next() ok = false, want true (cursor must still trail writeAt after a correct skip)")
	}
}
