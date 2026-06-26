// Package authstoretest provides a shared, engine-agnostic behavioral contract
// suite for the composite auth store (cplieger/auth/store.Composite). It
// depends ONLY on the cplieger/auth library (the domain types and the store
// interface), so it can be driven against any concrete engine — the legacy
// SQLite authdb.AuthDB and the new bbolt authstore.Store — without importing
// either, proving behavioural parity at the AuthStore seam (Requirements 14.1,
// 14.2).
//
// # What this suite is
//
// Suite asserts EXACT observable values, not merely "does not error", for the
// behaviours that are TRUE PARITY between the old SQLite store and the new
// bbolt store:
//
//   - uniqueness: duplicate username (case-insensitive), duplicate
//     (oidc_issuer, oidc_sub), duplicate passkey credential id, and duplicate
//     API-key hash are each rejected with a non-nil error and no partial write
//     (Requirements 9.3, 16.1);
//   - user-delete cascade: a deleted user's passkeys, API keys, and sessions
//     are all removed, the freed username can be recreated, and ANOTHER user's
//     records are untouched (Requirement 9.4);
//   - single-use ConsumeOIDCState: the first consume returns the stored values
//     exactly, the second returns not-found (Requirement 16.3);
//   - credential ownership: a non-owner cannot delete or rename a passkey, nor
//     delete an API key (Requirement 16.4);
//   - session expiry: CleanupExpiredSessions evicts idle-expired and
//     absolute-expired sessions, keeps live ones, with an exact count and an
//     exclusive boundary (Requirement 10.3);
//   - sign_count durability across a simulated restart: a raised sign_count
//     survives a Reopen, and so do the durable user/passkey records
//     (Requirement 9.5, the durable-and-monotonic half that BOTH engines
//     share).
//
// Deliberate DIVERGENCES between the two engines are intentionally NOT asserted
// here, because they would make the shared suite fail against one engine. They
// are covered by new-store-only tests in the authstore package and documented
// there:
//
//   - sign_count never REGRESSES on a lower incoming count: the bbolt store
//     stores max(stored, incoming) (CVE-2023-45669 hardening); the SQLite store
//     overwrote unconditionally.
//   - the CloneWarning flag round-trips: the bbolt store persists it; the
//     SQLite schema has no clone_warning column.
//   - sessions/OIDC states are empty after a restart: they are ephemeral
//     (in-memory) in the bbolt design; the SQLite store persisted sessions in a
//     table.
//
// # Backing-store dependency
//
// The sign_count-durability case needs to survive a simulated process restart,
// so the suite drives it through a Harness whose Reopen closes the current
// store and reopens durable state from the SAME backing file. Each engine's
// test package supplies the Harness; the suite stays engine-agnostic.
package authstoretest

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/auth"
	authlibstore "github.com/cplieger/auth/store"
)

// Harness builds AuthStore instances over durable storage that survives a
// simulated restart, so durability can be asserted engine-agnostically. Each
// engine's test package implements it: the bbolt store over a real *.bolt file,
// the SQLite store over a file-backed database.
type Harness interface {
	// Store returns the live store to exercise.
	Store() authlibstore.Composite
	// Reopen simulates a process restart: it closes the current store and
	// reopens durable state from the same backing file, returning a fresh
	// store. Durable records (users, passkeys, API keys) MUST survive; whether
	// ephemeral records (sessions, OIDC states) survive is engine-specific and
	// is not asserted by the shared suite.
	Reopen(t *testing.T) authlibstore.Composite
}

// Suite runs the engine-agnostic behavioural contract against any AuthStore
// produced by newHarness. Each behaviour is a NAMED subtest so a failure names
// the exact contract that regressed.
func Suite(t *testing.T, newHarness func(t *testing.T) Harness) {
	t.Helper()

	t.Run("Uniqueness_username_oidc_passkey_apikey", func(t *testing.T) {
		testUniqueness(t, newHarness(t))
	})
	t.Run("DeleteUser_cascade_and_isolation", func(t *testing.T) {
		testDeleteUserCascade(t, newHarness(t))
	})
	t.Run("ConsumeOIDCState_single_use", func(t *testing.T) {
		testConsumeOIDCStateSingleUse(t, newHarness(t))
	})
	t.Run("CredentialOwnership_passkey_and_apikey", func(t *testing.T) {
		testCredentialOwnership(t, newHarness(t))
	})
	t.Run("SessionExpiry_idle_and_absolute", func(t *testing.T) {
		testSessionExpiry(t, newHarness(t))
	})
	t.Run("SignCount_durable_across_reopen", func(t *testing.T) {
		testSignCountDurableAcrossReopen(t, newHarness(t))
	})
}

// --- builders ---

func mkUser(name string) *auth.User {
	return &auth.User{Username: name, Role: auth.RoleUser, Enabled: true}
}

// mkPasskey builds a minimally-valid passkey: a non-empty credential id, public
// key, and a 16-byte AAGUID are required by the SQLite NOT NULL columns (the
// old store's INSERT lists every column, so a nil value becomes a NULL rather
// than the schema default), so the suite always sets them. A real passkey
// always carries these fields, so this keeps the suite engine-agnostic.
func mkPasskey(userID int64, credID []byte, name string) *auth.PasskeyCredential {
	return &auth.PasskeyCredential{
		UserID:       userID,
		CredentialID: credID,
		PublicKey:    []byte("pub-" + name),
		AAGUID:       make([]byte, 16),
		Name:         name,
		SignCount:    0,
	}
}

func mkAPIKey(userID int64, hash, label string) *auth.Key {
	return &auth.Key{UserID: userID, KeyHash: hash, KeyPrefix: "sk_", KeySuffix: "abcd", Label: label}
}

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

// --- behaviours ---

// testUniqueness asserts all four uniqueness constraints reject a duplicate
// with a non-nil error and write nothing (Requirements 9.3, 16.1).
func testUniqueness(t *testing.T, h Harness) {
	t.Helper()
	s := h.Store()
	assertUsernameUniqueness(t, s)
	assertOIDCUniqueness(t, s)
	assertCredentialUniqueness(t, s)
}

// assertUsernameUniqueness asserts a duplicate username is rejected
// case-insensitively with no partial write (Requirements 9.3, 16.1).
func assertUsernameUniqueness(t *testing.T, s authlibstore.Composite) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateUser(ctx, mkUser("Alice")); err != nil {
		t.Fatalf("CreateUser(Alice): %v", err)
	}
	if err := s.CreateUser(ctx, mkUser("alice")); err == nil {
		t.Errorf("CreateUser(alice) duplicate username: err = nil, want non-nil (case-insensitive)")
	}
	if n, err := s.UserCount(ctx); err != nil || n != 1 {
		t.Errorf("UserCount after rejected duplicate = (%d, %v), want (1, nil) — no partial write", n, err)
	}
}

// assertOIDCUniqueness asserts a duplicate (issuer, sub) is rejected while a
// distinct sub under the same issuer is allowed (Requirement 9.3).
func assertOIDCUniqueness(t *testing.T, s authlibstore.Composite) {
	t.Helper()
	ctx := context.Background()
	const issuer = "https://idp"
	oidc1 := mkUser("oidc1")
	oidc1.OIDCIssuer, oidc1.OIDCSub = issuer, "sub-1"
	if err := s.CreateUser(ctx, oidc1); err != nil {
		t.Fatalf("CreateUser(oidc1): %v", err)
	}
	dupOIDC := mkUser("oidc2")
	dupOIDC.OIDCIssuer, dupOIDC.OIDCSub = issuer, "sub-1"
	if err := s.CreateUser(ctx, dupOIDC); err == nil {
		t.Errorf("CreateUser duplicate (issuer,sub): err = nil, want non-nil")
	}
	// A distinct sub under the same issuer is allowed.
	distinct := mkUser("oidc3")
	distinct.OIDCIssuer, distinct.OIDCSub = issuer, "sub-2"
	if err := s.CreateUser(ctx, distinct); err != nil {
		t.Errorf("CreateUser(distinct sub) = %v, want nil", err)
	}
}

// assertCredentialUniqueness asserts a duplicate passkey credential id and a
// duplicate API-key hash are each rejected with no partial write. Both checks
// need a real owner (the SQLite store has a NOT NULL FK), so this helper creates
// one and exercises both (Requirements 9.3, 16.1).
func assertCredentialUniqueness(t *testing.T, s authlibstore.Composite) {
	t.Helper()
	ctx := context.Background()
	owner := mkUser("pk-owner")
	if err := s.CreateUser(ctx, owner); err != nil {
		t.Fatalf("CreateUser(pk-owner): %v", err)
	}

	// Passkey credential id.
	credID := []byte("dup-cred")
	if err := s.CreatePasskey(ctx, mkPasskey(owner.ID, credID, "first")); err != nil {
		t.Fatalf("CreatePasskey(first): %v", err)
	}
	if err := s.CreatePasskey(ctx, mkPasskey(owner.ID, credID, "second")); err == nil {
		t.Errorf("CreatePasskey duplicate credential id: err = nil, want non-nil")
	}
	if n, err := s.PasskeyCountForUser(ctx, owner.ID); err != nil || n != 1 {
		t.Errorf("PasskeyCountForUser after rejected duplicate = (%d, %v), want (1, nil) — no partial write", n, err)
	}

	// API-key hash.
	if err := s.CreateAPIKey(ctx, mkAPIKey(owner.ID, "dup-hash", "first")); err != nil {
		t.Fatalf("CreateAPIKey(first): %v", err)
	}
	if err := s.CreateAPIKey(ctx, mkAPIKey(owner.ID, "dup-hash", "second")); err == nil {
		t.Errorf("CreateAPIKey duplicate hash: err = nil, want non-nil")
	}
	keys, err := s.ListAPIKeysByUserID(ctx, owner.ID)
	if err != nil {
		t.Fatalf("ListAPIKeysByUserID: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("API keys after rejected duplicate = %d, want 1 — no partial write", len(keys))
	}
}

// testDeleteUserCascade asserts DeleteUser removes the user's passkeys, API
// keys, and sessions, frees the username, and leaves another user untouched
// (Requirement 9.4).
func testDeleteUserCascade(t *testing.T, h Harness) {
	t.Helper()
	ctx := context.Background()
	s := h.Store()

	victim := mkUser("victim")
	keep := mkUser("keep")
	if err := s.CreateUser(ctx, victim); err != nil {
		t.Fatalf("CreateUser(victim): %v", err)
	}
	if err := s.CreateUser(ctx, keep); err != nil {
		t.Fatalf("CreateUser(keep): %v", err)
	}

	vCred, kCred := []byte("victim-cred"), []byte("keep-cred")
	if err := s.CreatePasskey(ctx, mkPasskey(victim.ID, vCred, "vpk")); err != nil {
		t.Fatalf("CreatePasskey(victim): %v", err)
	}
	if err := s.CreatePasskey(ctx, mkPasskey(keep.ID, kCred, "kpk")); err != nil {
		t.Fatalf("CreatePasskey(keep): %v", err)
	}
	if err := s.CreateAPIKey(ctx, mkAPIKey(victim.ID, "victim-hash", "vkey")); err != nil {
		t.Fatalf("CreateAPIKey(victim): %v", err)
	}
	if err := s.CreateAPIKey(ctx, mkAPIKey(keep.ID, "keep-hash", "kkey")); err != nil {
		t.Fatalf("CreateAPIKey(keep): %v", err)
	}
	now := time.Now().UTC()
	if err := s.CreateSession(ctx, mkSession("victim-sess", victim.ID, now, now)); err != nil {
		t.Fatalf("CreateSession(victim): %v", err)
	}
	if err := s.CreateSession(ctx, mkSession("keep-sess", keep.ID, now, now)); err != nil {
		t.Fatalf("CreateSession(keep): %v", err)
	}

	if err := s.DeleteUser(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteUser(victim): %v", err)
	}

	assertVictimCascaded(t, s, victim.ID, vCred)
	assertKeepUserIntact(t, s, keep.ID)
}

// assertVictimCascaded verifies the deleted user is gone, its username is
// freed, and its passkeys, API keys, and sessions were all cascaded away
// (Requirement 9.4).
func assertVictimCascaded(t *testing.T, s authlibstore.Composite, victimID int64, vCred []byte) {
	t.Helper()
	ctx := context.Background()

	// Victim user gone, username freed.
	if got, _ := s.GetUserByID(ctx, victimID); got != nil {
		t.Errorf("victim still present after delete: %+v", got)
	}
	if got, _ := s.GetUserByUsername(ctx, "victim"); got != nil {
		t.Errorf("victim username still resolves after delete")
	}
	if err := s.CreateUser(ctx, mkUser("victim")); err != nil {
		t.Errorf("recreate freed username: %v", err)
	}

	// Victim's children gone.
	if n, _ := s.PasskeyCountForUser(ctx, victimID); n != 0 {
		t.Errorf("victim passkeys not cascaded: count = %d, want 0", n)
	}
	if got, _ := s.GetPasskeyByCredentialID(ctx, vCred); got != nil {
		t.Errorf("victim passkey still resolvable by credential id after cascade")
	}
	if keys, _ := s.ListAPIKeysByUserID(ctx, victimID); len(keys) != 0 {
		t.Errorf("victim api keys not cascaded: count = %d, want 0", len(keys))
	}
	if got, _ := s.GetSessionByHash(ctx, "victim-sess"); got != nil {
		t.Errorf("victim session not cleared on cascade")
	}
}

// assertKeepUserIntact verifies the unrelated user and all its records survive
// another user's delete cascade (Requirement 9.4 isolation).
func assertKeepUserIntact(t *testing.T, s authlibstore.Composite, keepID int64) {
	t.Helper()
	ctx := context.Background()

	if got, _ := s.GetUserByID(ctx, keepID); got == nil {
		t.Errorf("keep user collaterally deleted")
	}
	if n, _ := s.PasskeyCountForUser(ctx, keepID); n != 1 {
		t.Errorf("keep passkey collaterally deleted: count = %d, want 1", n)
	}
	if got, _ := s.GetAPIKeyByHash(ctx, "keep-hash"); got == nil {
		t.Errorf("keep api key collaterally deleted")
	}
	if got, _ := s.GetSessionByHash(ctx, "keep-sess"); got == nil {
		t.Errorf("keep session collaterally cleared")
	}
}

// testConsumeOIDCStateSingleUse asserts the first consume returns the stored
// values exactly and the second returns not-found (Requirement 16.3).
func testConsumeOIDCStateSingleUse(t *testing.T, h Harness) {
	t.Helper()
	ctx := context.Background()
	s := h.Store()

	if err := s.CreateOIDCState(ctx, "state-1", "nonce-1", "verifier-1", "/cb-1"); err != nil {
		t.Fatalf("CreateOIDCState: %v", err)
	}
	nonce, verifier, redirect, err := s.ConsumeOIDCState(ctx, "state-1")
	if err != nil {
		t.Fatalf("first ConsumeOIDCState: %v", err)
	}
	if nonce != "nonce-1" || verifier != "verifier-1" || redirect != "/cb-1" {
		t.Errorf("first consume = (%q, %q, %q), want (nonce-1, verifier-1, /cb-1)", nonce, verifier, redirect)
	}

	nonce2, verifier2, redirect2, err2 := s.ConsumeOIDCState(ctx, "state-1")
	if err2 == nil {
		t.Errorf("second ConsumeOIDCState: err = nil, want not-found")
	}
	if nonce2 != "" || verifier2 != "" || redirect2 != "" {
		t.Errorf("second consume returned (%q, %q, %q), want all empty", nonce2, verifier2, redirect2)
	}

	// An unknown state is likewise not-found.
	if _, _, _, err := s.ConsumeOIDCState(ctx, "never-created"); err == nil {
		t.Errorf("ConsumeOIDCState(unknown): err = nil, want not-found")
	}
}

// testCredentialOwnership asserts a non-owner cannot delete or rename a passkey,
// nor delete an API key; the owner can (Requirement 16.4).
func testCredentialOwnership(t *testing.T, h Harness) {
	t.Helper()
	ctx := context.Background()
	s := h.Store()

	owner := mkUser("owner")
	other := mkUser("other")
	if err := s.CreateUser(ctx, owner); err != nil {
		t.Fatalf("CreateUser(owner): %v", err)
	}
	if err := s.CreateUser(ctx, other); err != nil {
		t.Fatalf("CreateUser(other): %v", err)
	}

	assertPasskeyOwnership(t, s, owner.ID, other.ID)
	assertAPIKeyOwnership(t, s, owner.ID, other.ID)
}

// assertPasskeyOwnership verifies a non-owner can neither rename nor delete a
// passkey while the owner can (Requirement 16.4).
func assertPasskeyOwnership(t *testing.T, s authlibstore.Composite, ownerID, otherID int64) {
	t.Helper()
	ctx := context.Background()

	cred := []byte("owner-cred")
	if err := s.CreatePasskey(ctx, mkPasskey(ownerID, cred, "original")); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	pks, perr := s.GetPasskeysByUserID(ctx, ownerID)
	if perr != nil || len(pks) != 1 {
		t.Fatalf("GetPasskeysByUserID(owner) = (%d, %v), want (1, nil)", len(pks), perr)
	}
	pkID := pks[0].ID

	// Non-owner rename is a no-op: the name is unchanged.
	if err := s.RenamePasskey(ctx, pkID, otherID, "hijacked"); err != nil {
		t.Fatalf("RenamePasskey(non-owner): %v", err)
	}
	if pks, _ := s.GetPasskeysByUserID(ctx, ownerID); len(pks) != 1 || pks[0].Name != "original" {
		t.Errorf("non-owner rename mutated passkey: %+v", pks)
	}
	// Non-owner delete is a no-op: the passkey survives.
	if err := s.DeletePasskey(ctx, pkID, otherID); err != nil {
		t.Fatalf("DeletePasskey(non-owner): %v", err)
	}
	if n, _ := s.PasskeyCountForUser(ctx, ownerID); n != 1 {
		t.Errorf("non-owner delete removed passkey: count = %d, want 1", n)
	}
	// Owner rename and delete take effect.
	if err := s.RenamePasskey(ctx, pkID, ownerID, "renamed"); err != nil {
		t.Fatalf("RenamePasskey(owner): %v", err)
	}
	if pks, _ := s.GetPasskeysByUserID(ctx, ownerID); len(pks) != 1 || pks[0].Name != "renamed" {
		t.Errorf("owner rename did not take effect: %+v", pks)
	}
	if err := s.DeletePasskey(ctx, pkID, ownerID); err != nil {
		t.Fatalf("DeletePasskey(owner): %v", err)
	}
	if n, _ := s.PasskeyCountForUser(ctx, ownerID); n != 0 {
		t.Errorf("owner delete did not remove passkey: count = %d, want 0", n)
	}
}

// assertAPIKeyOwnership verifies a non-owner cannot delete an API key while the
// owner can (Requirement 16.4).
func assertAPIKeyOwnership(t *testing.T, s authlibstore.Composite, ownerID, otherID int64) {
	t.Helper()
	ctx := context.Background()

	if err := s.CreateAPIKey(ctx, mkAPIKey(ownerID, "owner-keyhash", "k")); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	keys, kerr := s.ListAPIKeysByUserID(ctx, ownerID)
	if kerr != nil || len(keys) != 1 {
		t.Fatalf("ListAPIKeysByUserID(owner) = (%d, %v), want (1, nil)", len(keys), kerr)
	}
	keyID := keys[0].ID
	if err := s.DeleteAPIKey(ctx, keyID, otherID); err != nil {
		t.Fatalf("DeleteAPIKey(non-owner): %v", err)
	}
	if keys, _ := s.ListAPIKeysByUserID(ctx, ownerID); len(keys) != 1 {
		t.Errorf("non-owner delete removed api key: count = %d, want 1", len(keys))
	}
	if err := s.DeleteAPIKey(ctx, keyID, ownerID); err != nil {
		t.Fatalf("DeleteAPIKey(owner): %v", err)
	}
	if keys, _ := s.ListAPIKeysByUserID(ctx, ownerID); len(keys) != 0 {
		t.Errorf("owner delete did not remove api key: count = %d, want 0", len(keys))
	}
}

// testSessionExpiry asserts CleanupExpiredSessions evicts idle-expired and
// absolute-expired sessions, keeps live and exactly-at-boundary sessions, and
// returns the exact evicted count (Requirement 10.3). The exclusive (strict)
// boundary is shared by both engines.
func testSessionExpiry(t *testing.T, h Harness) {
	t.Helper()
	ctx := context.Background()
	s := h.Store()

	// Sessions reference a real user (the SQLite store has a NOT NULL FK).
	u := mkUser("sess-owner")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	now := time.Now().UTC()
	idle := time.Hour
	abs := 24 * time.Hour

	// live: recent create + recent activity -> kept.
	if err := s.CreateSession(ctx, mkSession("live", u.ID, now.Add(-time.Minute), now.Add(-time.Minute))); err != nil {
		t.Fatalf("CreateSession(live): %v", err)
	}
	// idleExpired: recent create but idle past the idle timeout -> evicted.
	if err := s.CreateSession(ctx, mkSession("idle", u.ID, now.Add(-2*time.Hour), now.Add(-2*time.Hour))); err != nil {
		t.Fatalf("CreateSession(idle): %v", err)
	}
	// absExpired: recent activity but created past the absolute timeout -> evicted.
	if err := s.CreateSession(ctx, mkSession("abs", u.ID, now.Add(-25*time.Hour), now.Add(-time.Minute))); err != nil {
		t.Fatalf("CreateSession(abs): %v", err)
	}
	// boundary: last_activity exactly at the idle cutoff -> kept (strict <).
	if err := s.CreateSession(ctx, mkSession("boundary", u.ID, now.Add(-time.Minute), now.Add(-idle))); err != nil {
		t.Fatalf("CreateSession(boundary): %v", err)
	}

	n, err := s.CleanupExpiredSessions(ctx, now, idle, abs)
	if err != nil {
		t.Fatalf("CleanupExpiredSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("evicted count = %d, want 2", n)
	}
	for hash, wantPresent := range map[string]bool{"live": true, "idle": false, "abs": false, "boundary": true} {
		got, _ := s.GetSessionByHash(ctx, hash)
		if present := got != nil; present != wantPresent {
			t.Errorf("session %q present = %v, want %v", hash, present, wantPresent)
		}
	}
}

// testSignCountDurableAcrossReopen asserts a raised sign_count and the durable
// user/passkey records survive a simulated restart (Requirement 9.5, the
// durable half shared by both engines).
func testSignCountDurableAcrossReopen(t *testing.T, h Harness) {
	t.Helper()
	ctx := context.Background()
	s := h.Store()

	u := mkUser("reopen-owner")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cred := mkPasskey(u.ID, []byte("reopen-cred"), "yubikey")
	cred.SignCount = 5
	if err := s.CreatePasskey(ctx, cred); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	// A successful login raises the counter to 9.
	flags := auth.PasskeyFlags{UserPresent: true, UserVerified: true}
	if err := s.UpdatePasskeyAfterLogin(ctx, cred.CredentialID, 9, flags); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin: %v", err)
	}
	if got, _ := s.GetPasskeyByCredentialID(ctx, cred.CredentialID); got == nil || got.SignCount != 9 {
		t.Fatalf("pre-reopen sign_count = %v, want 9", got)
	}

	// Simulate a process restart.
	s2 := h.Reopen(t)

	got, err := s2.GetPasskeyByCredentialID(ctx, cred.CredentialID)
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID after reopen: %v", err)
	}
	if got == nil {
		t.Fatalf("passkey did not survive reopen")
	}
	if got.SignCount != 9 {
		t.Errorf("sign_count after reopen = %d, want 9 (durable)", got.SignCount)
	}
	if gu, _ := s2.GetUserByUsername(ctx, "reopen-owner"); gu == nil {
		t.Errorf("durable user did not survive reopen")
	}
}
