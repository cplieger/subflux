package statedb

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
	"github.com/cplieger/go/slogx/capture"
)

var accountsAt = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func newAccounts(t *testing.T) (*Store, *AccountStore) {
	t.Helper()
	s := mustOpen(t, tempPath(t), productionConfig())
	return s, s.Accounts()
}

func mustCreateUser(t *testing.T, a *AccountStore, name string) *access.User {
	t.Helper()
	u := &access.User{Username: name, Role: access.RoleUser, Enabled: true, CreatedAt: accountsAt}
	if err := a.CreateUser(t.Context(), u); err != nil {
		t.Fatalf("CreateUser(%s): %v", name, err)
	}
	return u
}

func testSession(userID int64, id string, last time.Time) *access.Session {
	return &access.Session{
		CreatedAt: accountsAt, LastActivity: last, AuthTime: accountsAt, ID: id, TokenHash: access.NewHash("session-" + id),
		AuthMethod: access.MethodPassword, UserID: userID,
	}
}

func mustCreateSession(t *testing.T, a *AccountStore, s *access.Session) {
	t.Helper()
	if err := a.CreateSession(t.Context(), s); err != nil {
		t.Fatalf("CreateSession(%s): %v", s.ID, err)
	}
}

func testToken(hash string, purpose access.TokenPurpose, expires time.Time) access.OneTimeToken {
	return access.OneTimeToken{CreatedAt: accountsAt, ExpiresAt: expires, Hash: access.NewHash(hash), Purpose: purpose}
}

func mustCreateToken(t *testing.T, a *AccountStore, tok access.OneTimeToken) {
	t.Helper()
	if err := a.CreateOneTimeToken(t.Context(), tok); err != nil {
		t.Fatalf("CreateOneTimeToken: %v", err)
	}
}

func TestConsumeExpiredTokenAnswersNotFound(t *testing.T) {
	s, a := newAccounts(t)
	now := accountsAt
	for _, tc := range []struct {
		name    string
		expires time.Time
		found   bool
	}{
		{"expiring at now", now, false},
		{"expired before now", now.Add(-time.Second), false},
		{"expiring after now", now.Add(time.Nanosecond), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tok := testToken("consume-"+tc.name, access.PurposeRecovery, tc.expires)
			mustCreateToken(t, a, tok)
			got, found, err := a.ConsumeOneTimeToken(t.Context(), tok.Hash, now)
			if err != nil || found != tc.found {
				t.Fatalf("ConsumeOneTimeToken = found %v, %v; want %v", found, err, tc.found)
			}
			if found && !got.Hash.Equal(tok.Hash) {
				t.Error("the consumed token's hash differs from the stored one")
			}
			if _, ok := getRow(t, s, s.a.tokens, tok.Hash.Reveal()); ok {
				t.Error("the token row is still stored after the consume")
			}
		})
	}
}

func TestLiveOneTimeTokens(t *testing.T) {
	_, a := newAccounts(t)
	now := accountsAt
	// Equal-length texts, as every stored token hash is: hex SHA-256.
	invite := testToken("hash-b", access.PurposeInvite, now.Add(time.Hour))
	invite.IssuedByKey = 7
	recovery := testToken("hash-a", access.PurposeRecovery, now.Add(time.Hour))
	for _, tok := range []access.OneTimeToken{
		invite, recovery,
		testToken("hash-c", access.PurposeInvite, now.Add(-time.Hour)),
		testToken("hash-d", access.PurposeInvite, now),
	} {
		mustCreateToken(t, a, tok)
	}
	hashes := func(toks []access.OneTimeToken) []string {
		var out []string
		for _, tok := range toks {
			out = append(out, tok.Hash.Reveal())
		}
		return out
	}
	t.Run("both purposes in hash order", func(t *testing.T) {
		got, err := a.LiveOneTimeTokens(t.Context(), []access.TokenPurpose{access.PurposeInvite, access.PurposeRecovery}, now)
		if err != nil || !slices.Equal(hashes(got), []string{"hash-a", "hash-b"}) {
			t.Fatalf("LiveOneTimeTokens = %v, %v; want [hash-a hash-b]", hashes(got), err)
		}
		if got[1].IssuedByKey != 7 {
			t.Errorf("invite IssuedByKey %d, want 7", got[1].IssuedByKey)
		}
	})
	t.Run("a purposes filter", func(t *testing.T) {
		got, err := a.LiveOneTimeTokens(t.Context(), []access.TokenPurpose{access.PurposeRecovery}, now)
		if err != nil || !slices.Equal(hashes(got), []string{"hash-a"}) {
			t.Errorf("LiveOneTimeTokens([recovery]) = %v, %v; want [hash-a]", hashes(got), err)
		}
	})
}

func TestHashFieldsRoundTrip(t *testing.T) {
	s, a := newAccounts(t)
	ctx := t.Context()
	u := &access.User{Username: "ada", PasswordHash: access.NewHash("$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA")}
	if err := a.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	sess := testSession(u.ID, "s1", accountsAt)
	mustCreateSession(t, a, sess)
	key := &access.Key{KeyHash: access.NewHash("key-hash-1"), UserID: u.ID}
	if err := a.CreateAPIKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	tok := testToken("token-hash-1", access.PurposeInvite, accountsAt.Add(time.Hour))
	mustCreateToken(t, a, tok)

	gotUser, _, err := a.UserByID(ctx, u.ID)
	if err != nil || !gotUser.PasswordHash.Equal(u.PasswordHash) {
		t.Errorf("UserByID password hash Equal = %v (%v), want true", err == nil && gotUser.PasswordHash.Equal(u.PasswordHash), err)
	}
	if got, ok, err := a.SessionByHash(ctx, access.NewHash("session-s1")); err != nil || !ok || !got.TokenHash.Equal(sess.TokenHash) {
		t.Errorf("SessionByHash(an equal NewHash) = found %v, %v", ok, err)
	}
	if got, ok, err := a.APIKeyByHash(ctx, access.NewHash("key-hash-1")); err != nil || !ok || !got.KeyHash.Equal(key.KeyHash) {
		t.Errorf("APIKeyByHash(an equal NewHash) = found %v, %v", ok, err)
	}
	if got, ok, err := a.OneTimeTokenByHash(ctx, access.NewHash("token-hash-1")); err != nil || !ok || !got.Hash.Equal(tok.Hash) {
		t.Errorf("OneTimeTokenByHash(an equal NewHash) = found %v, %v", ok, err)
	}
	t.Run("rows hold the Reveal text", func(t *testing.T) {
		if row, _ := getRow(t, s, s.a.users, u.ID); row.PasswordHash != u.PasswordHash.Reveal() {
			t.Errorf("user row hash %q", row.PasswordHash)
		}
		if row, _ := getRow(t, s, s.a.sessions, access.SessionRef{UserID: u.ID, ID: "s1"}); row.TokenHash != "session-s1" {
			t.Errorf("session row hash %q", row.TokenHash)
		}
		if row, _ := getRow(t, s, s.a.keys, key.ID); row.KeyHash != "key-hash-1" {
			t.Errorf("key row hash %q", row.KeyHash)
		}
		if _, ok := getRow(t, s, s.a.tokens, "token-hash-1"); !ok {
			t.Error("no token row under the Reveal text")
		}
	})
}

// expiredSessions creates three sessions idle past an hour, of two users,
// and one live one.
func expiredSessions(t *testing.T, a *AccountStore) (expired []access.SessionRef, live access.SessionRef) {
	t.Helper()
	u1, u2 := mustCreateUser(t, a, "u1"), mustCreateUser(t, a, "u2")
	for _, s := range []*access.Session{testSession(u1.ID, "e1", accountsAt), testSession(u2.ID, "e2", accountsAt), testSession(u2.ID, "e3", accountsAt)} {
		mustCreateSession(t, a, s)
		expired = append(expired, access.SessionRef{UserID: s.UserID, ID: s.ID})
	}
	mustCreateSession(t, a, testSession(u1.ID, "live", accountsAt.Add(2*time.Hour)))
	return expired, access.SessionRef{UserID: u1.ID, ID: "live"}
}

var cleanupTimeouts = access.SessionTimeouts{Idle: time.Hour, Absolute: 7 * 24 * time.Hour}

func TestCleanupReturnsDeletedRefs(t *testing.T) {
	path := tempPath(t)
	s := mustOpen(t, path, productionConfig())
	a := s.Accounts()
	expired, live := expiredSessions(t, a)
	now := accountsAt.Add(90 * time.Minute)
	var deleted []access.SessionRef
	for i, want := range []int{2, 1, 0} {
		refs, err := a.CleanupExpiredSessions(t.Context(), now, cleanupTimeouts, 2)
		if err != nil || len(refs) != want {
			t.Fatalf("call %d: %v, %v; want %d refs", i+1, refs, err, want)
		}
		deleted = append(deleted, refs...)
		if i == 0 {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = mustOpen(t, path, productionConfig())
			a = s.Accounts()
		}
	}
	slices.SortFunc(deleted, func(x, y access.SessionRef) int { return strings.Compare(x.ID, y.ID) })
	if !slices.Equal(deleted, expired) {
		t.Errorf("refs across the calls and a reopen %v, want %v", deleted, expired)
	}
	if _, ok := getRow(t, s, s.a.sessions, live); !ok {
		t.Error("the live session was deleted")
	}
	if n := countRows(t, s, s.a.sessions, nil); n != 1 {
		t.Errorf("%d sessions left, want the live one", n)
	}
}

func TestCleanupHonoursSessionCaps(t *testing.T) {
	s, a := newAccounts(t)
	u := mustCreateUser(t, a, "capped")
	capped := testSession(u.ID, "capped", accountsAt)
	capped.IdleCap = 30 * time.Minute
	mustCreateSession(t, a, capped)
	mustCreateSession(t, a, testSession(u.ID, "uncapped", accountsAt))
	refs, err := a.CleanupExpiredSessions(t.Context(), accountsAt.Add(31*time.Minute), cleanupTimeouts, 10)
	if err != nil || !slices.Equal(refs, []access.SessionRef{{UserID: u.ID, ID: "capped"}}) {
		t.Fatalf("CleanupExpiredSessions = %v, %v; want the capped session alone", refs, err)
	}
	if _, ok := getRow(t, s, s.a.sessions, access.SessionRef{UserID: u.ID, ID: "uncapped"}); !ok {
		t.Error("the uncapped session was deleted")
	}
}

func TestActivityNeverMovesBackwards(t *testing.T) {
	_, a := newAccounts(t)
	ctx := t.Context()
	u := mustCreateUser(t, a, "ada")
	sess := testSession(u.ID, "s1", accountsAt)
	mustCreateSession(t, a, sess)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 5, h, m, 0, 0, time.UTC) }
	activity := func() (time.Time, string) {
		t.Helper()
		got, _, err := a.SessionByHash(ctx, sess.TokenHash)
		if err != nil {
			t.Fatal(err)
		}
		return got.LastActivity, got.LastIP
	}
	for _, act := range []access.SessionActivity{{At: at(12, 30), IP: "192.0.2.30"}, {At: at(12, 0), IP: "192.0.2.0"}, {At: at(12, 30), IP: "192.0.2.31"}} {
		if err := a.UpdateSessionActivity(ctx, sess.TokenHash, act); err != nil {
			t.Fatalf("UpdateSessionActivity(%v): %v", act.At, err)
		}
	}
	if last, ip := activity(); !last.Equal(at(12, 30)) || ip != "192.0.2.30" {
		t.Errorf("activity %v %s, want 12:30 from 192.0.2.30", last, ip)
	}
	t.Run("rotation keeps a later stored stamp", func(t *testing.T) {
		next := *testSession(u.ID, "s1", at(12, 10))
		next.TokenHash = access.NewHash("session-s1-rotated")
		if err := a.RotateSession(ctx, account.Rotation{ID: "s1", UserID: u.ID, Next: next}); err != nil {
			t.Fatal(err)
		}
		sess.TokenHash = next.TokenHash
		if last, ip := activity(); !last.Equal(at(12, 30)) || ip != "192.0.2.30" {
			t.Errorf("activity after the rotation %v %s, want 12:30 from 192.0.2.30", last, ip)
		}
	})
	t.Run("rotation at an equal stamp writes Next's IP", func(t *testing.T) {
		next := *testSession(u.ID, "s1", at(12, 30))
		next.TokenHash, next.LastIP = access.NewHash("session-s1-tied"), "192.0.2.32"
		if err := a.RotateSession(ctx, account.Rotation{ID: "s1", UserID: u.ID, Next: next}); err != nil {
			t.Fatal(err)
		}
		sess.TokenHash = next.TokenHash
		if last, ip := activity(); !last.Equal(at(12, 30)) || ip != "192.0.2.32" {
			t.Errorf("activity after the tied rotation %v %s, want 12:30 from 192.0.2.32", last, ip)
		}
	})
	t.Run("a key keeps its later use", func(t *testing.T) {
		key := &access.Key{KeyHash: access.NewHash("k1"), UserID: u.ID}
		if err := a.CreateAPIKey(ctx, key); err != nil {
			t.Fatal(err)
		}
		for _, use := range []time.Time{at(12, 30), at(12, 0)} {
			if err := a.TouchAPIKey(ctx, key.ID, use); err != nil {
				t.Fatal(err)
			}
		}
		if got, _, err := a.APIKeyByHash(ctx, key.KeyHash); err != nil || !got.LastUsedAt.Equal(at(12, 30)) {
			t.Errorf("LastUsedAt %v (%v), want 12:30", got.LastUsedAt, err)
		}
	})
}

func TestKeyLineageAndAttribution(t *testing.T) {
	path := tempPath(t)
	s := mustOpen(t, path, productionConfig())
	a := s.Accounts()
	ctx := t.Context()
	// ada holds the highest user ID when she is deleted, so an allocator
	// taking the highest live ID plus one would hand hers out again.
	other := mustCreateUser(t, a, "bob")
	u := mustCreateUser(t, a, "ada")
	key := &access.Key{KeyHash: access.NewHash("k1"), UserID: u.ID, LineageIDs: []int64{11, 12}}
	if err := a.CreateAPIKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(key.LineageIDs, []int64{11, 12, key.ID}) {
		t.Errorf("LineageIDs %v, want [11 12 %d]", key.LineageIDs, key.ID)
	}
	var ids []int64
	for _, owner := range []int64{u.ID, other.ID, u.ID} {
		rec := &access.KeyAttribution{At: accountsAt, Action: access.AttributionCreated, KeyLineage: key.LineageIDs, UserID: owner}
		if err := a.CreateKeyAttribution(ctx, rec); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rec.ID)
	}
	if !slices.IsSorted(ids) || ids[0] == ids[1] || ids[1] == ids[2] {
		t.Errorf("attribution IDs %v, want increasing", ids)
	}
	listed := func() []int64 {
		t.Helper()
		recs, err := a.ListKeyAttributions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, r := range recs {
			out = append(out, r.ID)
		}
		return out
	}
	if err := a.DeleteAPIKey(ctx, access.KeyRef{ID: key.ID, UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if got := listed(); !slices.Equal(got, ids) {
		t.Errorf("attributions after deleting the key %v, want %v", got, ids)
	}
	t.Run("tokens issued by a key", func(t *testing.T) {
		for i, issuer := range []int64{5, 5, 6, 0} {
			tok := testToken("issued-"+string(rune('a'+i)), access.PurposeInvite, accountsAt.Add(time.Hour))
			tok.IssuedByKey = issuer
			mustCreateToken(t, a, tok)
		}
		for _, tc := range []struct {
			keys []int64
			want int
		}{{nil, 0}, {[]int64{0}, 0}, {[]int64{5}, 2}, {[]int64{6, 9}, 1}} {
			if n, err := a.DeleteOneTimeTokensIssuedBy(ctx, tc.keys); err != nil || n != tc.want {
				t.Errorf("DeleteOneTimeTokensIssuedBy(%v) = %d, %v; want %d", tc.keys, n, err, tc.want)
			}
		}
		if n := countRows(t, s, s.a.tokens, nil); n != 1 {
			t.Errorf("%d tokens left, want the one no key issued", n)
		}
	})
	if err := a.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if got := listed(); !slices.Equal(got, ids[1:2]) {
		t.Errorf("attributions after deleting %d: %v, want the other user's %v", u.ID, got, ids[1:2])
	}
	t.Run("no ID is assigned twice across a reopen", func(t *testing.T) {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s = mustOpen(t, path, productionConfig())
		a = s.Accounts()
		u2 := mustCreateUser(t, a, "carol")
		k2 := &access.Key{KeyHash: access.NewHash("k2"), UserID: u2.ID}
		if err := a.CreateAPIKey(ctx, k2); err != nil {
			t.Fatal(err)
		}
		rec := &access.KeyAttribution{Action: access.AttributionEnabled, UserID: u2.ID}
		if err := a.CreateKeyAttribution(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if u2.ID <= u.ID || k2.ID <= key.ID || rec.ID <= ids[2] {
			t.Errorf("after deletes and a reopen: user %d, key %d, attribution %d; want above %d, %d, %d", u2.ID, k2.ID, rec.ID, u.ID, key.ID, ids[2])
		}
	})
}

func TestUniqueConflictsMapToSentinels(t *testing.T) {
	_, a := newAccounts(t)
	ctx := t.Context()
	idp := access.OIDCIdentity{Issuer: "https://idp.example", Subject: "s"}
	ada := &access.User{Username: "ada", OIDC: idp, WebAuthnHandle: []byte("h1")}
	if err := a.CreateUser(ctx, ada); err != nil {
		t.Fatal(err)
	}
	bob := mustCreateUser(t, a, "bob")
	cred := &access.PasskeyCredential{UserID: ada.ID, CredentialID: []byte("c1"), RawFlags: 1}
	if err := a.CreatePasskey(ctx, cred); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateAPIKey(ctx, &access.Key{KeyHash: access.NewHash("k1"), UserID: ada.ID}); err != nil {
		t.Fatal(err)
	}
	sentinels := []error{account.ErrUsernameTaken, account.ErrOIDCIdentityTaken, account.ErrDuplicateCredential}
	for _, tc := range []struct {
		name string
		call func() error
		want error
	}{
		{"create, username", func() error { return a.CreateUser(ctx, &access.User{Username: "ada"}) }, account.ErrUsernameTaken},
		{"update, username", func() error { b := *bob; b.Username = "ada"; return a.UpdateUser(ctx, &b) }, account.ErrUsernameTaken},
		{"create, identity", func() error { return a.CreateUser(ctx, &access.User{Username: "carol", OIDC: idp}) }, account.ErrOIDCIdentityTaken},
		{"update, identity", func() error { b := *bob; b.OIDC = idp; return a.UpdateUser(ctx, &b) }, account.ErrOIDCIdentityTaken},
		{"passkey, another user's credential", func() error {
			return a.CreatePasskey(ctx, &access.PasskeyCredential{UserID: bob.ID, CredentialID: []byte("c1")})
		}, account.ErrDuplicateCredential},
		{"handle", func() error { return a.CreateUser(ctx, &access.User{Username: "dave", WebAuthnHandle: []byte("h1")}) }, nil},
		{"key hash", func() error { return a.CreateAPIKey(ctx, &access.Key{KeyHash: access.NewHash("k1"), UserID: bob.ID}) }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("the conflicting write succeeded")
			}
			for _, s := range sentinels {
				if errors.Is(err, s) != (s == tc.want) {
					t.Errorf("%v: matches %v %v, want %v", err, s, errors.Is(err, s), s == tc.want)
				}
			}
		})
	}
	if n, err := a.UserCount(ctx); err != nil || n != 2 {
		t.Errorf("UserCount after the refusals = %d, %v; want 2", n, err)
	}
}

func TestCreateNeverOverwrites(t *testing.T) {
	s, a := newAccounts(t)
	ctx := t.Context()
	u := mustCreateUser(t, a, "ada")
	tok := testToken("dup", access.PurposeInvite, accountsAt.Add(time.Hour))
	mustCreateToken(t, a, tok)
	again := tok
	again.Purpose = access.PurposeRecovery
	if err := a.CreateOneTimeToken(ctx, again); err == nil {
		t.Error("a second token of the same hash was stored")
	}
	if row, _ := getRow(t, s, s.a.tokens, "dup"); row.Purpose != string(access.PurposeInvite) {
		t.Errorf("token purpose %q after the refused create, want invite", row.Purpose)
	}
	mustCreateSession(t, a, testSession(u.ID, "s1", accountsAt))
	dup := testSession(u.ID, "s1", accountsAt)
	dup.TokenHash, dup.Label = access.NewHash("other-hash"), "overwritten"
	if err := a.CreateSession(ctx, dup); err == nil {
		t.Error("a second session of the same user and ID was stored")
	}
	if row, _ := getRow(t, s, s.a.sessions, access.SessionRef{UserID: u.ID, ID: "s1"}); row.Label != "" || row.TokenHash != "session-s1" {
		t.Errorf("session row %+v after the refused create, want the first", row)
	}
}

func TestSessionIDUniqueAcrossUsers(t *testing.T) {
	s, a := newAccounts(t)
	u1, u2 := mustCreateUser(t, a, "u1"), mustCreateUser(t, a, "u2")
	mustCreateSession(t, a, testSession(u1.ID, "shared", accountsAt))
	other := testSession(u2.ID, "shared", accountsAt)
	other.TokenHash = access.NewHash("another-hash")
	err := a.CreateSession(t.Context(), other)
	if err == nil || errors.Is(err, account.ErrNotFound) {
		t.Fatalf("CreateSession of another user's session ID = %v, want a backend error", err)
	}
	if n := countRows(t, s, s.a.sessions, nil); n != 1 {
		t.Errorf("%d sessions, want 1", n)
	}
}

func TestUpdateAbsentIsNotFound(t *testing.T) {
	s, a := newAccounts(t)
	ctx := t.Context()
	u := mustCreateUser(t, a, "ada")
	ghost := *u
	ghost.ID = u.ID + 100
	next := *testSession(u.ID, "ghost", accountsAt)
	for name, call := range map[string]func() error{
		"UpdateUser":              func() error { return a.UpdateUser(ctx, &ghost) },
		"UpdateSessionLabel":      func() error { return a.UpdateSessionLabel(ctx, access.SessionRef{UserID: u.ID, ID: "ghost"}, "x") },
		"RotateSession":           func() error { return a.RotateSession(ctx, account.Rotation{ID: "ghost", UserID: u.ID, Next: next}) },
		"RenamePasskey":           func() error { return a.RenamePasskey(ctx, access.PasskeyRef{UserID: u.ID, ID: 1}, "x") },
		"DeletePasskey":           func() error { return a.DeletePasskey(ctx, access.PasskeyRef{UserID: u.ID, ID: 1}) },
		"DeleteSession":           func() error { return a.DeleteSession(ctx, access.SessionRef{UserID: u.ID, ID: "ghost"}) },
		"DeleteAPIKey":            func() error { return a.DeleteAPIKey(ctx, access.KeyRef{UserID: u.ID, ID: 1}) },
		"DeleteUser":              func() error { return a.DeleteUser(ctx, ghost.ID) },
		"UpdatePasskeyAfterLogin": func() error { return a.UpdatePasskeyAfterLogin(ctx, u.ID, []byte("none"), access.PasskeyLoginUpdate{}) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, account.ErrNotFound) {
				t.Errorf("%s on an absent row = %v, want ErrNotFound", name, err)
			}
		})
	}
	t.Run("absent stamps write nothing", func(t *testing.T) {
		if err := a.UpdateSessionActivity(ctx, access.NewHash("none"), access.SessionActivity{At: accountsAt}); err != nil {
			t.Error(err)
		}
		if err := a.TouchAPIKey(ctx, 99, accountsAt); err != nil {
			t.Error(err)
		}
		if n, m := countRows(t, s, s.a.activity, nil), countRows(t, s, s.a.keyUse, nil); n != 0 || m != 0 {
			t.Errorf("%d activity and %d key use rows, want none", n, m)
		}
	})
}

func TestIDAboveMaxSafeFails(t *testing.T) {
	cases := map[string]func(*testing.T, *AccountStore, int64) (int64, error){
		"user": func(t *testing.T, a *AccountStore, _ int64) (int64, error) {
			u := &access.User{Username: "u"}
			err := a.CreateUser(t.Context(), u)
			return u.ID, err
		},
		"key": func(t *testing.T, a *AccountStore, owner int64) (int64, error) {
			k := &access.Key{KeyHash: access.NewHash("k"), UserID: owner}
			err := a.CreateAPIKey(t.Context(), k)
			return k.ID, err
		},
		"passkey": func(t *testing.T, a *AccountStore, owner int64) (int64, error) {
			p := &access.PasskeyCredential{UserID: owner, CredentialID: []byte("c")}
			err := a.CreatePasskey(t.Context(), p)
			return p.ID, err
		},
		"attribution": func(t *testing.T, a *AccountStore, owner int64) (int64, error) {
			rec := &access.KeyAttribution{UserID: owner, Action: access.AttributionCreated}
			err := a.CreateKeyAttribution(t.Context(), rec)
			return rec.ID, err
		},
	}
	for name, create := range cases {
		t.Run(name, func(t *testing.T) {
			s, a := newAccounts(t)
			owner := mustCreateUser(t, a, "owner").ID
			before := headRev(t, s)
			seeded := func(n uint64) {
				drawID = func(*bolttable.Tx, func(*bolttable.Tx) (uint64, error)) (uint64, error) { return n, nil }
			}
			t.Cleanup(func() {
				drawID = func(tx *bolttable.Tx, next func(*bolttable.Tx) (uint64, error)) (uint64, error) { return next(tx) }
			})
			seeded(maxID + 1)
			if id, err := create(t, a, owner); !errors.Is(err, errIDRange) || id != 0 {
				t.Errorf("create at 2^53 = id %d, %v; want errIDRange and no id", id, err)
			}
			if rev := headRev(t, s); rev != before {
				t.Errorf("head %d after the refused create, want %d: nothing written", rev, before)
			}
			seeded(maxID)
			if id, err := create(t, a, owner); err != nil || id != maxID {
				t.Errorf("create at 2^53 - 1 = id %d, %v; want that id", id, err)
			}
		})
	}
}

func TestNoOrphanCompanionRows(t *testing.T) {
	s, a := newAccounts(t)
	ctx := t.Context()
	u := mustCreateUser(t, a, "ada")
	mustCreateSession(t, a, testSession(u.ID, "deleted", accountsAt))
	mustCreateSession(t, a, testSession(u.ID, "expired", accountsAt))
	cred := &access.PasskeyCredential{UserID: u.ID, CredentialID: []byte("c1"), RawFlags: 1}
	if err := a.CreatePasskey(ctx, cred); err != nil {
		t.Fatal(err)
	}
	key := &access.Key{KeyHash: access.NewHash("k1"), UserID: u.ID}
	if err := a.CreateAPIKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	companions := func() (activity, passkeyUse, keyUse int) {
		return countRows(t, s, s.a.activity, nil), countRows(t, s, s.a.passkeyUse, nil), countRows(t, s, s.a.keyUse, nil)
	}
	if err := a.DeleteSession(ctx, access.SessionRef{UserID: u.ID, ID: "deleted"}); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := companions(); n != 1 {
		t.Errorf("after a delete: %d activity rows, want 1", n)
	}
	if _, err := a.CleanupExpiredSessions(ctx, accountsAt.Add(2*time.Hour), cleanupTimeouts, 10); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := companions(); n != 0 {
		t.Errorf("after an expiry: %d activity rows, want 0", n)
	}
	if err := a.DeletePasskey(ctx, access.PasskeyRef{UserID: u.ID, ID: cred.ID}); err != nil {
		t.Fatal(err)
	}
	if _, n, _ := companions(); n != 0 {
		t.Errorf("after a passkey delete: %d passkey use rows, want 0", n)
	}
	if err := a.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, n := companions(); n != 0 {
		t.Errorf("after the user's delete: %d key use rows, want 0", n)
	}
}

func TestStampsReserveNoRevision(t *testing.T) {
	s, a := newAccounts(t)
	ctx := t.Context()
	u := mustCreateUser(t, a, "ada")
	sess := testSession(u.ID, "s1", accountsAt)
	mustCreateSession(t, a, sess)
	cred := &access.PasskeyCredential{UserID: u.ID, CredentialID: []byte("c1"), RawFlags: 1}
	if err := a.CreatePasskey(ctx, cred); err != nil {
		t.Fatal(err)
	}
	key := &access.Key{KeyHash: access.NewHash("k1"), UserID: u.ID}
	if err := a.CreateAPIKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	for name, stamp := range map[string]func() error{
		"a wrong password": func() error {
			return a.PutAuthFailure(ctx, account.FailureCount{Name: "n", Method: access.MethodPassword, Count: 1})
		},
		"session activity": func() error {
			return a.UpdateSessionActivity(ctx, sess.TokenHash, access.SessionActivity{At: accountsAt.Add(time.Minute), IP: "192.0.2.1"})
		},
		"a passkey sign-in": func() error {
			return a.UpdatePasskeyAfterLogin(ctx, u.ID, cred.CredentialID, access.PasskeyLoginUpdate{At: accountsAt.Add(time.Minute), SignCount: 2, RawFlags: 5})
		},
		"a key use": func() error { return a.TouchAPIKey(ctx, key.ID, accountsAt.Add(time.Minute)) },
	} {
		t.Run(name, func(t *testing.T) {
			before := headRev(t, s)
			if err := stamp(); err != nil {
				t.Fatal(err)
			}
			if rev := headRev(t, s); rev != before {
				t.Errorf("head moved from %d to %d", before, rev)
			}
		})
	}
}

// corruptStored rewrites the one occurrence of old in the closed store
// file at path with new, of the same length.
func corruptStored(t *testing.T, path, old, new string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), old); n != 1 {
		t.Fatalf("%q occurs %d times in the file, want once", old, n)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptTokenRowHidesHash(t *testing.T) {
	path := tempPath(t)
	s := mustOpen(t, path, productionConfig())
	const hash = "secret-token-hash-text"
	mustCreateToken(t, s.Accounts(), testToken(hash, access.PurposeRecovery, accountsAt.Add(time.Hour)))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	corruptStored(t, path, `"purpose":"recovery"`, `"purpose":9recovery"`)
	rec := capture.Default(t)
	s = mustOpen(t, path, productionConfig())
	_, _, err := s.Accounts().OneTimeTokenByHash(t.Context(), access.NewHash(hash))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("read of the corrupt row: %v, want statedb's ErrCorrupt", err)
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), hash) {
			t.Errorf("error chain link %q holds the hash", e.Error())
		}
	}
	in, err := s.Inspect(t.Context(), InspectOptions{Verify: true})
	if err != nil && strings.Contains(err.Error(), hash) {
		t.Errorf("Inspect error %q holds the hash", err)
	}
	if strings.Contains(strings.Join(rec.Messages(), "\n"), hash) || strings.Contains(fmt.Sprintf("%+v", in), hash) {
		t.Error("a log line or the inspection holds the hash")
	}
}

func TestDeleteSession1KeepsSession10Activity(t *testing.T) {
	s, a := newAccounts(t)
	u := mustCreateUser(t, a, "ada")
	mustCreateSession(t, a, testSession(u.ID, "1", accountsAt))
	mustCreateSession(t, a, testSession(u.ID, "10", accountsAt.Add(time.Minute)))
	if err := a.DeleteSession(t.Context(), access.SessionRef{UserID: u.ID, ID: "1"}); err != nil {
		t.Fatal(err)
	}
	act, ok := getRow(t, s, s.a.activity, access.SessionRef{UserID: u.ID, ID: "10"})
	if !ok || !act.LastActivity.Equal(accountsAt.Add(time.Minute)) {
		t.Errorf("session 10's activity after deleting session 1: %+v, found %v", act, ok)
	}
}

func TestCreateRotateWritesActivity(t *testing.T) {
	s, a := newAccounts(t)
	ctx := t.Context()
	u := mustCreateUser(t, a, "ada")
	sess := testSession(u.ID, "s1", accountsAt)
	sess.LastIP = "192.0.2.1"
	mustCreateSession(t, a, sess)
	ref := access.SessionRef{UserID: u.ID, ID: "s1"}
	if act, _ := getRow(t, s, s.a.activity, ref); !act.LastActivity.Equal(accountsAt) || act.LastIP != "192.0.2.1" {
		t.Errorf("activity after CreateSession %+v", act)
	}
	next := *sess
	next.TokenHash, next.LastActivity, next.LastIP = access.NewHash("rotated"), accountsAt.Add(time.Hour), "192.0.2.2"
	if err := a.RotateSession(ctx, account.Rotation{ID: "s1", UserID: u.ID, ExpectHash: sess.TokenHash, Next: next}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := a.SessionByHash(ctx, next.TokenHash)
	if err != nil || !ok || !got.LastActivity.Equal(next.LastActivity) || got.LastIP != "192.0.2.2" {
		t.Errorf("session after RotateSession: %+v, found %v, %v", got, ok, err)
	}
	if _, ok, _ := a.SessionByHash(ctx, sess.TokenHash); ok {
		t.Error("the old hash still finds the session")
	}
	t.Run("a stale ExpectHash", func(t *testing.T) {
		if err := a.RotateSession(ctx, account.Rotation{ID: "s1", UserID: u.ID, ExpectHash: sess.TokenHash, Next: next}); !errors.Is(err, account.ErrNotFound) {
			t.Errorf("RotateSession with the old hash = %v, want ErrNotFound", err)
		}
	})
}

func TestTokenChangesMarkAccounts(t *testing.T) {
	s, a := newAccounts(t)
	ctx := t.Context()
	mustCreateUser(t, a, "ada")
	tok := testToken("t1", access.PurposeInvite, accountsAt.Add(time.Hour))
	// consume reads the token create stored, so the cases run in order.
	for _, tc := range []struct {
		name   string
		change func() error
	}{
		{"create", func() error { return a.CreateOneTimeToken(ctx, tok) }},
		{"consume", func() error {
			_, found, err := a.ConsumeOneTimeToken(ctx, tok.Hash, accountsAt)
			if err == nil && !found {
				err = errors.New("the created token was not found")
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := headRev(t, s)
			if err := tc.change(); err != nil {
				t.Fatal(err)
			}
			if d := since(t, s, before); !slices.Equal(d.Marks, []string{MarkAccounts}) {
				t.Errorf("marks %v, want [%s]", d.Marks, MarkAccounts)
			}
		})
	}
	t.Run("a delete matching nothing", func(t *testing.T) {
		before := headRev(t, s)
		if _, err := a.DeleteOneTimeTokens(ctx, access.PurposeInvite, 0); err != nil {
			t.Fatal(err)
		}
		if rev := headRev(t, s); rev != before {
			t.Errorf("head moved from %d to %d", before, rev)
		}
	})
}
