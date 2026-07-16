package authstore

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/cplieger/slogx/capture"
	bolt "go.etcd.io/bbolt"
)

// newSweeperStore returns a Store wired over a bootstrapped, shared bbolt
// handle with a FAST sweep interval so the test does not wait minutes for a
// tick. The idle/absolute/OIDC durations keep their defaults; the test makes
// entries expired by backdating their timestamps well past those defaults.
// White-box: the test is in-package, so it sets the unexported sweepInterval
// before Open (Open snapshots it under the lock).
func newSweeperStore(t *testing.T, interval time.Duration) *Store {
	t.Helper()
	s := New(openShared(t, bootstrappedFile(t)))
	s.sweepInterval = interval
	return s
}

// waitFor polls cond until it is true or the deadline elapses. Returns true if
// cond became true within the bound. Used to assert eviction happens within a
// bounded time without sleeping for a fixed (and flaky) duration.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

// sessionPresent reports whether a session with the given hash is still in the
// store (used as the eviction predicate).
func sessionPresent(t *testing.T, s *Store, hash string) bool {
	t.Helper()
	got, err := s.GetSessionByHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("GetSessionByHash: %v", err)
	}
	return got != nil
}

// oidcPresent reports whether an OIDC state with the given token is still in
// the store. ConsumeOIDCState is single-use, so this is only safe to call once
// the entry is expected gone, or as a final assertion; the sweeper test only
// uses it after eviction is expected, and via the in-package map under lock for
// the live check.
func oidcPresent(s *Store, state string) bool {
	s.mu.RLock()
	_, ok := s.oidc[state]
	s.mu.RUnlock()
	return ok
}

// TestSweeper_evictsExpiredSessionsAndOIDCStates is the core behaviour: with a
// fast interval, an already-expired session and OIDC state are evicted within a
// bounded time once Open starts the sweeper, while a live entry survives
// (Requirement 10.3).
func TestSweeper_evictsExpiredSessionsAndOIDCStates(t *testing.T) {
	s := newSweeperStore(t, 2*time.Millisecond)
	ctx := context.Background()
	now := time.Now().UTC()

	// Expired session: idle 48h ago, well past the 24h default idle timeout.
	if err := s.CreateSession(ctx, mkSession("expired", 1, now.Add(-48*time.Hour), now.Add(-48*time.Hour))); err != nil {
		t.Fatalf("CreateSession(expired): %v", err)
	}
	// Live session: created and active just now -> must survive.
	if err := s.CreateSession(ctx, mkSession("live", 1, now, now)); err != nil {
		t.Fatalf("CreateSession(live): %v", err)
	}
	// Expired OIDC state: created 1h ago, past the 10m default TTL.
	putOIDC(s, "oidc-expired", now.Add(-time.Hour))
	// Live OIDC state: created just now -> must survive.
	putOIDC(s, "oidc-live", now)

	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if !waitFor(2*time.Second, func() bool {
		return !sessionPresent(t, s, "expired") && !oidcPresent(s, "oidc-expired")
	}) {
		t.Fatal("sweeper did not evict the expired session and OIDC state within the bound")
	}

	// Live entries must not have been swept.
	if !sessionPresent(t, s, "live") {
		t.Error("sweeper evicted a live session")
	}
	if !oidcPresent(s, "oidc-live") {
		t.Error("sweeper evicted a live OIDC state")
	}
}

// TestSweeper_closeStopsFurtherSweeps asserts that after Close the sweeper does
// no more work: an entry that becomes expired only AFTER Close stays put.
func TestSweeper_closeStopsFurtherSweeps(t *testing.T) {
	s := newSweeperStore(t, 2*time.Millisecond)
	ctx := context.Background()
	now := time.Now().UTC()

	// Start the sweeper, then stop it. Close waits for the goroutine to exit.
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Insert an already-expired session AFTER the sweeper has stopped.
	if err := s.CreateSession(ctx, mkSession("late-expired", 1, now.Add(-48*time.Hour), now.Add(-48*time.Hour))); err != nil {
		t.Fatalf("CreateSession(late-expired): %v", err)
	}

	// Give a stopped sweeper ample time to (wrongly) run; it must not.
	time.Sleep(50 * time.Millisecond)
	if !sessionPresent(t, s, "late-expired") {
		t.Error("session was evicted after Close stopped the sweeper")
	}
}

// TestSweeper_openCloseCloseNoPanic asserts the lifecycle is safe: Open then a
// double Close must not panic (closeOnce guards the stop channel) and each Close
// returns nil.
func TestSweeper_openCloseCloseNoPanic(t *testing.T) {
	s := newSweeperStore(t, time.Hour) // slow interval; lifecycle only
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestSweeper_closeWithoutOpen asserts Close is safe with no prior Open: it must
// not panic and must not close the shared *bbolt.DB handle (the core store owns
// it), so the handle stays usable afterwards.
func TestSweeper_closeWithoutOpen(t *testing.T) {
	db := openShared(t, bootstrappedFile(t))
	s := New(db)

	if err := s.Close(); err != nil {
		t.Fatalf("Close without Open: %v", err)
	}

	// The shared handle must still be usable: Close must not have closed it.
	if err := db.View(func(tx *bolt.Tx) error { return nil }); err != nil {
		t.Fatalf("shared handle unusable after Close-without-Open: %v", err)
	}
}

// TestSweeper_doubleOpenStartsOneGoroutine asserts Open is idempotent: a second
// Open while running is a no-op, and a single Close still cleanly stops the one
// goroutine (Close would block forever waiting on a second, never-joined done
// channel if a second goroutine had been started with a fresh done).
func TestSweeper_doubleOpenStartsOneGoroutine(t *testing.T) {
	s := newSweeperStore(t, 2*time.Millisecond)
	if err := s.Open(); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s.Open(); err != nil {
		t.Fatalf("second Open: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return; a double Open likely started a second goroutine")
	}
}

// TestSweepOnce_noSpuriousFailureLogsOnHealthySweep pins that a healthy sweep
// logs no failure: the in-memory cleanups always succeed, so neither the
// session nor the OIDC "cleanup failed" line may appear.
func TestSweepOnce_noSpuriousFailureLogsOnHealthySweep(t *testing.T) {
	logs := capture.Default(t)
	s := newSweeperStore(t, time.Hour) // interval unused; sweepOnce is called directly

	s.sweepOnce(time.Now()) // healthy sweep: both cleanups succeed (return nil)

	if n := logs.CountExact("auth sweeper: session cleanup failed"); n != 0 {
		t.Errorf("session cleanup failure logged %d times on a healthy sweep, want 0", n)
	}
	if n := logs.CountExact("auth sweeper: oidc cleanup failed"); n != 0 {
		t.Errorf("oidc cleanup failure logged %d times on a healthy sweep, want 0", n)
	}
}

// TestSweeper_zeroIntervalUsesDefault pins the non-positive-interval fallback: a
// zero configured sweepInterval is replaced by defaultSweepInterval, and the
// started-sweeper log records that default rather than 0.
func TestSweeper_zeroIntervalUsesDefault(t *testing.T) {
	s := newSweeperStore(t, 0)
	logs := capture.Default(t)
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var found bool
	for _, r := range logs.Records() {
		if r.Message != "auth sweeper started" {
			continue
		}
		found = true
		var iv slog.Value
		var ok bool
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "interval" {
				iv, ok = a.Value, true
				return false
			}
			return true
		})
		if !ok {
			t.Fatal(`"auth sweeper started" log has no "interval" attribute`)
		}
		if iv.Kind() != slog.KindDuration {
			t.Fatalf(`"interval" attribute kind = %v, want Duration`, iv.Kind())
		}
		if iv.Duration() != defaultSweepInterval {
			t.Errorf("sweeper started with interval %v, want default %v", iv.Duration(), defaultSweepInterval)
		}
	}
	if !found {
		t.Fatal(`no "auth sweeper started" log captured`)
	}
}
