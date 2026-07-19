package authstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cplieger/auth/v2"
	"go.etcd.io/bbolt"
)

// This file unit-tests the destructive-auth migration seam (ResetPreserving,
// Requirement 3.2): the explicit-transform refusal, the identity round-trip
// (rows, IDs, ownership links, uniqueness indexes, bucket sequences), a real
// transform, and the fail-closed restore guards (duplicate rows, dangling
// owners, invalid IDs) whose errors abort the enclosing transaction so the
// pre-step state survives. The cross-domain fixtures (core untouched by an
// auth bump) live in boltstore's migrate_reset_test.go, where the ladder runs.

// seededStore builds a Store over a boltstore-bootstrapped file holding two
// users (one with an OIDC identity), a passkey, and an API key.
func seededStore(t *testing.T) (*Store, []auth.User, auth.PasskeyCredential, auth.Key) {
	t.Helper()
	ctx := context.Background()
	s := New(openShared(t, bootstrappedFile(t)))

	alice := &auth.User{Username: "alice", Email: "alice@example.com", Role: auth.RoleAdmin, PasswordHash: "hash-a", Enabled: true, OIDCIssuer: "https://idp", OIDCSub: "sub-alice"}
	bob := &auth.User{Username: "bob", Role: auth.RoleUser, PasswordHash: "hash-b", Enabled: true}
	for _, u := range []*auth.User{alice, bob} {
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatalf("CreateUser(%s): %v", u.Username, err)
		}
	}
	pk := &auth.PasskeyCredential{
		UserID: alice.ID, Name: "yubi", CredentialID: []byte{0x01, 0x00, 0x02},
		PublicKey: []byte{0xAA}, SignCount: 9, UserVerified: true,
	}
	if err := s.CreatePasskey(ctx, pk); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	key := &auth.Key{UserID: bob.ID, KeyHash: "hash-key-1", KeyPrefix: "sfx_", KeySuffix: "beef", Label: "cli"}
	if err := s.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return s, []auth.User{*alice, *bob}, *pk, *key
}

// resetWith runs ResetPreserving with the given transform in one write
// transaction on the store's shared handle, returning the transaction error —
// exactly how a ladder step body would run it.
func resetWith(s *Store, transform RowsetTransform) error {
	return s.update(func(tx *bbolt.Tx) error {
		return ResetPreserving(tx, transform)
	})
}

// TestResetPreserving_refusesNilTransform proves the explicit-transform
// contract: a destructive step must state its rewrite; nil is refused before
// anything is touched.
func TestResetPreserving_refusesNilTransform(t *testing.T) {
	s, users, _, _ := seededStore(t)
	err := resetWith(s, nil)
	if err == nil || !strings.Contains(err.Error(), "transform") {
		t.Fatalf("ResetPreserving(nil) = %v, want the explicit-transform refusal", err)
	}
	// Nothing was touched.
	u, gerr := s.GetUserByUsername(context.Background(), users[0].Username)
	if gerr != nil || u == nil || u.ID != users[0].ID {
		t.Errorf("user after refused reset = (%+v, %v), want untouched", u, gerr)
	}
}

// TestResetPreserving_identityRoundTrip proves the identity transform brings
// every row back: same IDs, working uniqueness indexes and lookups, ownership
// links via the user-scoped index walks, and live sequences that never
// re-allocate a preserved id.
func TestResetPreserving_identityRoundTrip(t *testing.T) {
	s, users, pk, key := seededStore(t)
	ctx := context.Background()

	if err := resetWith(s, func(*Rowset) error { return nil }); err != nil {
		t.Fatalf("ResetPreserving(identity): %v", err)
	}

	for _, want := range users {
		u, err := s.GetUserByUsername(ctx, want.Username)
		if err != nil || u == nil {
			t.Fatalf("GetUserByUsername(%s) = (%v, %v), want restored", want.Username, u, err)
		}
		if u.ID != want.ID || u.PasswordHash != want.PasswordHash || u.Role != want.Role || u.Enabled != want.Enabled {
			t.Errorf("restored user %s = %+v, want identical to %+v", want.Username, u, want)
		}
	}
	if u, err := s.GetUserByOIDCSub(ctx, "https://idp", "sub-alice"); err != nil || u == nil || u.ID != users[0].ID {
		t.Errorf("GetUserByOIDCSub = (%+v, %v), want alice via the rebuilt ix_user_oidc", u, err)
	}

	pks, err := s.GetPasskeysByUserID(ctx, users[0].ID)
	if err != nil || len(pks) != 1 {
		t.Fatalf("GetPasskeysByUserID = (%+v, %v), want the restored passkey", pks, err)
	}
	if pks[0].ID != pk.ID || string(pks[0].CredentialID) != string(pk.CredentialID) || pks[0].SignCount != pk.SignCount {
		t.Errorf("restored passkey = %+v, want identical to %+v", pks[0], pk)
	}
	keys, err := s.ListAPIKeysByUserID(ctx, users[1].ID)
	if err != nil || len(keys) != 1 || keys[0].ID != key.ID || keys[0].KeyHash != key.KeyHash {
		t.Errorf("restored api keys = (%+v, %v), want identical to %+v", keys, err, key)
	}

	// Uniqueness indexes are live again.
	if err := s.CreateUser(ctx, &auth.User{Username: "ALICE", Role: auth.RoleUser}); !errors.Is(err, errConflict) {
		t.Errorf("duplicate username after reset = %v, want errConflict", err)
	}
	// Sequences: fresh rows allocate past the preserved ids.
	fresh := &auth.User{Username: "carol", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, fresh); err != nil {
		t.Fatalf("CreateUser(carol): %v", err)
	}
	if fresh.ID <= users[1].ID {
		t.Errorf("fresh user id = %d, want > %d (sequence preserved)", fresh.ID, users[1].ID)
	}
	freshPk := &auth.PasskeyCredential{UserID: fresh.ID, Name: "n", CredentialID: []byte{0x09}, PublicKey: []byte{0x01}}
	if err := s.CreatePasskey(ctx, freshPk); err != nil {
		t.Fatalf("CreatePasskey(fresh): %v", err)
	}
	if freshPk.ID <= pk.ID {
		t.Errorf("fresh passkey id = %d, want > %d (sequence preserved)", freshPk.ID, pk.ID)
	}
}

// TestResetPreserving_appliesTransform proves the transform's rewrite is what
// gets restored: a renamed user is findable under the new name only, and a
// dropped API key (with its owner's other rows kept) is gone.
func TestResetPreserving_appliesTransform(t *testing.T) {
	s, users, _, _ := seededStore(t)
	ctx := context.Background()

	err := resetWith(s, func(rs *Rowset) error {
		for i := range rs.Users {
			if rs.Users[i].Username == "alice" {
				rs.Users[i].Username = "alice-v2"
			}
		}
		rs.Keys = nil // the v2 schema drops every API key
		return nil
	})
	if err != nil {
		t.Fatalf("ResetPreserving(transform): %v", err)
	}

	if u, err := s.GetUserByUsername(ctx, "alice"); err != nil || u != nil {
		t.Errorf("old username still resolves: (%+v, %v)", u, err)
	}
	u, err := s.GetUserByUsername(ctx, "alice-v2")
	if err != nil || u == nil || u.ID != users[0].ID {
		t.Errorf("renamed user = (%+v, %v), want alice's id under the new name", u, err)
	}
	keys, err := s.ListAPIKeysByUserID(ctx, users[1].ID)
	if err != nil || len(keys) != 0 {
		t.Errorf("api keys after dropping transform = (%+v, %v), want none", keys, err)
	}
}

// TestResetPreserving_sequenceBumpsPastTransformedIDs proves the
// fresh-allocation half of the restore contract for the credential families:
// when a transform moves a passkey or API key to a surrogate id ABOVE the
// captured pre-reset sequence, the restored bucket sequence is bumped past it,
// so a post-migration create can never re-allocate a restored id.
func TestResetPreserving_sequenceBumpsPastTransformedIDs(t *testing.T) {
	s, users, _, _ := seededStore(t)
	ctx := context.Background()

	err := resetWith(s, func(rs *Rowset) error {
		rs.Passkeys[0].ID = 100
		rs.Keys[0].ID = 60
		return nil
	})
	if err != nil {
		t.Fatalf("ResetPreserving(raise ids): %v", err)
	}

	// The transformed ids are what got restored.
	pks, err := s.GetPasskeysByUserID(ctx, users[0].ID)
	if err != nil || len(pks) != 1 || pks[0].ID != 100 {
		t.Fatalf("restored passkeys = (%+v, %v), want the transformed id 100", pks, err)
	}
	keys, err := s.ListAPIKeysByUserID(ctx, users[1].ID)
	if err != nil || len(keys) != 1 || keys[0].ID != 60 {
		t.Fatalf("restored api keys = (%+v, %v), want the transformed id 60", keys, err)
	}

	// Fresh allocations land strictly past the raised ids.
	freshPk := &auth.PasskeyCredential{UserID: users[0].ID, Name: "n", CredentialID: []byte{0x09}, PublicKey: []byte{0x01}}
	if err := s.CreatePasskey(ctx, freshPk); err != nil {
		t.Fatalf("CreatePasskey(fresh): %v", err)
	}
	if freshPk.ID <= 100 {
		t.Errorf("fresh passkey id = %d, want > 100 (sequence must bump past transformed ids)", freshPk.ID)
	}
	freshKey := &auth.Key{UserID: users[1].ID, KeyHash: "hash-key-fresh", KeyPrefix: "sfx_", KeySuffix: "cafe", Label: "fresh"}
	if err := s.CreateAPIKey(ctx, freshKey); err != nil {
		t.Fatalf("CreateAPIKey(fresh): %v", err)
	}
	if freshKey.ID <= 60 {
		t.Errorf("fresh api key id = %d, want > 60 (sequence must bump past transformed ids)", freshKey.ID)
	}
}

// TestResetPreserving_failClosedGuards proves the restore refuses a rowset
// that would corrupt the domain — and because the refusal errors the
// enclosing transaction, the pre-step state survives untouched.
func TestResetPreserving_failClosedGuards(t *testing.T) {
	cases := []struct {
		name      string
		transform RowsetTransform
		wantErr   string
	}{
		{
			name: "duplicate-username",
			transform: func(rs *Rowset) error {
				for i := range rs.Users {
					rs.Users[i].Username = "same"
				}
				return nil
			},
			wantErr: "duplicate username",
		},
		{
			name: "duplicate-user-id",
			transform: func(rs *Rowset) error {
				for i := range rs.Users {
					rs.Users[i].ID = 1
				}
				return nil
			},
			wantErr: "duplicate surrogate id",
		},
		{
			name: "invalid-user-id",
			transform: func(rs *Rowset) error {
				rs.Users[0].ID = 0
				return nil
			},
			wantErr: "invalid surrogate id",
		},
		{
			name: "invalid-passkey-id",
			transform: func(rs *Rowset) error {
				rs.Passkeys[0].ID = 0
				return nil
			},
			wantErr: "restore passkey \"yubi\" (user 1): invalid surrogate id",
		},
		{
			name: "duplicate-passkey-id",
			transform: func(rs *Rowset) error {
				dup := rs.Passkeys[0]
				dup.CredentialID = []byte{0x0F} // unique primary key; only the surrogate id collides
				rs.Passkeys = append(rs.Passkeys, dup)
				return nil
			},
			wantErr: "restore passkey \"yubi\": duplicate surrogate id",
		},
		{
			name: "negative-key-id",
			transform: func(rs *Rowset) error {
				rs.Keys[0].ID = -3
				return nil
			},
			wantErr: "restore api key \"cli\" (user 2): invalid surrogate id",
		},
		{
			name: "duplicate-key-id",
			transform: func(rs *Rowset) error {
				dup := rs.Keys[0]
				dup.KeyHash = "hash-key-other" // unique primary key; only the surrogate id collides
				rs.Keys = append(rs.Keys, dup)
				return nil
			},
			wantErr: "restore api key \"cli\": duplicate surrogate id",
		},
		{
			name: "dangling-passkey-owner",
			transform: func(rs *Rowset) error {
				rs.Users = rs.Users[1:] // drop alice but keep her passkey
				return nil
			},
			wantErr: "dangling credential",
		},
		{
			name: "dangling-key-owner",
			transform: func(rs *Rowset) error {
				rs.Users = rs.Users[:1] // drop bob but keep his API key
				return nil
			},
			wantErr: "dangling credential",
		},
		{
			name: "empty-credential-id",
			transform: func(rs *Rowset) error {
				rs.Passkeys[0].CredentialID = nil
				return nil
			},
			wantErr: "empty credential id",
		},
		{
			name: "empty-key-hash",
			transform: func(rs *Rowset) error {
				rs.Keys[0].KeyHash = ""
				return nil
			},
			wantErr: "empty key hash",
		},
		{
			name:      "transform-error",
			transform: func(*Rowset) error { return errors.New("boom") },
			wantErr:   "boom",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, users, pk, key := seededStore(t)
			ctx := context.Background()

			err := resetWith(s, tc.transform)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ResetPreserving = %v, want error containing %q", err, tc.wantErr)
			}

			// The failed transaction rolled back: everything is still there.
			u, gerr := s.GetUserByUsername(ctx, "alice")
			if gerr != nil || u == nil || u.ID != users[0].ID {
				t.Errorf("alice after rolled-back reset = (%+v, %v), want untouched", u, gerr)
			}
			pks, perr := s.GetPasskeysByUserID(ctx, users[0].ID)
			if perr != nil || len(pks) != 1 || string(pks[0].CredentialID) != string(pk.CredentialID) {
				t.Errorf("passkeys after rolled-back reset = (%+v, %v), want untouched", pks, perr)
			}
			keys, kerr := s.ListAPIKeysByUserID(ctx, users[1].ID)
			if kerr != nil || len(keys) != 1 || keys[0].KeyHash != key.KeyHash {
				t.Errorf("api keys after rolled-back reset = (%+v, %v), want untouched", keys, kerr)
			}
		})
	}
}
