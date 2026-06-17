package authstore_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/auth"
	authlibstore "github.com/cplieger/auth/store"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/authstore/authstoretest"
	"github.com/cplieger/subflux/internal/boltstore"
	bolt "go.etcd.io/bbolt"
)

// boltHarness drives the engine-agnostic authstore contract suite against the
// new bbolt authstore.Store over a REAL file (not in-memory), so the
// sign_count-durability case can survive a simulated restart. The core store
// (boltstore) owns bucket bootstrap; this harness mirrors the production wiring
// — bootstrap with boltstore.Open, release the OS lock, then share a raw
// *bbolt.DB handle with authstore.New.
type boltHarness struct {
	db   *bolt.DB
	s    *authstore.Store
	path string
}

func newBoltHarness(t *testing.T) authstoretest.Harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	// Bootstrap every core AND auth bucket, then release the exclusive lock so
	// the harness can reopen the file as the shared handle.
	core, err := boltstore.Open(path)
	if err != nil {
		t.Fatalf("boltstore.Open(%q): %v", path, err)
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatalf("boltstore.Close: %v", err)
	}
	h := &boltHarness{path: path}
	h.open(t)
	t.Cleanup(func() {
		if h.s != nil {
			_ = h.s.Close()
		}
		if h.db != nil {
			_ = h.db.Close()
		}
	})
	return h
}

// open reopens the shared bbolt handle and builds a fresh auth Store on it.
func (h *boltHarness) open(t *testing.T) {
	t.Helper()
	db, err := bolt.Open(h.path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("bolt.Open(%q): %v", h.path, err)
	}
	s := authstore.New(db)
	if err := s.Open(); err != nil {
		_ = db.Close()
		t.Fatalf("authstore.Open: %v", err)
	}
	h.db = db
	h.s = s
}

func (h *boltHarness) Store() authlibstore.Composite { return h.s }

// Reopen simulates a process restart: stop the sweeper, close the shared
// handle, and reopen durable state from the same file. Ephemeral state
// (sessions, OIDC) is in-memory by design and is therefore empty after this.
func (h *boltHarness) Reopen(t *testing.T) authlibstore.Composite {
	t.Helper()
	if err := h.s.Close(); err != nil {
		t.Fatalf("authstore.Close on reopen: %v", err)
	}
	if err := h.db.Close(); err != nil {
		t.Fatalf("bolt.Close on reopen: %v", err)
	}
	h.open(t)
	return h.s
}

// TestAuthStoreContract runs the shared engine-agnostic AuthStore contract
// suite against the new bbolt store (Requirements 14.1, 14.2).
func TestAuthStoreContract(t *testing.T) {
	authstoretest.Suite(t, newBoltHarness)
}

// --- New-store-only behaviours (deliberate divergences from the old SQLite
// store, asserted here rather than in the shared suite). ---

// TestSignCount_neverRegresses pins the CVE-2023-45669 hardening: a lower
// incoming sign_count must NOT overwrite a higher stored one (Requirement 9.5).
// The old SQLite store overwrote unconditionally, so this is a new-store-only
// assertion and is intentionally not in the shared suite.
func TestSignCount_neverRegresses(t *testing.T) {
	h := newBoltHarness(t)
	ctx := context.Background()
	s := h.Store()

	u := &auth.User{Username: "monotonic", Role: auth.RoleUser, Enabled: true}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cred := &auth.PasskeyCredential{UserID: u.ID, CredentialID: []byte("mono-cred"), PublicKey: []byte("pub"), SignCount: 10}
	if err := s.CreatePasskey(ctx, cred); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}

	// A replayed/cloned authenticator presents a stale, lower counter.
	flags := auth.PasskeyFlags{UserPresent: true, UserVerified: true}
	if err := s.UpdatePasskeyAfterLogin(ctx, cred.CredentialID, 3, flags); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin(lower): %v", err)
	}
	got, _ := s.GetPasskeyByCredentialID(ctx, cred.CredentialID)
	if got == nil || got.SignCount != 10 {
		t.Errorf("sign_count after lower incoming = %v, want 10 (no regression)", got)
	}

	// A higher counter still advances.
	if err := s.UpdatePasskeyAfterLogin(ctx, cred.CredentialID, 11, flags); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin(higher): %v", err)
	}
	got, _ = s.GetPasskeyByCredentialID(ctx, cred.CredentialID)
	if got == nil || got.SignCount != 11 {
		t.Errorf("sign_count after higher incoming = %v, want 11", got)
	}
}

// TestCloneWarning_roundTrips pins that the clone-warning flag survives the
// codec (Requirement 9.5). The old SQLite schema has no clone_warning column,
// so this is a new-store-only assertion.
func TestCloneWarning_roundTrips(t *testing.T) {
	h := newBoltHarness(t)
	ctx := context.Background()
	s := h.Store()

	u := &auth.User{Username: "clone", Role: auth.RoleUser, Enabled: true}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	cred := &auth.PasskeyCredential{UserID: u.ID, CredentialID: []byte("clone-cred"), PublicKey: []byte("pub"), SignCount: 1}
	if err := s.CreatePasskey(ctx, cred); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	// FinishLogin detected a cloned authenticator: CloneWarning is raised.
	flags := auth.PasskeyFlags{UserPresent: true, UserVerified: true, CloneWarning: true}
	if err := s.UpdatePasskeyAfterLogin(ctx, cred.CredentialID, 2, flags); err != nil {
		t.Fatalf("UpdatePasskeyAfterLogin: %v", err)
	}
	got, _ := s.GetPasskeyByCredentialID(ctx, cred.CredentialID)
	if got == nil || !got.CloneWarning {
		t.Errorf("CloneWarning did not round-trip: %+v", got)
	}
}

// TestEphemeral_emptyAfterReopen pins that sessions and OIDC states do NOT
// survive a restart (Requirement 10.1, 10.4): they are in-memory by design,
// while durable user records survive. The old SQLite store persisted sessions,
// so this is a new-store-only assertion.
func TestEphemeral_emptyAfterReopen(t *testing.T) {
	h := newBoltHarness(t)
	ctx := context.Background()
	s := h.Store()

	u := &auth.User{Username: "durable", Role: auth.RoleUser, Enabled: true}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	now := time.Now().UTC()
	sess := &auth.Session{TokenHash: "sess-1", UserID: u.ID, AuthMethod: auth.MethodPassword, CreatedAt: now, LastActivity: now}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateOIDCState(ctx, "state-1", "n", "v", "/cb"); err != nil {
		t.Fatalf("CreateOIDCState: %v", err)
	}

	s2 := h.Reopen(t)

	// Durable record survives.
	if got, _ := s2.GetUserByUsername(ctx, "durable"); got == nil {
		t.Errorf("durable user did not survive reopen")
	}
	// Ephemeral records are gone.
	if got, _ := s2.GetSessionByHash(ctx, "sess-1"); got != nil {
		t.Errorf("session survived reopen, want empty (ephemeral): %+v", got)
	}
	if _, _, _, err := s2.ConsumeOIDCState(ctx, "state-1"); err == nil {
		t.Errorf("oidc state survived reopen, want not-found (ephemeral)")
	}
}
