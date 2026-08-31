package providerhealth

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

func clockAt(t time.Time) *func() time.Time {
	fn := func() time.Time { return t }
	return &fn
}

func newTestTracker(threshold int, window, cooldown time.Duration, clock *func() time.Time) *Tracker {
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
// public API, so they seed the tracker's failure map directly.

// newSeedableTracker returns a tracker pinned to a fixed clock so a test can
// pre-seed the unexported failure map to set up a precise capacity scenario.
func newSeedableTracker(threshold int, window, cooldown time.Duration, now time.Time) *Tracker {
	return New(Config{
		Threshold: threshold,
		Window:    window,
		Cooldown:  cooldown,
		Now:       func() time.Time { return now },
	})
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

// ---- OnChange transitions (provider status events, E1) ----

type trackedChange struct {
	status subflux.ProviderStatus
	id     subflux.ProviderID
	raised bool
}

// The trip transition raises exactly once — further failures while tripped
// re-raise nothing — with the under-lock trip snapshot, fired after unlock
// (the hook re-enters the tracker).
func TestProviderTimeout_onChange_raises_once_on_trip(t *testing.T) {
	t.Parallel()
	clock := clockAt(time.Now())
	it := newTestTracker(3, 10*time.Minute, time.Hour, clock)
	var changes []trackedChange
	it.SetOnChange(func(id subflux.ProviderID, status subflux.ProviderStatus, raised bool) {
		_ = it.Status() // re-entry: deadlocks if fired under the tracker's lock
		changes = append(changes, trackedChange{id: id, status: status, raised: raised})
	})

	boom := errors.New("boom")
	for range 5 {
		it.RecordFailure("p", boom)
	}

	if len(changes) != 1 {
		t.Fatalf("onChange fired %d times across 5 failures, want 1 (the trip)", len(changes))
	}
	c := changes[0]
	if c.id != "p" || !c.raised {
		t.Fatalf("trip change = id %q raised %v, want p raised", c.id, c.raised)
	}
	if !c.status.TimedOut || c.status.CooldownRemaining != time.Hour ||
		c.status.RecentFailures != 3 || c.status.Threshold != 3 || c.status.LastError != "boom" {
		t.Errorf("trip snapshot = %+v, want timed_out with full cooldown, 3/3 failures, last error", c.status)
	}
}

// Observing cooldown expiry through IsTimedOut clears the timeout.
func TestProviderTimeout_onChange_clears_on_expiry(t *testing.T) {
	t.Parallel()
	start := time.Now()
	clock := clockAt(start)
	it := newTestTracker(2, 10*time.Minute, time.Hour, clock)
	var changes []trackedChange
	it.SetOnChange(func(id subflux.ProviderID, status subflux.ProviderStatus, raised bool) {
		changes = append(changes, trackedChange{id: id, status: status, raised: raised})
	})

	it.RecordFailure("p", nil)
	it.RecordFailure("p", nil)
	*clock = func() time.Time { return start.Add(time.Hour + time.Second) }
	if it.IsTimedOut("p") {
		t.Fatal("IsTimedOut past the cooldown = true, want expired")
	}

	if len(changes) != 2 {
		t.Fatalf("onChange fired %d times for trip+expiry, want 2", len(changes))
	}
	if changes[1].raised || changes[1].id != "p" {
		t.Errorf("expiry change = id %q raised %v, want p cleared", changes[1].id, changes[1].raised)
	}
	if changes[1].status.TimedOut {
		t.Errorf("expiry snapshot still timed out: %+v", changes[1].status)
	}
}

// A success clears a TRIPPED provider (and only a tripped one: successes on
// a healthy provider publish nothing).
func TestProviderTimeout_onChange_clears_on_success_only_when_tripped(t *testing.T) {
	t.Parallel()
	clock := clockAt(time.Now())
	it := newTestTracker(2, 10*time.Minute, time.Hour, clock)
	var changes []trackedChange
	it.SetOnChange(func(id subflux.ProviderID, status subflux.ProviderStatus, raised bool) {
		changes = append(changes, trackedChange{id: id, status: status, raised: raised})
	})

	it.RecordSuccess("p") // healthy: no transition
	it.RecordFailure("p", nil)
	it.RecordSuccess("p") // failures but not tripped: no transition
	if len(changes) != 0 {
		t.Fatalf("onChange fired %d times without a trip, want 0", len(changes))
	}

	it.RecordFailure("p", nil)
	it.RecordFailure("p", nil) // trips
	it.RecordSuccess("p")      // clears

	if len(changes) != 2 {
		t.Fatalf("onChange fired %d times for trip+success-clear, want 2", len(changes))
	}
	if changes[1].raised {
		t.Errorf("success change raised = true, want cleared")
	}
	if it.IsTimedOut("p") {
		t.Error("provider still timed out after RecordSuccess")
	}
}

// The operator reset clears every tripped provider, one transition each;
// untripped providers with mere failure history publish nothing.
func TestProviderTimeout_onChange_reset_clears_each_tripped(t *testing.T) {
	t.Parallel()
	clock := clockAt(time.Now())
	it := newTestTracker(2, 10*time.Minute, time.Hour, clock)
	var cleared []subflux.ProviderID
	it.SetOnChange(func(id subflux.ProviderID, _ subflux.ProviderStatus, raised bool) {
		if !raised {
			cleared = append(cleared, id)
		}
	})

	for _, p := range []subflux.ProviderID{"a", "b"} {
		it.RecordFailure(p, nil)
		it.RecordFailure(p, nil)
	}
	it.RecordFailure("healthy-ish", nil) // history, no trip

	it.Reset()

	slices.Sort(cleared)
	if !slices.Equal(cleared, []subflux.ProviderID{"a", "b"}) {
		t.Errorf("reset cleared %v, want [a b] (one clear per tripped provider, none for untripped)", cleared)
	}
}
