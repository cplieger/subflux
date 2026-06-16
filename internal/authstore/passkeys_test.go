package authstore

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cplieger/auth"
	bolt "go.etcd.io/bbolt"
)

// newPasskeyStore opens a freshly bootstrapped bbolt file as a shared handle
// and returns an auth Store ready for the durable passkey methods.
func newPasskeyStore(t *testing.T) *Store {
	t.Helper()
	db := openShared(t, bootstrappedFile(t))
	s := New(db)
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// sampleCred builds a fully-populated credential so round-trip assertions cover
// every persisted field, including the binary credential material and flags
// that auth.PasskeyCredential marks json:"-".
func sampleCred(userID int64, credID []byte, name string) *auth.PasskeyCredential {
	return &auth.PasskeyCredential{
		UserID:          userID,
		CredentialID:    credID,
		PublicKey:       []byte("pub-" + name),
		AAGUID:          bytes.Repeat([]byte{0xAB}, 16),
		RawAttestation:  []byte("raw-attestation-" + name),
		AttestationType: "none",
		Transport:       "usb",
		Name:            name,
		SignCount:       5,
		BackupEligible:  true,
		BackupState:     true,
		UserPresent:     true,
		UserVerified:    true,
	}
}

func TestCreatePasskey_setsIDAndRoundTripsAllFields(t *testing.T) {
	s := newPasskeyStore(t)
	ctx := context.Background()

	// A credential id containing a NUL byte exercises the fixed-offset
	// separator in the ix_passkey_user key (binary credential ids are legal).
	credID := []byte{0x01, 0x00, 0x02, 0x03}
	cred := sampleCred(7, credID, "yubikey")
	cred.CloneWarning = true // must round-trip (Requirement 9.5)

	if err := s.CreatePasskey(ctx, cred); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	if cred.ID < 1 {
		t.Fatalf("CreatePasskey did not set a positive ID, got %d", cred.ID)
	}
	if cred.CreatedAt.IsZero() {
		t.Errorf("CreatePasskey did not stamp CreatedAt")
	}

	got, err := s.GetPasskeyByCredentialID(ctx, credID)
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID: %v", err)
	}
	if got == nil {
		t.Fatal("GetPasskeyByCredentialID returned nil for an existing credential")
	}
	if got.ID != cred.ID || got.UserID != 7 {
		t.Errorf("id/user mismatch: got id=%d user=%d", got.ID, got.UserID)
	}
	if !bytes.Equal(got.CredentialID, credID) || !bytes.Equal(got.PublicKey, cred.PublicKey) ||
		!bytes.Equal(got.AAGUID, cred.AAGUID) || !bytes.Equal(got.RawAttestation, cred.RawAttestation) {
		t.Errorf("binary fields lost on round-trip: %+v", got)
	}
	if got.AttestationType != "none" || got.Transport != "usb" || got.Name != "yubikey" || got.SignCount != 5 {
		t.Errorf("scalar fields lost on round-trip: %+v", got)
	}
	if !got.BackupEligible || !got.BackupState || !got.UserPresent || !got.UserVerified || !got.CloneWarning {
		t.Errorf("flags lost on round-trip: %+v", got)
	}
}

func TestGetPasskeyByCredentialID_notFound(t *testing.T) {
	s := newPasskeyStore(t)
	got, err := s.GetPasskeyByCredentialID(context.Background(), []byte("nope"))
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID: %v", err)
	}
	if got != nil {
		t.Errorf("GetPasskeyByCredentialID(absent) = %+v, want nil", got)
	}
}

func TestCreatePasskey_duplicateCredentialIDRejected(t *testing.T) {
	s := newPasskeyStore(t)
	ctx := context.Background()
	credID := []byte("dup-cred")
	if err := s.CreatePasskey(ctx, sampleCred(1, credID, "first")); err != nil {
		t.Fatalf("CreatePasskey first: %v", err)
	}
	err := s.CreatePasskey(ctx, sampleCred(2, credID, "second"))
	if !errors.Is(err, errConflict) {
		t.Fatalf("CreatePasskey duplicate credential id = %v, want errConflict", err)
	}
	// No partial write: the original owner still owns exactly one passkey and
	// user 2 got nothing.
	if n, _ := s.PasskeyCountForUser(ctx, 1); n != 1 {
		t.Errorf("owner count after rejected duplicate = %d, want 1", n)
	}
	if n, _ := s.PasskeyCountForUser(ctx, 2); n != 0 {
		t.Errorf("second-user count after rejected duplicate = %d, want 0", n)
	}
}

func TestGetPasskeysByUserID_orderedAndIsolated(t *testing.T) {
	s := newPasskeyStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Second)
	// Insert out of chronological order; result must come back by CreatedAt asc.
	mk := func(userID int64, name string, at time.Time) {
		c := sampleCred(userID, []byte(name), name)
		c.CreatedAt = at
		if err := s.CreatePasskey(ctx, c); err != nil {
			t.Fatalf("CreatePasskey %q: %v", name, err)
		}
	}
	mk(1, "u1-b", base.Add(2*time.Hour))
	mk(1, "u1-a", base.Add(1*time.Hour))
	mk(1, "u1-c", base.Add(3*time.Hour))
	mk(2, "u2-x", base) // different user, must not leak in

	got, err := s.GetPasskeysByUserID(ctx, 1)
	if err != nil {
		t.Fatalf("GetPasskeysByUserID: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d passkeys for user 1, want 3 (isolation failure?)", len(got))
	}
	wantOrder := []string{"u1-a", "u1-b", "u1-c"}
	for i, w := range wantOrder {
		if got[i].Name != w {
			t.Errorf("order[%d] = %q, want %q (ascending CreatedAt)", i, got[i].Name, w)
		}
	}
}

func TestUpdatePasskeyAfterLogin_monotonicAndFlags(t *testing.T) {
	s := newPasskeyStore(t)
	ctx := context.Background()
	credID := []byte("cred-mono")
	cred := sampleCred(1, credID, "key")
	cred.SignCount = 5
	if err := s.CreatePasskey(ctx, cred); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}

	// Higher incoming count persists; flags are taken from the update.
	flags := auth.PasskeyFlags{BackupEligible: false, BackupState: false, UserPresent: true, UserVerified: false, CloneWarning: true}
	if err := s.UpdatePasskeyAfterLogin(ctx, credID, 10, flags); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin(10): %v", err)
	}
	got, _ := s.GetPasskeyByCredentialID(ctx, credID)
	if got.SignCount != 10 {
		t.Errorf("sign_count = %d after bump to 10, want 10", got.SignCount)
	}
	if got.BackupEligible || got.BackupState || !got.UserPresent || got.UserVerified || !got.CloneWarning {
		t.Errorf("flags not persisted from update: %+v", got)
	}

	// A lower incoming count (replay / cloned authenticator) must NOT regress
	// the stored value (CVE-2023-45669 clone detection).
	if err := s.UpdatePasskeyAfterLogin(ctx, credID, 3, auth.PasskeyFlags{UserPresent: true}); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin(3): %v", err)
	}
	got, _ = s.GetPasskeyByCredentialID(ctx, credID)
	if got.SignCount != 10 {
		t.Errorf("sign_count regressed to %d after stale count 3, want 10 (monotonic)", got.SignCount)
	}
}

// TestUpdatePasskeyAfterLogin_durableAcrossReopen asserts the monotonic
// sign_count survives a full close/reopen of the underlying file — clone
// detection state must be durable, not just in-memory (Requirement 9.5).
func TestUpdatePasskeyAfterLogin_durableAcrossReopen(t *testing.T) {
	path := bootstrappedFile(t)
	ctx := context.Background()
	credID := []byte("cred-durable")

	// First session: create at 5, bump to 42, then close the file entirely.
	db1, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("bolt.Open(1): %v", err)
	}
	s1 := New(db1)
	if err := s1.Open(); err != nil {
		t.Fatalf("Open(1): %v", err)
	}
	if err := s1.CreatePasskey(ctx, sampleCred(1, credID, "key")); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	if err := s1.UpdatePasskeyAfterLogin(ctx, credID, 42, auth.PasskeyFlags{UserPresent: true}); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin(42): %v", err)
	}
	_ = s1.Close()
	if err := db1.Close(); err != nil {
		t.Fatalf("db1.Close: %v", err)
	}

	// Second session over the same file: stored count is still 42, and a stale
	// lower count is still rejected.
	db2, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("bolt.Open(2): %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	s2 := New(db2)
	if err := s2.Open(); err != nil {
		t.Fatalf("Open(2): %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	got, err := s2.GetPasskeyByCredentialID(ctx, credID)
	if err != nil {
		t.Fatalf("GetPasskeyByCredentialID after reopen: %v", err)
	}
	if got == nil || got.SignCount != 42 {
		t.Fatalf("sign_count after reopen = %v, want 42 (durable)", got)
	}
	if err := s2.UpdatePasskeyAfterLogin(ctx, credID, 1, auth.PasskeyFlags{UserPresent: true}); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin(1): %v", err)
	}
	got, _ = s2.GetPasskeyByCredentialID(ctx, credID)
	if got.SignCount != 42 {
		t.Errorf("sign_count regressed to %d across reopen, want 42", got.SignCount)
	}
}

func TestUpdatePasskeyAfterLogin_absentIsNoOp(t *testing.T) {
	s := newPasskeyStore(t)
	err := s.UpdatePasskeyAfterLogin(context.Background(), []byte("ghost"), 9, auth.PasskeyFlags{})
	if err != nil {
		t.Errorf("UpdatePasskeyAfterLogin(absent) = %v, want nil", err)
	}
}

func TestRenamePasskey_ownershipEnforced(t *testing.T) {
	s := newPasskeyStore(t)
	ctx := context.Background()

	owner := sampleCred(1, []byte("owner-cred"), "old-name")
	if err := s.CreatePasskey(ctx, owner); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}

	// A different user attempting to rename by the same surrogate id must be a
	// no-op (Requirement 16.4): the name is unchanged.
	if err := s.RenamePasskey(ctx, owner.ID, 2, "hijacked"); err != nil {
		t.Fatalf("RenamePasskey(wrong owner): %v", err)
	}
	got, _ := s.GetPasskeyByCredentialID(ctx, owner.CredentialID)
	if got.Name != "old-name" {
		t.Errorf("non-owner rename took effect: name = %q, want %q", got.Name, "old-name")
	}

	// The real owner can rename.
	if err := s.RenamePasskey(ctx, owner.ID, 1, "new-name"); err != nil {
		t.Fatalf("RenamePasskey(owner): %v", err)
	}
	got, _ = s.GetPasskeyByCredentialID(ctx, owner.CredentialID)
	if got.Name != "new-name" {
		t.Errorf("owner rename did not apply: name = %q, want %q", got.Name, "new-name")
	}
}

func TestDeletePasskey_ownershipEnforced(t *testing.T) {
	s := newPasskeyStore(t)
	ctx := context.Background()

	owner := sampleCred(1, []byte("del-cred"), "key")
	if err := s.CreatePasskey(ctx, owner); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}

	// Wrong user cannot delete: the credential is still present afterward.
	if err := s.DeletePasskey(ctx, owner.ID, 2); err != nil {
		t.Fatalf("DeletePasskey(wrong owner): %v", err)
	}
	if got, _ := s.GetPasskeyByCredentialID(ctx, owner.CredentialID); got == nil {
		t.Fatal("non-owner delete removed the credential")
	}
	if n, _ := s.PasskeyCountForUser(ctx, 1); n != 1 {
		t.Errorf("count after non-owner delete = %d, want 1", n)
	}

	// Real owner deletes; both the primary and its index entry go away.
	if err := s.DeletePasskey(ctx, owner.ID, 1); err != nil {
		t.Fatalf("DeletePasskey(owner): %v", err)
	}
	if got, _ := s.GetPasskeyByCredentialID(ctx, owner.CredentialID); got != nil {
		t.Errorf("owner delete left the credential: %+v", got)
	}
	if n, _ := s.PasskeyCountForUser(ctx, 1); n != 0 {
		t.Errorf("count after owner delete = %d, want 0 (index entry leaked?)", n)
	}
}

func TestPasskeyCountForUser(t *testing.T) {
	s := newPasskeyStore(t)
	ctx := context.Background()
	if n, _ := s.PasskeyCountForUser(ctx, 1); n != 0 {
		t.Fatalf("count on empty = %d, want 0", n)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := s.CreatePasskey(ctx, sampleCred(1, []byte("u1-"+name), name)); err != nil {
			t.Fatalf("CreatePasskey %q: %v", name, err)
		}
	}
	if err := s.CreatePasskey(ctx, sampleCred(2, []byte("u2-a"), "a")); err != nil {
		t.Fatalf("CreatePasskey u2: %v", err)
	}
	if n, _ := s.PasskeyCountForUser(ctx, 1); n != 3 {
		t.Errorf("count(user1) = %d, want 3", n)
	}
	if n, _ := s.PasskeyCountForUser(ctx, 2); n != 1 {
		t.Errorf("count(user2) = %d, want 1", n)
	}
}

// TestDeleteUser_cascadesRealPasskeys cross-checks the create path's index
// layout against users.go's cascade: a passkey created via CreatePasskey must
// be deleted (primary + index) when its owning user is deleted, while another
// user's passkey is untouched.
func TestDeleteUser_cascadesRealPasskeys(t *testing.T) {
	s := newPasskeyStore(t)
	ctx := context.Background()

	victim := &auth.User{Username: "victim", Role: auth.RoleUser}
	keep := &auth.User{Username: "keep", Role: auth.RoleUser}
	if err := s.CreateUser(ctx, victim); err != nil {
		t.Fatalf("CreateUser victim: %v", err)
	}
	if err := s.CreateUser(ctx, keep); err != nil {
		t.Fatalf("CreateUser keep: %v", err)
	}

	vCred := sampleCred(victim.ID, []byte("victim-pk"), "v")
	kCred := sampleCred(keep.ID, []byte("keep-pk"), "k")
	if err := s.CreatePasskey(ctx, vCred); err != nil {
		t.Fatalf("CreatePasskey victim: %v", err)
	}
	if err := s.CreatePasskey(ctx, kCred); err != nil {
		t.Fatalf("CreatePasskey keep: %v", err)
	}

	if err := s.DeleteUser(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Victim's passkey is gone from both the primary bucket and the user index.
	if got, _ := s.GetPasskeyByCredentialID(ctx, vCred.CredentialID); got != nil {
		t.Errorf("victim passkey survived user delete: %+v", got)
	}
	if n, _ := s.PasskeyCountForUser(ctx, victim.ID); n != 0 {
		t.Errorf("victim passkey index leaked: count = %d, want 0", n)
	}
	// Keep's passkey is untouched.
	if got, _ := s.GetPasskeyByCredentialID(ctx, kCred.CredentialID); got == nil {
		t.Errorf("keep passkey collaterally deleted")
	}
	if n, _ := s.PasskeyCountForUser(ctx, keep.ID); n != 1 {
		t.Errorf("keep passkey count = %d, want 1", n)
	}
}
