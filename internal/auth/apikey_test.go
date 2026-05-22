package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"testing"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 14: API key hash verification round-trip
// **Validates: Requirements 11.1, 11.4, 16.2**
func TestProperty_APIKeyHashVerificationRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		plaintext, returnedHash, _, _, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey error: %v", err)
		}

		// Compute SHA-256 of the plaintext independently.
		h := sha256.Sum256([]byte(plaintext))
		computedHash := hex.EncodeToString(h[:])

		// The independently computed hash must match the returned hash
		// (constant-time comparison).
		if subtle.ConstantTimeCompare([]byte(computedHash), []byte(returnedHash)) != 1 {
			t.Fatalf("hash mismatch: computed %s != returned %s", computedHash, returnedHash)
		}

		// A different string's hash must NOT match.
		other := plaintext + "x"
		otherH := sha256.Sum256([]byte(other))
		otherHash := hex.EncodeToString(otherH[:])

		if subtle.ConstantTimeCompare([]byte(otherHash), []byte(returnedHash)) == 1 {
			t.Fatalf("different key produced same hash")
		}
	})
}

// Feature: subflux-authentication, Property 15: API key format and uniqueness
// **Validates: Requirements 11.3**
func TestProperty_APIKeyFormatAndUniqueness(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 20).Draw(t, "n")
		plaintexts := make(map[string]struct{}, n)
		hashes := make(map[string]struct{}, n)

		for i := range n {
			plaintext, hash, prefix, suffix, err := GenerateAPIKey()
			if err != nil {
				t.Fatalf("GenerateAPIKey[%d] error: %v", i, err)
			}

			// Must start with "sfx_".
			if len(plaintext) < 4 || plaintext[:4] != "sfx_" {
				t.Fatalf("key does not start with sfx_: %s", plaintext)
			}

			// Random portion (after "sfx_") must be at least 64 hex chars (32 bytes).
			randomPart := plaintext[4:]
			if len(randomPart) < 64 {
				t.Fatalf("random portion length %d < 64", len(randomPart))
			}
			// Verify it's valid hex.
			if _, err := hex.DecodeString(randomPart); err != nil {
				t.Fatalf("random portion is not valid hex: %v", err)
			}

			// prefix must be first 8 chars of plaintext.
			if prefix != plaintext[:8] {
				t.Fatalf("prefix %q != first 8 chars %q", prefix, plaintext[:8])
			}

			// suffix must be last 4 chars of plaintext.
			if suffix != plaintext[len(plaintext)-4:] {
				t.Fatalf("suffix %q != last 4 chars %q", suffix, plaintext[len(plaintext)-4:])
			}

			// All plaintext keys must be unique.
			if _, dup := plaintexts[plaintext]; dup {
				t.Fatalf("duplicate plaintext at index %d", i)
			}
			plaintexts[plaintext] = struct{}{}

			// All hashes must be unique.
			if _, dup := hashes[hash]; dup {
				t.Fatalf("duplicate hash at index %d", i)
			}
			hashes[hash] = struct{}{}
		}
	})
}

func TestVerifyAPIKey_error_paths(t *testing.T) {
	t.Parallel()
	db := newFakeSessionStore()
	ctx := context.Background()

	// Create a user and store a known API key.
	user := &api.User{
		Username:     "keyuser",
		PasswordHash: "dummy",
		Role:         "admin",
		Enabled:      true,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	plaintext, hash, prefix, suffix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     "test",
	}
	if err := db.CreateAPIKey(ctx, apiKey); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	tests := []struct {
		name string
		key  string
	}{
		{
			name: "nonexistent key returns ErrInvalidAPIKey",
			key:  "sfx_0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name: "wrong key returns ErrInvalidAPIKey",
			key:  plaintext + "x",
		},
		{
			name: "empty key returns ErrInvalidAPIKey",
			key:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := VerifyAPIKey(ctx, db, tc.key)
			if !errors.Is(err, ErrInvalidAPIKey) {
				t.Errorf("VerifyAPIKey(%q) error = %v, want ErrInvalidAPIKey", tc.key, err)
			}
			if got != nil {
				t.Errorf("VerifyAPIKey(%q) = %+v, want nil", tc.key, got)
			}
		})
	}

	// Also verify the valid key still works.
	got, err := VerifyAPIKey(ctx, db, plaintext)
	if err != nil {
		t.Fatalf("VerifyAPIKey(valid) error: %v", err)
	}
	if got == nil {
		t.Fatal("VerifyAPIKey(valid) = nil, want key")
	}
}
