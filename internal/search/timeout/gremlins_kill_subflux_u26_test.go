package timeout

// Unit subflux-u26: tests added to kill surviving gremlins mutation-testing
// mutants in timeout.go. All package-level identifiers are prefixed with
// gk_subflux_u26_ to avoid colliding with any sibling unit sharing this
// package. Internal test package so unexported tracker fields are reachable.

import (
	"testing"
	"time"
)

// gk_subflux_u26_ref is a fixed clock instant used by every test below so that
// window/cooldown arithmetic is fully deterministic.
var gk_subflux_u26_ref = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// gk_subflux_u26_fixedTracker builds a tracker with a frozen clock and returns
// the concrete *tracker so tests can pre-seed the unexported failure/trip maps.
func gk_subflux_u26_fixedTracker(threshold int, window, cooldown time.Duration, now time.Time) *tracker {
	return New(Config{
		Threshold: threshold,
		Window:    window,
		Cooldown:  cooldown,
		Now:       func() time.Time { return now },
	}).(*tracker)
}

// Kills timeout.go:124:17 (CONDITIONALS_NEGATION `>`->`<=`, CONDITIONALS_BOUNDARY
// `>`->`>=`) and 124:20 (ARITHMETIC_BASE `*`->`/`) on the shrink gate
// `cap(pruned) > 2*it.threshold && len(pruned) < it.threshold`.
//
// With threshold=4 the backing slice has cap exactly 8 == 2*threshold and the
// pruned length is 1 < threshold. The original `cap(8) > 8` is false, so the
// shrink block is skipped and capacity stays 8. Every mutant flips the first
// comparison true (8<=8, 8>=8, or 8 > 2/4==0), runs the shrink, and reallocates
// capacity down to threshold (4).
func Test_gk_subflux_u26_RecordFailure_shrink_cap_gate(t *testing.T) {
	t.Parallel()
	now := gk_subflux_u26_ref
	tr := gk_subflux_u26_fixedTracker(4, time.Hour, time.Hour, now)
	// Empty slice with capacity exactly 2*threshold; appending one entry keeps
	// len < threshold and cap == 8.
	tr.failures["pA"] = make([]time.Time, 0, 8)

	tr.RecordFailure("pA", nil)

	if got := cap(tr.failures["pA"]); got != 8 {
		t.Errorf("RecordFailure shrink cap-gate: cap(failures[pA]) = %d, want 8", got)
	}
}

// Kills timeout.go:124:49 (CONDITIONALS_BOUNDARY `<`->`<=`) on the length term
// `len(pruned) < it.threshold` of the same shrink gate.
//
// cap is 16 (> 2*threshold=8 so the first term is true and the length term is
// actually evaluated) and exactly threshold(4) entries survive pruning. The
// original `len(4) < 4` is false, so the block is skipped and cap stays 16. The
// `<=` mutant makes `4 <= 4` true, runs the shrink, and reallocates cap to 4.
func Test_gk_subflux_u26_RecordFailure_shrink_len_gate(t *testing.T) {
	t.Parallel()
	now := gk_subflux_u26_ref
	tr := gk_subflux_u26_fixedTracker(4, time.Hour, time.Hour, now)
	// cap 16, three in-window failures; pruning keeps all three, then the new
	// failure makes len == threshold (4) exactly.
	seed := make([]time.Time, 3, 16)
	seed[0], seed[1], seed[2] = now, now, now
	tr.failures["pB"] = seed

	tr.RecordFailure("pB", nil)

	if got := cap(tr.failures["pB"]); got != 16 {
		t.Errorf("RecordFailure shrink len-gate: cap(failures[pB]) = %d, want 16", got)
	}
}

// Kills timeout.go:156:20 (INVERT_NEGATIVES / ARITHMETIC_BASE on `-it.window`)
// and 163:10 (INCREMENT_DECREMENT on `count++`) in Status().
//
// Two failures sit at `now`, inside the window. The original computes
// cutoff = now - window, so both are After(cutoff) and count++ runs twice -> 2.
// Flipping the `-` to `+` puts cutoff in the future (now + window) so neither
// failure counts -> 0. Flipping `count++` to `count--` yields -2.
func Test_gk_subflux_u26_Status_recent_failures_count(t *testing.T) {
	t.Parallel()
	now := gk_subflux_u26_ref
	tr := gk_subflux_u26_fixedTracker(5, time.Hour, time.Hour, now)
	tr.failures["pS"] = []time.Time{now, now}

	out := tr.Status()

	if got := out["pS"].RecentFailures; got != 2 {
		t.Errorf("Status RecentFailures with 2 in-window failures = %d, want 2", got)
	}
}

// Kills timeout.go:168:29 (ARITHMETIC_BASE `-`->`+`, INVERT_NEGATIVES) on
// `remaining := it.cooldown - now.Sub(trippedAt)` in Status().
//
// Provider tripped 10 minutes ago with a 1h cooldown: remaining = 60m - 10m =
// 50m. The `+` mutant yields 60m + 10m = 70m (any operator change misses 50m).
func Test_gk_subflux_u26_Status_cooldown_remaining_value(t *testing.T) {
	t.Parallel()
	now := gk_subflux_u26_ref
	tr := gk_subflux_u26_fixedTracker(5, time.Hour, time.Hour, now)
	tr.failures["pC"] = []time.Time{now}
	tr.tripped["pC"] = now.Add(-10 * time.Minute)

	out := tr.Status()

	if got := out["pC"].CooldownRemaining; got != 50*time.Minute {
		t.Errorf("Status CooldownRemaining = %v, want %v", got, 50*time.Minute)
	}
}

// Kills timeout.go:169:17 (CONDITIONALS_BOUNDARY `>`->`>=`) on `if remaining > 0`
// in Status().
//
// Provider tripped exactly one cooldown ago, so remaining == 0. The original
// `0 > 0` is false, leaving TimedOut at its zero value (false). The `>=` mutant
// makes `0 >= 0` true and sets TimedOut.
func Test_gk_subflux_u26_Status_remaining_zero_not_timed_out(t *testing.T) {
	t.Parallel()
	now := gk_subflux_u26_ref
	tr := gk_subflux_u26_fixedTracker(5, time.Hour, time.Hour, now)
	tr.failures["pD"] = []time.Time{now}
	tr.tripped["pD"] = now.Add(-time.Hour)

	out := tr.Status()

	s, ok := out["pD"]
	if !ok {
		t.Fatalf("Status: expected an entry for pD")
	}
	if s.TimedOut {
		t.Errorf("Status TimedOut at remaining==0 = true, want false")
	}
}
