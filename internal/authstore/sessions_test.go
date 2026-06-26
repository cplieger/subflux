package authstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/auth"
)

// newSessionStore returns a Store wired over a bootstrapped, shared bbolt
// handle. Sessions are in-memory, but New still needs a handle (the durable
// half shares it). The helpers bootstrappedFile/openShared live in
// authdb_test.go (same package).
func newSessionStore(t *testing.T) *Store {
	t.Helper()
	return New(openShared(t, bootstrappedFile(t)))
}

// mkSession builds a session for the given hash/user with explicit
// created/last-activity timestamps.
func mkSession(hash string, userID int64, created, lastActivity time.Time) *auth.Session {
	return &auth.Session{
		TokenHash:    hash,
		UserID:       userID,
		AuthMethod:   auth.MethodPassword,
		IPAddress:    "10.0.0.1",
		CreatedAt:    created,
		LastActivity: lastActivity,
	}
}

func TestCreateSession_andGetByHash_roundTrips(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	exp := now.Add(time.Hour)
	want := mkSession("h1", 7, now, now)
	want.AuthMethod = auth.MethodOIDC
	want.OIDCExpiry = &exp

	if err := s.CreateSession(ctx, want); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := s.GetSessionByHash(ctx, "h1")
	if err != nil {
		t.Fatalf("GetSessionByHash: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionByHash returned nil for a stored session")
	}
	if got.TokenHash != "h1" || got.UserID != 7 || got.AuthMethod != auth.MethodOIDC || got.IPAddress != "10.0.0.1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.OIDCExpiry == nil || !got.OIDCExpiry.Equal(exp) {
		t.Errorf("OIDCExpiry round-trip = %v, want %v", got.OIDCExpiry, exp)
	}
}

func TestGetSessionByHash_absentReturnsNilNil(t *testing.T) {
	s := newSessionStore(t)
	got, err := s.GetSessionByHash(context.Background(), "missing")
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("session = %+v, want nil", got)
	}
}

// TestGetSessionByHash_returnsCopy asserts callers cannot mutate stored state
// through the returned pointer (no map-value aliasing), including the
// OIDCExpiry pointer.
func TestGetSessionByHash_returnsCopy(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	// wantExp is the expected stored expiry, captured independently of the
	// pointer handed to CreateSession so later mutations of that pointer cannot
	// change the expectation.
	wantExp := now.Add(time.Hour)
	exp := wantExp
	in := mkSession("h1", 1, now, now)
	in.OIDCExpiry = &exp
	if err := s.CreateSession(ctx, in); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Mutating the caller's original struct after create must not affect storage.
	in.UserID = 999
	in.IPAddress = "evil"
	*in.OIDCExpiry = now.Add(100 * time.Hour)

	got, _ := s.GetSessionByHash(ctx, "h1")
	if got.UserID != 1 || got.IPAddress != "10.0.0.1" {
		t.Errorf("stored session was aliased to caller struct: %+v", got)
	}
	if !got.OIDCExpiry.Equal(wantExp) {
		t.Errorf("stored OIDCExpiry was aliased: got %v, want %v", got.OIDCExpiry, wantExp)
	}

	// Mutating the returned copy must not affect a subsequent read either.
	got.UserID = 555
	*got.OIDCExpiry = now.Add(200 * time.Hour)
	again, _ := s.GetSessionByHash(ctx, "h1")
	if again.UserID != 1 || !again.OIDCExpiry.Equal(wantExp) {
		t.Errorf("returned copy aliased stored session: %+v", again)
	}
}

func TestUpdateSessionActivity_single(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	t0 := time.Now().UTC()
	if err := s.CreateSession(ctx, mkSession("h1", 1, t0, t0)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t1 := t0.Add(30 * time.Minute)
	if err := s.UpdateSessionActivity(ctx, "h1", t1); err != nil {
		t.Fatalf("UpdateSessionActivity: %v", err)
	}
	got, _ := s.GetSessionByHash(ctx, "h1")
	if !got.LastActivity.Equal(t1) {
		t.Errorf("LastActivity = %v, want %v", got.LastActivity, t1)
	}
	// Absent session is a no-op returning nil (UPDATE affecting 0 rows).
	if err := s.UpdateSessionActivity(ctx, "missing", t1); err != nil {
		t.Errorf("UpdateSessionActivity(absent) = %v, want nil", err)
	}
}

func TestBatchUpdateSessionActivity(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	t0 := time.Now().UTC()
	for _, h := range []string{"h1", "h2", "h3"} {
		if err := s.CreateSession(ctx, mkSession(h, 1, t0, t0)); err != nil {
			t.Fatalf("CreateSession(%s): %v", h, err)
		}
	}
	t1 := t0.Add(time.Hour)
	// Includes an absent hash, which must be skipped without error.
	if err := s.BatchUpdateSessionActivity(ctx, []string{"h1", "h3", "missing"}, t1); err != nil {
		t.Fatalf("BatchUpdateSessionActivity: %v", err)
	}
	for h, want := range map[string]time.Time{"h1": t1, "h2": t0, "h3": t1} {
		got, _ := s.GetSessionByHash(ctx, h)
		if !got.LastActivity.Equal(want) {
			t.Errorf("%s LastActivity = %v, want %v", h, got.LastActivity, want)
		}
	}
	// Empty slice is a no-op.
	if err := s.BatchUpdateSessionActivity(ctx, nil, t1); err != nil {
		t.Errorf("BatchUpdateSessionActivity(nil) = %v, want nil", err)
	}
}

func TestDeleteSession(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	t0 := time.Now().UTC()
	if err := s.CreateSession(ctx, mkSession("h1", 1, t0, t0)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.DeleteSession(ctx, "h1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got, _ := s.GetSessionByHash(ctx, "h1"); got != nil {
		t.Errorf("session still present after delete: %+v", got)
	}
	// Deleting an absent session is a no-op returning nil.
	if err := s.DeleteSession(ctx, "missing"); err != nil {
		t.Errorf("DeleteSession(absent) = %v, want nil", err)
	}
}

// TestDeleteUserSessions_keepsOneAndOnlyThatUser asserts the keep-one exception
// removes all of the target user's sessions except exceptHash and never touches
// another user's sessions.
func TestDeleteUserSessions_keepsOneAndOnlyThatUser(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	t0 := time.Now().UTC()
	// User 1 has three sessions; user 2 has one.
	for _, h := range []string{"u1a", "u1b", "u1keep"} {
		if err := s.CreateSession(ctx, mkSession(h, 1, t0, t0)); err != nil {
			t.Fatalf("CreateSession(%s): %v", h, err)
		}
	}
	if err := s.CreateSession(ctx, mkSession("u2a", 2, t0, t0)); err != nil {
		t.Fatalf("CreateSession(u2a): %v", err)
	}

	if err := s.DeleteUserSessions(ctx, 1, "u1keep"); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}

	if got, _ := s.GetSessionByHash(ctx, "u1keep"); got == nil {
		t.Error("kept session u1keep was removed")
	}
	for _, h := range []string{"u1a", "u1b"} {
		if got, _ := s.GetSessionByHash(ctx, h); got != nil {
			t.Errorf("user-1 session %s should have been deleted", h)
		}
	}
	if got, _ := s.GetSessionByHash(ctx, "u2a"); got == nil {
		t.Error("other user's session u2a was wrongly deleted")
	}

	// exceptHash="" removes everything for the user (the cascade path).
	if err := s.DeleteUserSessions(ctx, 1, ""); err != nil {
		t.Fatalf("DeleteUserSessions(empty except): %v", err)
	}
	if got, _ := s.GetSessionByHash(ctx, "u1keep"); got != nil {
		t.Error("empty exceptHash should have removed u1keep")
	}
	if got, _ := s.GetSessionByHash(ctx, "u2a"); got == nil {
		t.Error("user-2 session must survive a user-1 bulk delete")
	}
}

// TestCleanupExpiredSessions covers idle-expiry, absolute-expiry, the keep-live
// case, the exclusive boundary, and the returned count.
func TestCleanupExpiredSessions(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	idle := time.Hour
	abs := 24 * time.Hour

	// live: created recently, active recently -> kept.
	if err := s.CreateSession(ctx, mkSession("live", 1, now.Add(-time.Minute), now.Add(-time.Minute))); err != nil {
		t.Fatalf("CreateSession(live): %v", err)
	}
	// idleExpired: created recently but idle past the idle timeout -> evicted.
	if err := s.CreateSession(ctx, mkSession("idle", 1, now.Add(-2*time.Hour), now.Add(-2*time.Hour))); err != nil {
		t.Fatalf("CreateSession(idle): %v", err)
	}
	// absExpired: active recently but created past the absolute timeout -> evicted.
	if err := s.CreateSession(ctx, mkSession("abs", 1, now.Add(-25*time.Hour), now.Add(-time.Minute))); err != nil {
		t.Fatalf("CreateSession(abs): %v", err)
	}
	// boundary: last_activity exactly at the idle cutoff -> NOT expired (strict <).
	if err := s.CreateSession(ctx, mkSession("boundary", 1, now.Add(-time.Minute), now.Add(-idle))); err != nil {
		t.Fatalf("CreateSession(boundary): %v", err)
	}

	n, err := s.CleanupExpiredSessions(ctx, now, idle, abs)
	if err != nil {
		t.Fatalf("CleanupExpiredSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("evicted count = %d, want 2", n)
	}
	for h, wantPresent := range map[string]bool{"live": true, "idle": false, "abs": false, "boundary": true} {
		got, _ := s.GetSessionByHash(ctx, h)
		if present := got != nil; present != wantPresent {
			t.Errorf("session %s present=%v, want %v", h, present, wantPresent)
		}
	}
}

// TestSessions_concurrentAccess exercises the RWMutex guarding the session map
// under -race: parallel create/get/update/cleanup/delete goroutines must not
// race or panic.
func TestSessions_concurrentAccess(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	const workers = 8
	const iters = 200
	var wg sync.WaitGroup

	// Creators / updaters.
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range iters {
				h := fmt.Sprintf("w%d-%d", w, i)
				_ = s.CreateSession(ctx, mkSession(h, int64(w), now, now))
				_ = s.UpdateSessionActivity(ctx, h, now.Add(time.Duration(i)*time.Second))
				_, _ = s.GetSessionByHash(ctx, h)
			}
		}(w)
	}
	// Sweepers.
	for range 2 {
		wg.Go(func() {
			for range iters {
				_, _ = s.CleanupExpiredSessions(ctx, time.Now(), time.Hour, 24*time.Hour)
			}
		})
	}
	// Batch updaters and per-user deleters.
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range iters {
				_ = s.BatchUpdateSessionActivity(ctx, []string{fmt.Sprintf("w%d-%d", w, i)}, now)
				_ = s.DeleteUserSessions(ctx, int64(w), fmt.Sprintf("w%d-0", w))
			}
		}(w)
	}
	wg.Wait()
}

// TestCleanupExpiredSessions_logsOnlyWhenEvicted pins the log guard: the
// "expired sessions cleaned" line is emitted only when something is actually
// evicted — never on a sweep that evicts nothing.
func TestCleanupExpiredSessions_logsOnlyWhenEvicted(t *testing.T) {
	s := newSessionStore(t)
	ctx := context.Background()
	logs := captureLogs(t)
	now := time.Now().UTC()
	idle := time.Hour
	abs := 24 * time.Hour

	// A live session only -> nothing evicted -> no log line.
	if err := s.CreateSession(ctx, mkSession("live", 1, now, now)); err != nil {
		t.Fatalf("CreateSession(live): %v", err)
	}
	n, err := s.CleanupExpiredSessions(ctx, now, idle, abs)
	if err != nil {
		t.Fatalf("CleanupExpiredSessions(none expired): %v", err)
	}
	if n != 0 {
		t.Fatalf("evicted = %d, want 0", n)
	}
	if got := countMsg(logs(), "expired sessions cleaned"); got != 0 {
		t.Errorf("nothing evicted logged the cleanup line %d times, want 0", got)
	}

	// Add an idle-expired session -> one eviction -> exactly one log line.
	if err := s.CreateSession(ctx, mkSession("expired", 1, now.Add(-2*time.Hour), now.Add(-2*time.Hour))); err != nil {
		t.Fatalf("CreateSession(expired): %v", err)
	}
	n, err = s.CleanupExpiredSessions(ctx, now, idle, abs)
	if err != nil {
		t.Fatalf("CleanupExpiredSessions(one expired): %v", err)
	}
	if n != 1 {
		t.Fatalf("evicted = %d, want 1", n)
	}
	if got := countMsg(logs(), "expired sessions cleaned"); got != 1 {
		t.Errorf("after one eviction, cleanup line logged %d times total, want 1", got)
	}
}
