package authstore

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/auth"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// newUserStore opens a freshly bootstrapped bbolt file as a shared handle and
// returns an auth Store ready for the durable user methods.
func newUserStore(t *testing.T) *Store {
	t.Helper()
	db := openShared(t, bootstrappedFile(t))
	s := New(db)
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedChild writes a child record (passkey or API key) and its user-scoped
// index entry directly into the buckets, standing in for the not-yet-built
// 8.3/8.4 create paths so the DeleteUser cascade can be exercised.
func seedChild(t *testing.T, db *bolt.DB, indexBucket, primaryBucket string, userID int64, primaryKey []byte) {
	t.Helper()
	idxKey := append(append(kv.Be64(uint64(userID)), kv.Sep), primaryKey...)
	if err := db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte(primaryBucket)).Put(primaryKey, []byte(`{}`)); err != nil {
			return err
		}
		return tx.Bucket([]byte(indexBucket)).Put(idxKey, nil)
	}); err != nil {
		t.Fatalf("seed child into %q: %v", primaryBucket, err)
	}
}

// childExists reports whether the child primary and its index entry are present.
func childExists(t *testing.T, db *bolt.DB, indexBucket, primaryBucket string, userID int64, primaryKey []byte) (primary, index bool) {
	t.Helper()
	idxKey := append(append(kv.Be64(uint64(userID)), kv.Sep), primaryKey...)
	_ = db.View(func(tx *bolt.Tx) error {
		primary = tx.Bucket([]byte(primaryBucket)).Get(primaryKey) != nil
		index = tx.Bucket([]byte(indexBucket)).Get(idxKey) != nil
		return nil
	})
	return primary, index
}

func TestCreateUser_setsIDAndRoundTrips(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	u := &auth.User{Username: "Alice", Email: "Alice@Example.COM", Role: auth.RoleAdmin, PasswordHash: "hash", Enabled: true}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID < 1 {
		t.Fatalf("CreateUser did not set a positive ID, got %d", u.ID)
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Errorf("CreateUser did not stamp timestamps: created=%v updated=%v", u.CreatedAt, u.UpdatedAt)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetUserByID returned nil for an existing user")
	}
	// json:"-" fields must survive the round-trip via userRec.
	if got.PasswordHash != "hash" || got.Role != auth.RoleAdmin || !got.Enabled {
		t.Errorf("round-trip lost fields: %+v", got)
	}
}

func TestGetUserByID_notFound(t *testing.T) {
	s := newUserStore(t)
	got, err := s.GetUserByID(context.Background(), 999)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got != nil {
		t.Errorf("GetUserByID(absent) = %+v, want nil", got)
	}
}

func TestGetUserByUsername_caseInsensitive(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	u := &auth.User{Username: "Alice", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for _, q := range []string{"Alice", "alice", "ALICE", "aLiCe"} {
		got, err := s.GetUserByUsername(ctx, q)
		if err != nil {
			t.Fatalf("GetUserByUsername(%q): %v", q, err)
		}
		if got == nil || got.ID != u.ID {
			t.Errorf("GetUserByUsername(%q) = %v, want user id %d", q, got, u.ID)
		}
	}

	none, err := s.GetUserByUsername(ctx, "bob")
	if err != nil {
		t.Fatalf("GetUserByUsername(bob): %v", err)
	}
	if none != nil {
		t.Errorf("GetUserByUsername(bob) = %+v, want nil", none)
	}
}

func TestGetUserByEmail_caseInsensitive(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	u := &auth.User{Username: "carol", Email: "Carol@Example.com", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := s.GetUserByEmail(ctx, "carol@example.COM")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got == nil || got.ID != u.ID {
		t.Errorf("GetUserByEmail(case-insensitive) = %v, want user id %d", got, u.ID)
	}

	none, err := s.GetUserByEmail(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail(absent): %v", err)
	}
	if none != nil {
		t.Errorf("GetUserByEmail(absent) = %+v, want nil", none)
	}
}

func TestGetUserByOIDCSub(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	u := &auth.User{Username: "dora", Role: auth.RoleUser, OIDCIssuer: "https://idp", OIDCSub: "sub-123"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := s.GetUserByOIDCSub(ctx, "https://idp", "sub-123")
	if err != nil {
		t.Fatalf("GetUserByOIDCSub: %v", err)
	}
	if got == nil || got.ID != u.ID {
		t.Errorf("GetUserByOIDCSub = %v, want user id %d", got, u.ID)
	}

	// Wrong issuer must not resolve (subject unique only within an issuer).
	if other, _ := s.GetUserByOIDCSub(ctx, "https://other", "sub-123"); other != nil {
		t.Errorf("GetUserByOIDCSub(wrong issuer) = %+v, want nil", other)
	}
	// Empty sub never matches.
	if none, _ := s.GetUserByOIDCSub(ctx, "https://idp", ""); none != nil {
		t.Errorf("GetUserByOIDCSub(empty sub) = %+v, want nil", none)
	}
}

func TestCreateUser_duplicateUsernameRejected(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	if err := s.CreateUser(ctx, &auth.User{Username: "Eve", Role: auth.RoleUser}); err != nil {
		t.Fatalf("CreateUser first: %v", err)
	}
	// Different case must still conflict (case-insensitive uniqueness).
	err := s.CreateUser(ctx, &auth.User{Username: "eve", Role: auth.RoleUser})
	if !errors.Is(err, errConflict) {
		t.Fatalf("CreateUser duplicate username = %v, want errConflict", err)
	}
	if n, _ := s.UserCount(ctx); n != 1 {
		t.Errorf("UserCount after rejected duplicate = %d, want 1 (no partial write)", n)
	}
}

func TestCreateUser_duplicateOIDCRejected(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	if err := s.CreateUser(ctx, &auth.User{Username: "frank", Role: auth.RoleUser, OIDCIssuer: "iss", OIDCSub: "x"}); err != nil {
		t.Fatalf("CreateUser first: %v", err)
	}
	err := s.CreateUser(ctx, &auth.User{Username: "frank2", Role: auth.RoleUser, OIDCIssuer: "iss", OIDCSub: "x"})
	if !errors.Is(err, errConflict) {
		t.Fatalf("CreateUser duplicate (issuer,sub) = %v, want errConflict", err)
	}
	// A different sub under the same issuer is allowed.
	if err := s.CreateUser(ctx, &auth.User{Username: "frank3", Role: auth.RoleUser, OIDCIssuer: "iss", OIDCSub: "y"}); err != nil {
		t.Errorf("CreateUser distinct sub: %v", err)
	}
}

func TestUserCount(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	if n, _ := s.UserCount(ctx); n != 0 {
		t.Fatalf("UserCount on empty = %d, want 0", n)
	}
	for _, name := range []string{"u1", "u2", "u3"} {
		if err := s.CreateUser(ctx, &auth.User{Username: name, Role: auth.RoleUser}); err != nil {
			t.Fatalf("CreateUser %q: %v", name, err)
		}
	}
	if n, _ := s.UserCount(ctx); n != 3 {
		t.Errorf("UserCount = %d, want 3", n)
	}
}

func TestListUsers_orderedByUsername(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	for _, name := range []string{"charlie", "Alice", "bob"} {
		if err := s.CreateUser(ctx, &auth.User{Username: name, Role: auth.RoleUser}); err != nil {
			t.Fatalf("CreateUser %q: %v", name, err)
		}
	}
	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	got := []string{users[0].Username, users[1].Username, users[2].Username}
	want := []string{"Alice", "bob", "charlie"} // case-insensitive ascending
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListUsers order = %v, want %v", got, want)
			break
		}
	}
}

func TestUpdateUser_reKeysUsernameIndex(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	u := &auth.User{Username: "oldname", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	u.Username = "newname"
	if err := s.UpdateUser(ctx, u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	// Old username no longer resolves; the new one does.
	if old, _ := s.GetUserByUsername(ctx, "oldname"); old != nil {
		t.Errorf("old username still resolves after rename: %+v", old)
	}
	got, err := s.GetUserByUsername(ctx, "newname")
	if err != nil {
		t.Fatalf("GetUserByUsername(newname): %v", err)
	}
	if got == nil || got.ID != u.ID {
		t.Errorf("new username does not resolve to id %d, got %v", u.ID, got)
	}
	// Re-keyed: the old name is now free for a brand-new user.
	if err := s.CreateUser(ctx, &auth.User{Username: "oldname", Role: auth.RoleUser}); err != nil {
		t.Errorf("CreateUser reusing freed old username: %v", err)
	}
}

func TestUpdateUser_reKeysOIDCIndex(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	u := &auth.User{Username: "grace", Role: auth.RoleUser, OIDCIssuer: "iss", OIDCSub: "old-sub"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	u.OIDCSub = "new-sub"
	if err := s.UpdateUser(ctx, u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if old, _ := s.GetUserByOIDCSub(ctx, "iss", "old-sub"); old != nil {
		t.Errorf("old (issuer,sub) still resolves after change: %+v", old)
	}
	got, _ := s.GetUserByOIDCSub(ctx, "iss", "new-sub")
	if got == nil || got.ID != u.ID {
		t.Errorf("new (issuer,sub) does not resolve to id %d, got %v", u.ID, got)
	}
}

func TestUpdateUser_preservesCreatedAt(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	u := &auth.User{Username: "heidi", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	created := u.CreatedAt

	u.DisplayName = "Heidi H"
	if err := s.UpdateUser(ctx, u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	got, _ := s.GetUserByID(ctx, u.ID)
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt changed on update: got %v, want %v", got.CreatedAt, created)
	}
	if got.DisplayName != "Heidi H" {
		t.Errorf("DisplayName not updated: %q", got.DisplayName)
	}
}

func TestDeleteUser_cascadesAndIsolates(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()

	victim := &auth.User{Username: "victim", Role: auth.RoleUser}
	keep := &auth.User{Username: "keep", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, victim); err != nil {
		t.Fatalf("CreateUser victim: %v", err)
	}
	if err := s.CreateUser(ctx, keep); err != nil {
		t.Fatalf("CreateUser keep: %v", err)
	}

	// Seed durable children directly (8.3/8.4 create paths not built yet).
	vCred := []byte("victim-cred")
	vHash := []byte("victim-keyhash")
	kCred := []byte("keep-cred")
	kHash := []byte("keep-keyhash")
	seedChild(t, s.db, bucketIxPasskeyUser, bucketAuthPasskeys, victim.ID, vCred)
	seedChild(t, s.db, bucketIxAPIKeyUser, bucketAuthAPIKeys, victim.ID, vHash)
	seedChild(t, s.db, bucketIxPasskeyUser, bucketAuthPasskeys, keep.ID, kCred)
	seedChild(t, s.db, bucketIxAPIKeyUser, bucketAuthAPIKeys, keep.ID, kHash)

	// Seed ephemeral sessions for both users.
	s.mu.Lock()
	s.sessions["victim-sess"] = &api.Session{UserID: victim.ID, TokenHash: "victim-sess"}
	s.sessions["keep-sess"] = &api.Session{UserID: keep.ID, TokenHash: "keep-sess"}
	s.mu.Unlock()

	if err := s.DeleteUser(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Victim user and its uniqueness index entries are gone.
	if got, _ := s.GetUserByID(ctx, victim.ID); got != nil {
		t.Errorf("victim still present after delete: %+v", got)
	}
	if got, _ := s.GetUserByUsername(ctx, "victim"); got != nil {
		t.Errorf("victim username index leaked after delete")
	}
	// Freed username can be recreated (clean-break recovery path).
	if err := s.CreateUser(ctx, &auth.User{Username: "victim", Role: auth.RoleUser}); err != nil {
		t.Errorf("recreate freed username: %v", err)
	}

	// Victim's children (primary + index) are gone.
	if p, ix := childExists(t, s.db, bucketIxPasskeyUser, bucketAuthPasskeys, victim.ID, vCred); p || ix {
		t.Errorf("victim passkey not cascaded: primary=%v index=%v", p, ix)
	}
	if p, ix := childExists(t, s.db, bucketIxAPIKeyUser, bucketAuthAPIKeys, victim.ID, vHash); p || ix {
		t.Errorf("victim api key not cascaded: primary=%v index=%v", p, ix)
	}
	// Victim's session is gone.
	s.mu.RLock()
	_, vSess := s.sessions["victim-sess"]
	_, kSess := s.sessions["keep-sess"]
	s.mu.RUnlock()
	if vSess {
		t.Errorf("victim session not cleared")
	}

	// The other user is fully untouched.
	if got, _ := s.GetUserByID(ctx, keep.ID); got == nil {
		t.Errorf("keep user removed by victim's delete")
	}
	if p, ix := childExists(t, s.db, bucketIxPasskeyUser, bucketAuthPasskeys, keep.ID, kCred); !p || !ix {
		t.Errorf("keep passkey collaterally deleted: primary=%v index=%v", p, ix)
	}
	if p, ix := childExists(t, s.db, bucketIxAPIKeyUser, bucketAuthAPIKeys, keep.ID, kHash); !p || !ix {
		t.Errorf("keep api key collaterally deleted: primary=%v index=%v", p, ix)
	}
	if !kSess {
		t.Errorf("keep session collaterally cleared")
	}
}

func TestDeleteUser_absentIsNoOp(t *testing.T) {
	s := newUserStore(t)
	if err := s.DeleteUser(context.Background(), 12345); err != nil {
		t.Errorf("DeleteUser(absent) = %v, want nil", err)
	}
}

// TestAsciiFold_uppercaseZ pins that the fold covers the top of the uppercase
// ASCII range: 'Z' must lowercase to 'z' (a single-char case that also drives
// the multi-byte lowercasing pass).
func TestAsciiFold_uppercaseZ(t *testing.T) {
	if got := asciiFold("Z"); got != "z" {
		t.Errorf("asciiFold(%q) = %q, want %q", "Z", got, "z")
	}
	if got := asciiFold("AZ"); got != "az" {
		t.Errorf("asciiFold(%q) = %q, want %q", "AZ", got, "az")
	}
}

// TestCreateUser_errorsWhenOIDCIndexBucketMissing pins that an index-write
// failure on the OIDC index aborts the whole create: with ix_user_oidc gone,
// CreateUser must return a non-nil error rather than committing a user without
// its OIDC index entry.
func TestCreateUser_errorsWhenOIDCIndexBucketMissing(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte(bucketIxUserOIDC))
	}); err != nil {
		t.Fatalf("drop %q: %v", bucketIxUserOIDC, err)
	}
	u := &auth.User{Username: "oidc-user", Role: auth.RoleUser, OIDCIssuer: "iss", OIDCSub: "sub"}
	if err := s.CreateUser(ctx, u); err == nil {
		t.Fatal("CreateUser with missing OIDC index bucket = nil, want a non-nil index error")
	}
}

// TestUpdateUser_rejectsOIDCCollision pins that an update which sets a NEW OIDC
// identity is uniqueness-checked: pointing one user at another user's already
// registered (issuer, sub) must be rejected with errConflict.
func TestUpdateUser_rejectsOIDCCollision(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	a := &auth.User{Username: "collide-a", Role: auth.RoleUser, OIDCIssuer: "iss", OIDCSub: "shared-sub"}
	if err := s.CreateUser(ctx, a); err != nil {
		t.Fatalf("CreateUser(a): %v", err)
	}
	b := &auth.User{Username: "collide-b", Role: auth.RoleUser}
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

// TestDeleteUser_freesOIDCIndexForReuse pins that deleting a user that HAS an
// OIDC identity removes its ix_user_oidc entry, so the freed (issuer, sub) can
// back a brand-new user (the clean-break recovery path).
func TestDeleteUser_freesOIDCIndexForReuse(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	a := &auth.User{Username: "oidc-victim", Role: auth.RoleUser, OIDCIssuer: "iss-x", OIDCSub: "sub-x"}
	if err := s.CreateUser(ctx, a); err != nil {
		t.Fatalf("CreateUser(a): %v", err)
	}
	if err := s.DeleteUser(ctx, a.ID); err != nil {
		t.Fatalf("DeleteUser(a): %v", err)
	}
	// The (issuer, sub) must be free now: recreating with it must succeed.
	b := &auth.User{Username: "oidc-reuse", Role: auth.RoleUser, OIDCIssuer: "iss-x", OIDCSub: "sub-x"}
	if err := s.CreateUser(ctx, b); err != nil {
		t.Fatalf("CreateUser reusing the deleted user's (issuer,sub) = %v, want nil (stale index entry?)", err)
	}
}

// TestDeleteUser_propagatesAPIKeyCascadeError pins that a failure inside the
// durable API-key cascade is propagated (the whole delete tx rolls back) rather
// than swallowed. A user-scoped index entry whose child key names a sub-bucket
// makes the cascade's Bucket.Delete return ErrIncompatibleValue.
func TestDeleteUser_propagatesAPIKeyCascadeError(t *testing.T) {
	s := newUserStore(t)
	ctx := context.Background()
	u := &auth.User{Username: "cascade-user", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	const child = "cascade_subbkt"
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
