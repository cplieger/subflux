package authstore

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newOIDCStore returns a Store wired over a bootstrapped, shared bbolt handle.
// OIDC states are in-memory, but New still needs a handle (the durable half
// shares it). The helpers bootstrappedFile/openShared live in authdb_test.go
// (same package).
func newOIDCStore(t *testing.T) *Store {
	t.Helper()
	return New(openShared(t, bootstrappedFile(t)))
}

// putOIDC inserts an OIDC record with an explicit creation instant, bypassing
// CreateOIDCState (which stamps time.Now). It lets the cleanup test control
// record age. White-box: the test is in-package, so it touches s.oidc under the
// same lock the production code uses.
func putOIDC(s *Store, state string, ts time.Time) {
	s.mu.Lock()
	s.oidc[state] = &oidcRec{
		expiresAt:    ts,
		nonce:        "n-" + state,
		codeVerifier: "v-" + state,
		redirectURI:  "/cb/" + state,
	}
	s.mu.Unlock()
}

func TestCreateOIDCState_andConsume_roundTrips(t *testing.T) {
	s := newOIDCStore(t)
	ctx := context.Background()
	if err := s.CreateOIDCState(ctx, "state-abc", "nonce-xyz", "verifier-123", "/callback"); err != nil {
		t.Fatalf("CreateOIDCState: %v", err)
	}
	nonce, verifier, redirect, err := s.ConsumeOIDCState(ctx, "state-abc")
	if err != nil {
		t.Fatalf("ConsumeOIDCState: %v", err)
	}
	if nonce != "nonce-xyz" {
		t.Errorf("nonce = %q, want %q", nonce, "nonce-xyz")
	}
	if verifier != "verifier-123" {
		t.Errorf("codeVerifier = %q, want %q", verifier, "verifier-123")
	}
	if redirect != "/callback" {
		t.Errorf("redirectURI = %q, want %q", redirect, "/callback")
	}
}

// TestConsumeOIDCState_secondConsumeNotFound is the single-use / anti-replay
// guarantee: the first consume succeeds, the second consume of the same state
// returns not-found (Requirement 16.3).
func TestConsumeOIDCState_secondConsumeNotFound(t *testing.T) {
	s := newOIDCStore(t)
	ctx := context.Background()
	if err := s.CreateOIDCState(ctx, "state-abc", "n", "v", "/cb"); err != nil {
		t.Fatalf("CreateOIDCState: %v", err)
	}
	if _, _, _, err := s.ConsumeOIDCState(ctx, "state-abc"); err != nil {
		t.Fatalf("first ConsumeOIDCState: %v", err)
	}
	nonce, verifier, redirect, err := s.ConsumeOIDCState(ctx, "state-abc")
	if err == nil {
		t.Error("second ConsumeOIDCState: expected not-found error, got nil")
	}
	if nonce != "" || verifier != "" || redirect != "" {
		t.Errorf("second consume returned values %q/%q/%q, want all empty", nonce, verifier, redirect)
	}
}

func TestConsumeOIDCState_unknownNotFound(t *testing.T) {
	s := newOIDCStore(t)
	_, _, _, err := s.ConsumeOIDCState(context.Background(), "never-created")
	if err == nil {
		t.Error("ConsumeOIDCState(unknown): expected not-found error, got nil")
	}
}

// TestCleanupExpiredOIDCStates covers eviction of expired states, retention of
// live states, the exclusive boundary (a state exactly at the cutoff is kept),
// and the returned count.
func TestCleanupExpiredOIDCStates(t *testing.T) {
	s := newOIDCStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	maxAge := 10 * time.Minute

	// live: created a minute ago -> kept.
	putOIDC(s, "live", now.Add(-time.Minute))
	// expired: created 11 minutes ago, past the 10-minute maxAge -> evicted.
	putOIDC(s, "expired", now.Add(-11*time.Minute))
	// boundary: created exactly maxAge ago -> NOT expired (strict <), kept.
	putOIDC(s, "boundary", now.Add(-maxAge))

	n, err := s.CleanupExpiredOIDCStates(ctx, now, maxAge)
	if err != nil {
		t.Fatalf("CleanupExpiredOIDCStates: %v", err)
	}
	if n != 1 {
		t.Errorf("evicted count = %d, want 1", n)
	}

	for state, wantPresent := range map[string]bool{"live": true, "expired": false, "boundary": true} {
		_, _, _, err := s.ConsumeOIDCState(ctx, state)
		present := err == nil
		if present != wantPresent {
			t.Errorf("state %q present=%v, want %v", state, present, wantPresent)
		}
	}
}

// TestConsumeOIDCState_concurrentSingleUse launches many goroutines that all
// try to consume the SAME state; under -race, exactly one must succeed. This
// proves the read+delete is atomic under a single lock (no two consumers can
// both observe the entry). Requirement 16.3.
func TestConsumeOIDCState_concurrentSingleUse(t *testing.T) {
	s := newOIDCStore(t)
	ctx := context.Background()
	if err := s.CreateOIDCState(ctx, "race-state", "nonce-win", "verifier-win", "/cb-win"); err != nil {
		t.Fatalf("CreateOIDCState: %v", err)
	}

	const workers = 64
	var (
		wg        sync.WaitGroup
		successes atomic.Int64
		gotNonce  atomic.Value // string, set by the single winner
	)
	start := make(chan struct{})
	for range workers {
		wg.Go(func() {
			<-start // maximize contention: all goroutines race from the same instant
			nonce, _, _, err := s.ConsumeOIDCState(ctx, "race-state")
			if err == nil {
				successes.Add(1)
				gotNonce.Store(nonce)
			}
		})
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("concurrent single-use: %d goroutines succeeded, want exactly 1", got)
	}
	if v, _ := gotNonce.Load().(string); v != "nonce-win" {
		t.Errorf("winner nonce = %q, want %q", v, "nonce-win")
	}
}

// TestCleanupExpiredOIDCStates_logsOnlyWhenEvicted pins the log guard: the
// "expired oidc states cleaned" line is emitted only when something is actually
// evicted — never on a sweep that evicts nothing.
func TestCleanupExpiredOIDCStates_logsOnlyWhenEvicted(t *testing.T) {
	s := newOIDCStore(t)
	ctx := context.Background()
	logs := captureLogs(t)
	now := time.Now().UTC()
	maxAge := 10 * time.Minute

	// No expired states -> nothing evicted -> no log line.
	putOIDC(s, "live", now)
	n, err := s.CleanupExpiredOIDCStates(ctx, now, maxAge)
	if err != nil {
		t.Fatalf("CleanupExpiredOIDCStates(none expired): %v", err)
	}
	if n != 0 {
		t.Fatalf("evicted = %d, want 0", n)
	}
	if got := countMsg(logs(), "expired oidc states cleaned"); got != 0 {
		t.Errorf("nothing evicted logged the cleanup line %d times, want 0", got)
	}

	// One expired state -> one eviction -> exactly one log line.
	putOIDC(s, "expired", now.Add(-time.Hour))
	n, err = s.CleanupExpiredOIDCStates(ctx, now, maxAge)
	if err != nil {
		t.Fatalf("CleanupExpiredOIDCStates(one expired): %v", err)
	}
	if n != 1 {
		t.Fatalf("evicted = %d, want 1", n)
	}
	if got := countMsg(logs(), "expired oidc states cleaned"); got != 1 {
		t.Errorf("after one eviction, cleanup line logged %d times total, want 1", got)
	}
}
