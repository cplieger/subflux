package authstore

// Tests added by gremlins-kill unit subflux-u24. They target surviving
// mutation-testing mutants in the internal/authstore package by adding
// behavioral assertions only — no production code is modified. Helpers and
// types defined here are prefixed gk_subflux_u24_ to avoid colliding with
// sibling units sharing this package. Existing in-package helpers
// (openShared, bootstrappedFile, mkSession, putOIDC, sampleKey, sampleCred)
// and unexported production symbols are reused directly.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/auth"
	bolt "go.etcd.io/bbolt"
)

// --- shared helpers ---

// gk_subflux_u24_newStore returns a Store over a freshly bootstrapped, shared
// bbolt handle WITHOUT starting the sweeper. The durable methods exercised here
// use s.db directly, so no background goroutine is needed; omitting Open keeps
// these log-capture tests free of concurrent log emission.
func gk_subflux_u24_newStore(t *testing.T) *Store {
	t.Helper()
	return New(openShared(t, bootstrappedFile(t)))
}

// gk_subflux_u24_logRec is one captured slog record: its message plus a copy of
// its attributes (keyed by name) so a test can inspect attribute values.
type gk_subflux_u24_logRec struct {
	msg   string
	attrs map[string]slog.Value
}

// gk_subflux_u24_capHandler is a slog.Handler that records every log line into a
// shared slice under a mutex. It is enabled at every level so DEBUG lines (the
// cleanup guards) are captured.
type gk_subflux_u24_capHandler struct {
	mu   *sync.Mutex
	recs *[]gk_subflux_u24_logRec
}

func (gk_subflux_u24_capHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h gk_subflux_u24_capHandler) Handle(_ context.Context, r slog.Record) error {
	rec := gk_subflux_u24_logRec{msg: r.Message, attrs: make(map[string]slog.Value)}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, rec)
	h.mu.Unlock()
	return nil
}

func (h gk_subflux_u24_capHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h gk_subflux_u24_capHandler) WithGroup(string) slog.Handler      { return h }

// gk_subflux_u24_captureLogs installs a capturing slog default handler for the
// duration of the test and returns a snapshot getter. The previous default is
// restored on cleanup. Tests using it must not call t.Parallel (slog.SetDefault
// is process-global).
func gk_subflux_u24_captureLogs(t *testing.T) func() []gk_subflux_u24_logRec {
	t.Helper()
	mu := &sync.Mutex{}
	recs := &[]gk_subflux_u24_logRec{}
	prev := slog.Default()
	slog.SetDefault(slog.New(gk_subflux_u24_capHandler{mu: mu, recs: recs}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []gk_subflux_u24_logRec {
		mu.Lock()
		defer mu.Unlock()
		out := make([]gk_subflux_u24_logRec, len(*recs))
		copy(out, *recs)
		return out
	}
}

// gk_subflux_u24_countMsg counts captured records with the given message.
func gk_subflux_u24_countMsg(recs []gk_subflux_u24_logRec, msg string) int {
	n := 0
	for i := range recs {
		if recs[i].msg == msg {
			n++
		}
	}
	return n
}

// gk_subflux_u24_seedCorruptAPIKey writes a user-scoped ix_apikey_user entry
// pointing at a deliberately corrupt auth_api_keys record, so a user-scoped walk
// hits an undecodable row and fails closed.
func gk_subflux_u24_seedCorruptAPIKey(t *testing.T, s *Store, userID int64, hash string) {
	t.Helper()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte(bucketAuthAPIKeys)).Put([]byte(hash), []byte("{not valid json")); err != nil {
			return err
		}
		return tx.Bucket([]byte(bucketIxAPIKeyUser)).Put(apiKeyUserIndexKey(userID, hash), nil)
	}); err != nil {
		t.Fatalf("seed corrupt api key: %v", err)
	}
}

// gk_subflux_u24_seedCorruptPasskey is the passkey analogue of
// gk_subflux_u24_seedCorruptAPIKey.
func gk_subflux_u24_seedCorruptPasskey(t *testing.T, s *Store, userID int64, credID []byte) {
	t.Helper()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte(bucketAuthPasskeys)).Put(credID, []byte("{not valid json")); err != nil {
			return err
		}
		return tx.Bucket([]byte(bucketIxPasskeyUser)).Put(passkeyUserIndexKey(userID, credID), nil)
	}); err != nil {
		t.Fatalf("seed corrupt passkey: %v", err)
	}
}

// --- apikeys.go ---

// apikeys.go:246:86 CONDITIONALS_NEGATION — the post-idxDelete `err != nil` in
// DeleteAPIKey. Negated to `err == nil`, the success path returns early and
// never reaches `deleted = true`, so the "api key deleted" audit line is
// suppressed even though the row and its index entry were deleted.
func Test_gk_subflux_u24_deleteAPIKeyLogsDeletionOnSuccess(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	key := sampleKey(1, "gk-del-log-hash", "k")
	if err := s.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	logs := gk_subflux_u24_captureLogs(t)
	if err := s.DeleteAPIKey(ctx, key.ID, 1); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if got := gk_subflux_u24_countMsg(logs(), "api key deleted"); got != 1 {
		t.Errorf(`successful owner delete logged "api key deleted" %d times, want 1`, got)
	}
}

// apikeys.go:252:9 CONDITIONALS_NEGATION — the outer `if err != nil` in
// DeleteAPIKey. Negated to `err == nil`, a real error from the update tx is
// swallowed and DeleteAPIKey returns nil. A corrupt API-key record makes
// findUserAPIKeyByID fail closed, forcing that error to surface.
func Test_gk_subflux_u24_deleteAPIKeyPropagatesUpdateError(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	gk_subflux_u24_seedCorruptAPIKey(t, s, 1, "gk-corrupt-hash")
	if err := s.DeleteAPIKey(context.Background(), 999, 1); err == nil {
		t.Fatal("DeleteAPIKey over a corrupt record = nil, want a non-nil decode error")
	}
}

// --- oidcstates.go ---

// oidcstates.go:93:11 CONDITIONALS_NEGATION + CONDITIONALS_BOUNDARY — the
// `if total > 0` guard on the "expired oidc states cleaned" debug line. Both
// BOUNDARY (>=0) and NEGATION (<=0) make it log when total==0, so the total==0
// assertion kills both; the total>0 assertion additionally locks the negation
// (which would NOT log when total>0).
func Test_gk_subflux_u24_oidcCleanupLogGuard(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	logs := gk_subflux_u24_captureLogs(t)
	now := time.Now().UTC()
	maxAge := 10 * time.Minute

	// No expired states -> total == 0 -> original logs nothing.
	putOIDC(s, "gk-live", now)
	n, err := s.CleanupExpiredOIDCStates(ctx, now, maxAge)
	if err != nil {
		t.Fatalf("CleanupExpiredOIDCStates(none expired): %v", err)
	}
	if n != 0 {
		t.Fatalf("evicted = %d, want 0", n)
	}
	if got := gk_subflux_u24_countMsg(logs(), "expired oidc states cleaned"); got != 0 {
		t.Errorf("total==0 logged the cleanup line %d times, want 0", got)
	}

	// One expired state -> total == 1 -> original logs exactly once.
	putOIDC(s, "gk-expired", now.Add(-time.Hour))
	n, err = s.CleanupExpiredOIDCStates(ctx, now, maxAge)
	if err != nil {
		t.Fatalf("CleanupExpiredOIDCStates(one expired): %v", err)
	}
	if n != 1 {
		t.Fatalf("evicted = %d, want 1", n)
	}
	if got := gk_subflux_u24_countMsg(logs(), "expired oidc states cleaned"); got != 1 {
		t.Errorf("after one eviction, cleanup line logged %d times total, want 1", got)
	}
}

// --- passkeys.go ---

// passkeys.go:176:108 CONDITIONALS_NEGATION — the post-idxPut `err != nil` in
// CreatePasskey. Negated to `err == nil`, an index-write failure is swallowed
// and the tx commits a primary row with no index entry, returning nil instead
// of erroring. Dropping ix_passkey_user makes idxPut fail.
func Test_gk_subflux_u24_createPasskeyErrorsWhenIndexBucketMissing(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte(bucketIxPasskeyUser))
	}); err != nil {
		t.Fatalf("drop %q: %v", bucketIxPasskeyUser, err)
	}
	err := s.CreatePasskey(ctx, sampleCred(1, []byte("gk-cred-idx"), "k"))
	if err == nil {
		t.Fatal("CreatePasskey with missing index bucket = nil, want a non-nil index error")
	}
}

// passkeys.go:345:90 CONDITIONALS_NEGATION — the post-idxDelete `err != nil` in
// DeletePasskey. Negated, the success path returns early without setting
// deleted=true, suppressing the "passkey deleted" audit line.
func Test_gk_subflux_u24_deletePasskeyLogsDeletionOnSuccess(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	cred := sampleCred(1, []byte("gk-del-pk-cred"), "k")
	if err := s.CreatePasskey(ctx, cred); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	logs := gk_subflux_u24_captureLogs(t)
	if err := s.DeletePasskey(ctx, cred.ID, 1); err != nil {
		t.Fatalf("DeletePasskey: %v", err)
	}
	if got := gk_subflux_u24_countMsg(logs(), "passkey deleted"); got != 1 {
		t.Errorf(`successful owner delete logged "passkey deleted" %d times, want 1`, got)
	}
}

// passkeys.go:351:9 CONDITIONALS_NEGATION — the outer `if err != nil` in
// DeletePasskey. Negated, a real update error is swallowed and DeletePasskey
// returns nil. A corrupt passkey record makes findUserPasskeyByID fail closed.
func Test_gk_subflux_u24_deletePasskeyPropagatesUpdateError(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	gk_subflux_u24_seedCorruptPasskey(t, s, 1, []byte("gk-corrupt-cred"))
	if err := s.DeletePasskey(context.Background(), 999, 1); err == nil {
		t.Fatal("DeletePasskey over a corrupt record = nil, want a non-nil decode error")
	}
}

// --- sessions.go ---

// sessions.go:167:11 CONDITIONALS_NEGATION + CONDITIONALS_BOUNDARY — the
// `if total > 0` guard on the "expired sessions cleaned" debug line. Same shape
// as the OIDC guard: total==0 kills both mutants, total>0 locks the negation.
func Test_gk_subflux_u24_sessionsCleanupLogGuard(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	logs := gk_subflux_u24_captureLogs(t)
	now := time.Now().UTC()
	idle := time.Hour
	abs := 24 * time.Hour

	// A live session only -> total == 0 -> original logs nothing.
	if err := s.CreateSession(ctx, mkSession("gk-live", 1, now, now)); err != nil {
		t.Fatalf("CreateSession(live): %v", err)
	}
	n, err := s.CleanupExpiredSessions(ctx, now, idle, abs)
	if err != nil {
		t.Fatalf("CleanupExpiredSessions(none expired): %v", err)
	}
	if n != 0 {
		t.Fatalf("evicted = %d, want 0", n)
	}
	if got := gk_subflux_u24_countMsg(logs(), "expired sessions cleaned"); got != 0 {
		t.Errorf("total==0 logged the cleanup line %d times, want 0", got)
	}

	// Add an idle-expired session -> total == 1 -> original logs once.
	if err := s.CreateSession(ctx, mkSession("gk-expired", 1, now.Add(-2*time.Hour), now.Add(-2*time.Hour))); err != nil {
		t.Fatalf("CreateSession(expired): %v", err)
	}
	n, err = s.CleanupExpiredSessions(ctx, now, idle, abs)
	if err != nil {
		t.Fatalf("CleanupExpiredSessions(one expired): %v", err)
	}
	if n != 1 {
		t.Fatalf("evicted = %d, want 1", n)
	}
	if got := gk_subflux_u24_countMsg(logs(), "expired sessions cleaned"); got != 1 {
		t.Errorf("after one eviction, cleanup line logged %d times total, want 1", got)
	}
}

// --- sweeper.go ---

// sweeper.go:55:14 CONDITIONALS_BOUNDARY — the `if interval <= 0` guard in Open
// that substitutes defaultSweepInterval for a non-positive configured interval.
// BOUNDARY (<= -> <) stops substituting at exactly 0, so a zero sweepInterval is
// passed straight through. The original substitutes the default; assert the
// started-sweeper log records that default, not 0.
func Test_gk_subflux_u24_sweeperZeroIntervalUsesDefault(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	s.sweepInterval = 0
	logs := gk_subflux_u24_captureLogs(t)
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var found bool
	for _, r := range logs() {
		if r.msg != "auth sweeper started" {
			continue
		}
		found = true
		iv, ok := r.attrs["interval"]
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

// --- users.go ---

// users.go:223:62 CONDITIONALS_NEGATION — the post-idxPut `err != nil` in
// CreateUser (the OIDC index write). Negated to `err == nil`, an index-write
// failure is swallowed and the user is committed without its ix_user_oidc
// entry, returning nil. Dropping ix_user_oidc makes that idxPut fail.
func Test_gk_subflux_u24_createUserErrorsWhenOIDCIndexBucketMissing(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte(bucketIxUserOIDC))
	}); err != nil {
		t.Fatalf("drop %q: %v", bucketIxUserOIDC, err)
	}
	u := &auth.User{Username: "gk-oidc-user", Role: auth.RoleUser, OIDCIssuer: "iss", OIDCSub: "sub"}
	if err := s.CreateUser(ctx, u); err == nil {
		t.Fatal("CreateUser with missing OIDC index bucket = nil, want a non-nil index error")
	}
}

// users.go:412:17 CONDITIONALS_NEGATION — the `newOIDCKey != nil` half of the
// UpdateUser OIDC-uniqueness guard. Negated to `newOIDCKey == nil`, an update
// that sets a NEW oidc identity skips the uniqueness check, so a collision with
// another user's (issuer, sub) is silently accepted instead of rejected.
func Test_gk_subflux_u24_updateUserRejectsOIDCCollision(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	a := &auth.User{Username: "gk-a", Role: auth.RoleUser, OIDCIssuer: "iss", OIDCSub: "shared-sub"}
	if err := s.CreateUser(ctx, a); err != nil {
		t.Fatalf("CreateUser(a): %v", err)
	}
	b := &auth.User{Username: "gk-b", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, b); err != nil {
		t.Fatalf("CreateUser(b): %v", err)
	}
	// Point b at a's (issuer, sub): a brand-new oidc identity that collides.
	b.OIDCIssuer = "iss"
	b.OIDCSub = "shared-sub"
	if err := s.UpdateUser(ctx, b); !errors.Is(err, errConflict) {
		t.Fatalf("UpdateUser into a colliding (issuer,sub) = %v, want errConflict", err)
	}
}

// users.go:500:18 CONDITIONALS_NEGATION — the `rec.OIDCSub != ""` guard in
// DeleteUser that removes the user's ix_user_oidc entry. Negated to `== ""`,
// deleting a user that HAS an oidc identity skips the index removal, leaving a
// stale entry that blocks recreating a user with the same (issuer, sub).
func Test_gk_subflux_u24_deleteUserFreesOIDCIndexForReuse(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	a := &auth.User{Username: "gk-oidc-victim", Role: auth.RoleUser, OIDCIssuer: "iss-x", OIDCSub: "sub-x"}
	if err := s.CreateUser(ctx, a); err != nil {
		t.Fatalf("CreateUser(a): %v", err)
	}
	if err := s.DeleteUser(ctx, a.ID); err != nil {
		t.Fatalf("DeleteUser(a): %v", err)
	}
	// The (issuer, sub) must be free now: recreating with it must succeed.
	b := &auth.User{Username: "gk-oidc-reuse", Role: auth.RoleUser, OIDCIssuer: "iss-x", OIDCSub: "sub-x"}
	if err := s.CreateUser(ctx, b); err != nil {
		t.Fatalf("CreateUser reusing the deleted user's (issuer,sub) = %v, want nil (stale index entry?)", err)
	}
}

// users.go:513:85 CONDITIONALS_NEGATION — the `err != nil` check after the
// API-key cascade in DeleteUser. Negated to `err == nil`, a cascade failure is
// swallowed and the delete tx commits anyway, returning nil. A poisoned
// ix_apikey_user entry whose child key names a sub-bucket makes the cascade's
// pb.Delete return ErrIncompatibleValue.
func Test_gk_subflux_u24_deleteUserPropagatesAPIKeyCascadeError(t *testing.T) {
	s := gk_subflux_u24_newStore(t)
	ctx := context.Background()
	u := &auth.User{Username: "gk-cascade-user", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	const child = "gk_subflux_u24_subbkt"
	if err := s.db.Update(func(tx *bolt.Tx) error {
		// A sub-bucket inside auth_api_keys: deleting it via Bucket.Delete
		// (which the cascade does) returns ErrIncompatibleValue.
		if _, err := tx.Bucket([]byte(bucketAuthAPIKeys)).CreateBucket([]byte(child)); err != nil {
			return err
		}
		// A user-scoped index entry whose child segment is that sub-bucket name,
		// so the cascade walk targets it.
		return tx.Bucket([]byte(bucketIxAPIKeyUser)).Put(apiKeyUserIndexKey(u.ID, child), nil)
	}); err != nil {
		t.Fatalf("seed cascade-poison entry: %v", err)
	}
	if err := s.DeleteUser(ctx, u.ID); err == nil {
		t.Fatal("DeleteUser with a failing API-key cascade = nil, want a non-nil error")
	}
}
