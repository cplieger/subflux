package auth

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// SessionStoreContractSuite runs identical behavioral cases against any
// SessionStore implementation. It verifies basic roundtrip guarantees that
// both the real authdb and test fakes (e.g. fakeSessionStore) must satisfy.
func SessionStoreContractSuite(t *testing.T, newStore func(t *testing.T) SessionStore) {
	t.Helper()

	t.Run("GetSessionByHash_missing_returns_nil_nil", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		sess, err := s.GetSessionByHash(ctx, "nonexistent-hash")
		if err != nil {
			t.Fatalf("GetSessionByHash: unexpected error: %v", err)
		}
		if sess != nil {
			t.Fatalf("GetSessionByHash: expected nil for missing session, got %+v", sess)
		}
	})

	t.Run("GetUserByID_missing_returns_nil_nil", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		user, err := s.GetUserByID(ctx, 99999)
		if err != nil {
			t.Fatalf("GetUserByID: unexpected error: %v", err)
		}
		if user != nil {
			t.Fatalf("GetUserByID: expected nil for missing user, got %+v", user)
		}
	})

	t.Run("GetAPIKeyByHash_missing_returns_nil_nil", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		key, err := s.GetAPIKeyByHash(ctx, "nonexistent-key-hash")
		if err != nil {
			t.Fatalf("GetAPIKeyByHash: unexpected error: %v", err)
		}
		if key != nil {
			t.Fatalf("GetAPIKeyByHash: expected nil for missing key, got %+v", key)
		}
	})
}

// SessionStoreContractTest is an alias for SessionStoreContractSuite for
// backward compatibility with the ta-b4 proposal naming.
var SessionStoreContractTest = SessionStoreContractSuite

// TestFakeSessionStore_contract runs the contract suite against fakeSessionStore.
func TestFakeSessionStore_contract(t *testing.T) {
	t.Parallel()
	SessionStoreContractSuite(t, func(t *testing.T) SessionStore {
		return newFakeSessionStore()
	})
}

// TestFakeSessionStore_roundtrip verifies create-then-get semantics.
func TestFakeSessionStore_roundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeSessionStore()

	// Create a user and verify retrieval.
	u := &api.User{Username: "contract-user", PasswordHash: "hash"}
	if err := store.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got == nil || got.Username != "contract-user" {
		t.Fatalf("GetUserByID: got %+v, want user with username 'contract-user'", got)
	}

	// Create a session and verify retrieval.
	sess := &api.Session{
		TokenHash:    "test-token-hash",
		UserID:       u.ID,
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	gotSess, err := store.GetSessionByHash(ctx, "test-token-hash")
	if err != nil {
		t.Fatalf("GetSessionByHash: %v", err)
	}
	if gotSess == nil || gotSess.UserID != u.ID {
		t.Fatalf("GetSessionByHash: got %+v, want session for user %d", gotSess, u.ID)
	}

	// Create an API key and verify retrieval.
	key := &api.Key{
		KeyHash: "test-key-hash",
		UserID:  u.ID,
		Label:   "contract-key",
	}
	if err := store.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	gotKey, err := store.GetAPIKeyByHash(ctx, "test-key-hash")
	if err != nil {
		t.Fatalf("GetAPIKeyByHash: %v", err)
	}
	if gotKey == nil || gotKey.Label != "contract-key" {
		t.Fatalf("GetAPIKeyByHash: got %+v, want key labeled 'contract-key'", gotKey)
	}
}
