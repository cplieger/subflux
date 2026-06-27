package api

import "testing"

// TestLogOnce_first_true_once_per_key verifies the core dedup contract: the
// first sighting of a key returns true, every later sighting returns false,
// and distinct keys are tracked independently.
func TestLogOnce_first_true_once_per_key(t *testing.T) {
	t.Parallel()

	l := newLogOnce(8)
	if !l.first("a") {
		t.Error(`first("a") = false on first sighting, want true`)
	}
	if l.first("a") {
		t.Error(`first("a") = true on second sighting, want false`)
	}
	if !l.first("b") {
		t.Error(`first("b") = false on first sighting, want true`)
	}
}

// TestLogOnce_zero_capacity_never_records verifies a zero-capacity set records
// nothing: the first key is already at capacity, so first() returns false
// without ever growing the map.
func TestLogOnce_zero_capacity_never_records(t *testing.T) {
	t.Parallel()

	l := newLogOnce(0)
	if l.first("a") {
		t.Error(`first("a") = true at capacity 0, want false`)
	}
	if l.first("b") {
		t.Error(`first("b") = true at capacity 0, want false`)
	}
}

// TestLogOnce_full_capacity_rejects_new_keys exercises the capacity-full path
// the prior fuzz target never reached: distinct keys up to the capacity are
// recorded (true), the first key beyond capacity is rejected (false) rather
// than growing the set, and a key recorded before the set filled still
// returns false.
func TestLogOnce_full_capacity_rejects_new_keys(t *testing.T) {
	t.Parallel()

	l := newLogOnce(2)

	// Two distinct keys fill the set exactly to capacity; both are first
	// sightings and are recorded.
	if !l.first("k0") {
		t.Error(`first("k0") = false, want true (slot 1 of 2)`)
	}
	if !l.first("k1") {
		t.Error(`first("k1") = false, want true (slot 2 of 2)`)
	}

	// Capacity is now full: a brand-new key must be rejected, not grow the set.
	if l.first("overflow") {
		t.Error(`first("overflow") = true past capacity, want false`)
	}

	// A key recorded before the set filled still returns false on repeat.
	if l.first("k0") {
		t.Error(`first("k0") = true on repeat, want false`)
	}
}
