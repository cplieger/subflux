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
