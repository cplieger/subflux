package timeout

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func clockAt(t time.Time) *func() time.Time {
	fn := func() time.Time { return t }
	return &fn
}

func newTestTracker(threshold int, window, cooldown time.Duration, clock *func() time.Time) ProviderHealth {
	return New(Config{
		Threshold: threshold,
		Window:    window,
		Cooldown:  cooldown,
		Now:       func() time.Time { return (*clock)() },
	})
}

// --- Defaults ---

func TestProviderTimeout_defaults(t *testing.T) {
	t.Parallel()
	it := New(Config{})
	for range 4 {
		it.RecordFailure("p", nil)
	}
	if it.IsTimedOut("p") {
		t.Error("timed out after 4 failures, want active (default threshold=5)")
	}
	it.RecordFailure("p", nil)
	if !it.IsTimedOut("p") {
		t.Error("not timed out after 5 failures, want timed out (default threshold=5)")
	}
}

func TestProviderTimeout_negative_config_gets_defaults(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: -1, Window: -time.Second, Cooldown: -time.Minute})
	for range 4 {
		it.RecordFailure("p", nil)
	}
	if it.IsTimedOut("p") {
		t.Error("timed out after 4 failures with negative config, want active (default threshold=5)")
	}
	it.RecordFailure("p", nil)
	if !it.IsTimedOut("p") {
		t.Error("not timed out after 5 failures with negative config, want timed out")
	}
}

func TestProviderTimeout_threshold_one_is_not_defaulted(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 1, Window: time.Minute, Cooldown: time.Minute})
	it.RecordFailure("p", nil)
	if !it.IsTimedOut("p") {
		t.Error("not timed out after 1 failure, want timed out (threshold=1)")
	}
}

// --- State Transitions ---

func TestProviderTimeout_stays_active_below_threshold(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 3, Window: time.Hour, Cooldown: time.Hour})
	it.RecordFailure("prov1", nil)
	it.RecordFailure("prov1", nil)
	if it.IsTimedOut("prov1") {
		t.Error("timed out after 2 failures, want active (threshold=3)")
	}
}

func TestProviderTimeout_triggers_at_threshold(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 3, Window: time.Hour, Cooldown: time.Hour})
	for range 3 {
		it.RecordFailure("prov1", nil)
	}
	if !it.IsTimedOut("prov1") {
		t.Error("not timed out after 3 failures, want timed out (threshold=3)")
	}
}

// --- Window and Cooldown ---

func TestProviderTimeout_cooldown_expires(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := clockAt(now)
	it := newTestTracker(2, time.Hour, 5*time.Minute, clock)

	it.RecordFailure("prov1", nil)
	it.RecordFailure("prov1", nil)
	if !it.IsTimedOut("prov1") {
		t.Fatal("should be timed out")
	}

	*clock = func() time.Time { return now.Add(5*time.Minute - time.Nanosecond) }
	if !it.IsTimedOut("prov1") {
		t.Error("should still be timed out 1ns before cooldown boundary")
	}

	*clock = func() time.Time { return now.Add(5 * time.Minute) }
	if it.IsTimedOut("prov1") {
		t.Error("should no longer be timed out after cooldown")
	}
}

func TestProviderTimeout_window_expiry_drops_old_failures(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := clockAt(now)
	it := newTestTracker(3, 10*time.Minute, time.Hour, clock)

	it.RecordFailure("p", nil)
	it.RecordFailure("p", nil)

	*clock = func() time.Time { return now.Add(11 * time.Minute) }
	it.RecordFailure("p", nil)
	if it.IsTimedOut("p") {
		t.Error("should not be timed out: first 2 failures expired outside window")
	}
}

// --- Success resets ---

func TestProviderTimeout_success_clears_state(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 2, Window: time.Hour, Cooldown: time.Hour})
	it.RecordFailure("p", nil)
	it.RecordFailure("p", nil)
	if !it.IsTimedOut("p") {
		t.Fatal("should be timed out")
	}
	it.RecordSuccess("p")
	if it.IsTimedOut("p") {
		t.Error("should not be timed out after success")
	}
}

// --- Reset ---

func TestProviderTimeout_reset_clears_all(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 2, Window: time.Hour, Cooldown: time.Hour})
	it.RecordFailure("a", nil)
	it.RecordFailure("a", nil)
	it.RecordFailure("b", nil)
	it.RecordFailure("b", nil)
	it.Reset()
	if it.IsTimedOut("a") || it.IsTimedOut("b") {
		t.Error("should not be timed out after reset")
	}
}

// --- Status ---

func TestProviderTimeout_status_reports_timed_out(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 2, Window: time.Hour, Cooldown: time.Hour})
	it.RecordFailure("p", errors.New("oops"))
	it.RecordFailure("p", errors.New("oops"))
	status := it.Status()
	s, ok := status["p"]
	if !ok {
		t.Fatal("expected status entry for p")
	}
	if !s.TimedOut {
		t.Error("status should report timed out")
	}
	if s.LastError != "oops" {
		t.Errorf("last error = %q, want %q", s.LastError, "oops")
	}
}

// --- Concurrency ---

func TestProviderTimeout_concurrent_access(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 5, Window: time.Hour, Cooldown: time.Hour})
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			it.RecordFailure("p", errors.New("err"))
			it.IsTimedOut("p")
			it.Status()
		})
	}
	wg.Wait()
}

// --- Isolation ---

func TestProviderTimeout_providers_are_independent(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 2, Window: time.Hour, Cooldown: time.Hour})
	it.RecordFailure("a", nil)
	it.RecordFailure("a", nil)
	if !it.IsTimedOut("a") {
		t.Fatal("a should be timed out")
	}
	if it.IsTimedOut("b") {
		t.Error("b should not be timed out")
	}
}

// --- Status: field-level reporting ---

func TestProviderTimeout_status_counts_in_window_failures(t *testing.T) {
	t.Parallel()
	it := New(Config{Threshold: 5, Window: time.Hour, Cooldown: time.Hour})
	it.RecordFailure("p", nil)
	it.RecordFailure("p", nil)
	s, ok := it.Status()["p"]
	if !ok {
		t.Fatal("expected status entry for p")
	}
	if s.RecentFailures != 2 {
		t.Errorf("RecentFailures = %d, want 2", s.RecentFailures)
	}
	if s.TimedOut {
		t.Error("should not be timed out below threshold")
	}
}

func TestProviderTimeout_status_reports_cooldown_remaining(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := clockAt(now)
	// threshold=1 trips on the first failure; a long window keeps that failure
	// in count while the clock advances inside the cooldown.
	it := newTestTracker(1, 24*time.Hour, time.Hour, clock)
	it.RecordFailure("p", nil)

	*clock = func() time.Time { return now.Add(10 * time.Minute) }
	s := it.Status()["p"]
	if !s.TimedOut {
		t.Error("should be timed out within cooldown")
	}
	if s.CooldownRemaining != 50*time.Minute {
		t.Errorf("CooldownRemaining = %v, want %v", s.CooldownRemaining, 50*time.Minute)
	}
}

func TestProviderTimeout_status_not_timed_out_at_cooldown_expiry(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := clockAt(now)
	it := newTestTracker(1, 24*time.Hour, time.Hour, clock)
	it.RecordFailure("p", nil)

	// Exactly one cooldown later: remaining == 0, which is not "> 0".
	*clock = func() time.Time { return now.Add(time.Hour) }
	s, ok := it.Status()["p"]
	if !ok {
		t.Fatal("expected status entry for p (failure still within window)")
	}
	if s.TimedOut {
		t.Error("should not be timed out at exactly cooldown expiry (remaining == 0)")
	}
}

// --- RecordFailure: failure-slice capacity management ---
//
// RecordFailure reuses a provider's backing slice and only reallocates it
// smaller when it has grown well past need (cap strictly over 2*threshold) yet
// holds fewer than threshold live failures. These two tests pin the boundaries
// where that shrink must NOT happen. Capacity is not observable through the
// public API, so they seed the concrete tracker directly.

// newSeedableTracker returns the concrete *tracker so a test can pre-seed the
// unexported failure map to set up a precise capacity scenario.
func newSeedableTracker(threshold int, window, cooldown time.Duration, now time.Time) *tracker {
	return New(Config{
		Threshold: threshold,
		Window:    window,
		Cooldown:  cooldown,
		Now:       func() time.Time { return now },
	}).(*tracker)
}

func TestProviderTimeout_RecordFailure_keeps_capacity_when_cap_equals_twice_threshold(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tr := newSeedableTracker(4, time.Hour, time.Hour, now)
	// cap == 2*threshold (8), not strictly greater, so appending one failure
	// (len 1 < threshold) must leave the capacity untouched.
	tr.failures["pA"] = make([]time.Time, 0, 8)

	tr.RecordFailure("pA", nil)

	if got := cap(tr.failures["pA"]); got != 8 {
		t.Errorf("cap(failures[pA]) = %d, want 8 (no shrink at cap boundary)", got)
	}
}

func TestProviderTimeout_RecordFailure_keeps_capacity_when_live_count_equals_threshold(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tr := newSeedableTracker(4, time.Hour, time.Hour, now)
	// cap 16 (> 2*threshold) but the new failure brings the live count to
	// exactly threshold (4), not below it, so no shrink happens.
	seed := make([]time.Time, 3, 16)
	seed[0], seed[1], seed[2] = now, now, now
	tr.failures["pB"] = seed

	tr.RecordFailure("pB", nil)

	if got := cap(tr.failures["pB"]); got != 16 {
		t.Errorf("cap(failures[pB]) = %d, want 16 (no shrink at length boundary)", got)
	}
}
