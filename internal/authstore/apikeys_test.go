package authstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cplieger/auth"
	bolt "go.etcd.io/bbolt"
)

// newAPIKeyStore opens a freshly bootstrapped bbolt file as a shared handle and
// returns an auth Store ready for the durable API-key methods.
func newAPIKeyStore(t *testing.T) *Store {
	t.Helper()
	db := openShared(t, bootstrappedFile(t))
	s := New(db)
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// sampleKey builds a fully-populated key so round-trip assertions cover every
// persisted field, including KeyHash which auth.Key marks json:"-".
func sampleKey(userID int64, hash, label string) *auth.Key {
	return &auth.Key{
		KeyHash:   hash,
		KeyPrefix: "sk_pre",
		KeySuffix: "sufx",
		Label:     label,
		UserID:    userID,
	}
}

func TestCreateAPIKey_setsIDAndGetByHashRoundTrips(t *testing.T) {
	s := newAPIKeyStore(t)
	ctx := context.Background()

	exp := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	key := sampleKey(7, "hash-abc", "ci-token")
	key.ExpiresAt = &exp

	if err := s.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if key.ID < 1 {
		t.Fatalf("CreateAPIKey did not set a positive ID, got %d", key.ID)
	}
	if key.CreatedAt.IsZero() {
		t.Errorf("CreateAPIKey did not stamp CreatedAt")
	}

	got, err := s.GetAPIKeyByHash(ctx, "hash-abc")
	if err != nil {
		t.Fatalf("GetAPIKeyByHash: %v", err)
	}
	if got == nil {
		t.Fatal("GetAPIKeyByHash returned nil for an existing key")
	}
	if got.ID != key.ID || got.UserID != 7 {
		t.Errorf("id/user mismatch: got id=%d user=%d", got.ID, got.UserID)
	}
	if got.KeyHash != "hash-abc" || got.KeyPrefix != "sk_pre" || got.KeySuffix != "sufx" || got.Label != "ci-token" {
		t.Errorf("scalar fields lost on round-trip: %+v", got)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Errorf("expires_at lost on round-trip: got %v, want %v", got.ExpiresAt, exp)
	}
}

func TestGetAPIKeyByHash_notFound(t *testing.T) {
	s := newAPIKeyStore(t)
	got, err := s.GetAPIKeyByHash(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetAPIKeyByHash: %v", err)
	}
	if got != nil {
		t.Errorf("GetAPIKeyByHash(absent) = %+v, want nil", got)
	}
}

func TestCreateAPIKey_duplicateHashRejectedNoPartialWrite(t *testing.T) {
	s := newAPIKeyStore(t)
	ctx := context.Background()
	if err := s.CreateAPIKey(ctx, sampleKey(1, "dup-hash", "first")); err != nil {
		t.Fatalf("CreateAPIKey first: %v", err)
	}
	err := s.CreateAPIKey(ctx, sampleKey(2, "dup-hash", "second"))
	if !errors.Is(err, errConflict) {
		t.Fatalf("CreateAPIKey duplicate hash = %v, want errConflict", err)
	}
	// No partial write: the original key is intact and unchanged, and user 2
	// gained nothing (the rejected create did not touch the user index).
	got, _ := s.GetAPIKeyByHash(ctx, "dup-hash")
	if got == nil || got.UserID != 1 || got.Label != "first" {
		t.Errorf("duplicate create mutated the existing key: %+v", got)
	}
	if keys, _ := s.ListAPIKeysByUserID(ctx, 2); len(keys) != 0 {
		t.Errorf("second-user key list after rejected duplicate = %d, want 0", len(keys))
	}
}

func TestCreateAPIKey_emptyHashRejected(t *testing.T) {
	s := newAPIKeyStore(t)
	if err := s.CreateAPIKey(context.Background(), sampleKey(1, "", "no-hash")); err == nil {
		t.Fatal("CreateAPIKey with empty hash = nil, want error")
	}
}

func TestListAPIKeysByUserID_scopedAndOrdered(t *testing.T) {
	s := newAPIKeyStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Second)
	mk := func(userID int64, hash, label string, at time.Time) {
		k := sampleKey(userID, hash, label)
		k.CreatedAt = at
		if err := s.CreateAPIKey(ctx, k); err != nil {
			t.Fatalf("CreateAPIKey %q: %v", label, err)
		}
	}
	// Insert out of order; result must be newest-first (CreatedAt DESC).
	mk(1, "h-a", "u1-a", base.Add(1*time.Hour))
	mk(1, "h-c", "u1-c", base.Add(3*time.Hour))
	mk(1, "h-b", "u1-b", base.Add(2*time.Hour))
	mk(2, "h-x", "u2-x", base.Add(5*time.Hour)) // different user, must not leak in

	got, err := s.ListAPIKeysByUserID(ctx, 1)
	if err != nil {
		t.Fatalf("ListAPIKeysByUserID: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d keys for user 1, want 3 (isolation failure?)", len(got))
	}
	wantOrder := []string{"u1-c", "u1-b", "u1-a"}
	for i, w := range wantOrder {
		if got[i].Label != w {
			t.Errorf("order[%d] = %q, want %q (descending CreatedAt)", i, got[i].Label, w)
		}
	}
}

func TestListAPIKeysByUserID_emptyUser(t *testing.T) {
	s := newAPIKeyStore(t)
	got, err := s.ListAPIKeysByUserID(context.Background(), 99)
	if err != nil {
		t.Fatalf("ListAPIKeysByUserID: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListAPIKeysByUserID(empty user) = %d entries, want 0", len(got))
	}
}

func TestDeleteAPIKey_ownershipEnforced(t *testing.T) {
	s := newAPIKeyStore(t)
	ctx := context.Background()

	owner := sampleKey(1, "owner-hash", "key")
	if err := s.CreateAPIKey(ctx, owner); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	// Wrong user cannot delete: the key is still present afterward
	// (mirrors DELETE ... WHERE id=? AND user_id=? affecting zero rows).
	if err := s.DeleteAPIKey(ctx, owner.ID, 2); err != nil {
		t.Fatalf("DeleteAPIKey(wrong owner): %v", err)
	}
	if got, _ := s.GetAPIKeyByHash(ctx, "owner-hash"); got == nil {
		t.Fatal("non-owner delete removed the key")
	}
	if keys, _ := s.ListAPIKeysByUserID(ctx, 1); len(keys) != 1 {
		t.Errorf("count after non-owner delete = %d, want 1", len(keys))
	}

	// Real owner deletes; both the primary and its index entry go away.
	if err := s.DeleteAPIKey(ctx, owner.ID, 1); err != nil {
		t.Fatalf("DeleteAPIKey(owner): %v", err)
	}
	if got, _ := s.GetAPIKeyByHash(ctx, "owner-hash"); got != nil {
		t.Errorf("owner delete left the key: %+v", got)
	}
	if keys, _ := s.ListAPIKeysByUserID(ctx, 1); len(keys) != 0 {
		t.Errorf("count after owner delete = %d, want 0 (index entry leaked?)", len(keys))
	}
}

func TestDeleteAPIKey_absentIsNoOp(t *testing.T) {
	s := newAPIKeyStore(t)
	if err := s.DeleteAPIKey(context.Background(), 12345, 1); err != nil {
		t.Errorf("DeleteAPIKey(absent) = %v, want nil", err)
	}
}

// TestDeleteUser_cascadesRealAPIKeys cross-checks the create path's index
// layout against users.go's cascade: an API key created via CreateAPIKey must
// be deleted (primary + index) when its owning user is deleted, while another
// user's key is untouched.
func TestDeleteUser_cascadesRealAPIKeys(t *testing.T) {
	s := newAPIKeyStore(t)
	ctx := context.Background()

	victim := &auth.User{Username: "victim", Role: auth.RoleUser}
	keep := &auth.User{Username: "keep", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, victim); err != nil {
		t.Fatalf("CreateUser victim: %v", err)
	}
	if err := s.CreateUser(ctx, keep); err != nil {
		t.Fatalf("CreateUser keep: %v", err)
	}

	vKey := sampleKey(victim.ID, "victim-key-hash", "v")
	kKey := sampleKey(keep.ID, "keep-key-hash", "k")
	if err := s.CreateAPIKey(ctx, vKey); err != nil {
		t.Fatalf("CreateAPIKey victim: %v", err)
	}
	if err := s.CreateAPIKey(ctx, kKey); err != nil {
		t.Fatalf("CreateAPIKey keep: %v", err)
	}

	if err := s.DeleteUser(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Victim's key is gone from both the primary bucket and the user index.
	if got, _ := s.GetAPIKeyByHash(ctx, "victim-key-hash"); got != nil {
		t.Errorf("victim api key survived user delete: %+v", got)
	}
	if keys, _ := s.ListAPIKeysByUserID(ctx, victim.ID); len(keys) != 0 {
		t.Errorf("victim api key index leaked: count = %d, want 0", len(keys))
	}
	// Keep's key is untouched.
	if got, _ := s.GetAPIKeyByHash(ctx, "keep-key-hash"); got == nil {
		t.Errorf("keep api key collaterally deleted")
	}
	if keys, _ := s.ListAPIKeysByUserID(ctx, keep.ID); len(keys) != 1 {
		t.Errorf("keep api key count = %d, want 1", len(keys))
	}
}

// seedCorruptAPIKey writes a user-scoped ix_apikey_user entry pointing at a
// deliberately corrupt auth_api_keys record, so a user-scoped walk hits an
// undecodable row and fails closed.
func seedCorruptAPIKey(t *testing.T, s *Store, userID int64, hash string) {
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

// TestDeleteAPIKey_logsDeletionOnSuccess pins the audit trail: a successful
// owner delete emits the "api key deleted" line exactly once.
func TestDeleteAPIKey_logsDeletionOnSuccess(t *testing.T) {
	s := newAPIKeyStore(t)
	ctx := context.Background()
	key := sampleKey(1, "del-log-hash", "k")
	if err := s.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	logs := captureLogs(t)
	if err := s.DeleteAPIKey(ctx, key.ID, 1); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if got := countMsg(logs(), "api key deleted"); got != 1 {
		t.Errorf(`successful owner delete logged "api key deleted" %d times, want 1`, got)
	}
}

// TestDeleteAPIKey_propagatesUpdateError pins that a real error from the delete
// transaction is surfaced, not swallowed: a corrupt API-key record makes
// findUserAPIKeyByID fail closed, and DeleteAPIKey must return that error.
func TestDeleteAPIKey_propagatesUpdateError(t *testing.T) {
	s := newAPIKeyStore(t)
	seedCorruptAPIKey(t, s, 1, "corrupt-hash")
	if err := s.DeleteAPIKey(context.Background(), 999, 1); err == nil {
		t.Fatal("DeleteAPIKey over a corrupt record = nil, want a non-nil decode error")
	}
}
