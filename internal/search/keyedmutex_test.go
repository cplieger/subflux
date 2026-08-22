package search

import "testing"

// The gate's entry table is bounded by in-flight work rather than by library
// size: the last holder of a key drops it, so a scan that visits thousands of
// items in sequence leaves nothing behind, and a key can be taken again
// afterwards.
func TestMediaGate_forgets_a_key_once_its_holders_release(t *testing.T) {
	t.Parallel()
	const key = "movie:tt777"
	g := newMediaGate()

	unlock := g.lock(key)
	if n := len(g.locks); n != 1 {
		t.Fatalf("mediaGate.lock(%q): tracked keys = %d while held, want 1", key, n)
	}
	unlock()
	if n := len(g.locks); n != 0 {
		t.Errorf("mediaGate.lock(%q): tracked keys = %d after its only holder released, want 0",
			key, n)
	}

	unlock = g.lock(key)
	unlock()
	if n := len(g.locks); n != 0 {
		t.Errorf("mediaGate.lock(%q): tracked keys = %d after a second acquire/release, want 0",
			key, n)
	}
}
