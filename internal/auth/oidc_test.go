package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 10: OIDC identity resolution
// **Validates: Requirements 4.7, 4.8**
func TestProperty_OIDCIdentityResolution(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate random OIDC claims.
		claims := &OIDCClaims{
			Subject:           rapid.StringMatching(`[a-z0-9]{8,32}`).Draw(t, "sub"),
			Email:             rapid.StringMatching(`[a-z]{4,8}@[a-z]{4,8}\.[a-z]{2,4}`).Draw(t, "email"),
			PreferredUsername: rapid.StringMatching(`[a-z]{4,16}`).Draw(t, "preferred_username"),
			Name:              rapid.StringMatching(`[A-Z][a-z]{2,8} [A-Z][a-z]{2,8}`).Draw(t, "name"),
		}

		// Generate existing users for each lookup path.
		existingByOIDCSub := &api.User{
			ID:       rapid.Int64Range(1, 1000).Draw(t, "subUserID"),
			Username: "oidc-sub-user",
			Role:     "admin",
			Enabled:  true,
		}
		existingByEmail := &api.User{
			ID:       rapid.Int64Range(1001, 2000).Draw(t, "emailUserID"),
			Username: "email-user",
			Role:     "user",
			Enabled:  true,
		}
		existingByUsername := &api.User{
			ID:       rapid.Int64Range(2001, 3000).Draw(t, "usernameUserID"),
			Username: "username-user",
			Role:     "user",
			Enabled:  true,
		}

		// Test path 1: existingByOIDCSub set → returns that user, isNew=false
		user, isNew := ResolveOIDCUser(claims, existingByOIDCSub, existingByEmail, existingByUsername)
		if user != existingByOIDCSub {
			t.Fatal("expected existingByOIDCSub when all three exist")
		}
		if isNew {
			t.Fatal("expected isNew=false for existingByOIDCSub")
		}

		// Test path 2: existingByOIDCSub nil, existingByEmail set → returns email user
		user, isNew = ResolveOIDCUser(claims, nil, existingByEmail, existingByUsername)
		if user != existingByEmail {
			t.Fatal("expected existingByEmail when sub is nil")
		}
		if isNew {
			t.Fatal("expected isNew=false for existingByEmail")
		}

		// Test path 3: existingByOIDCSub nil, existingByEmail nil, existingByUsername set
		user, isNew = ResolveOIDCUser(claims, nil, nil, existingByUsername)
		if user != existingByUsername {
			t.Fatal("expected existingByUsername when sub and email are nil")
		}
		if isNew {
			t.Fatal("expected isNew=false for existingByUsername")
		}

		// Test path 4: all nil → returns new user with role "user", isNew=true
		user, isNew = ResolveOIDCUser(claims, nil, nil, nil)
		if !isNew {
			t.Fatal("expected isNew=true when all lookups are nil")
		}
		if user.Role != "user" {
			t.Fatalf("expected role 'user', got %q", user.Role)
		}
		if user.Username != claims.PreferredUsername {
			t.Fatalf("expected username %q, got %q", claims.PreferredUsername, user.Username)
		}
		if user.Email != claims.Email {
			t.Fatalf("expected email %q, got %q", claims.Email, user.Email)
		}
		if user.DisplayName != claims.Name {
			t.Fatalf("expected display name %q, got %q", claims.Name, user.DisplayName)
		}
		if user.OIDCSub != claims.Subject {
			t.Fatalf("expected OIDCSub %q, got %q", claims.Subject, user.OIDCSub)
		}
		if !user.Enabled {
			t.Fatal("expected new user to be enabled")
		}
	})
}

// TestProperty_OIDCIdentityResolution_EmptyPreferredUsername verifies that when
// preferred_username is empty, the new user's username falls back to email.
func TestProperty_OIDCIdentityResolution_EmptyPreferredUsername(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		claims := &OIDCClaims{
			Subject:           rapid.StringMatching(`[a-z0-9]{8,32}`).Draw(t, "sub"),
			Email:             rapid.StringMatching(`[a-z]{4,8}@[a-z]{4,8}\.[a-z]{2,4}`).Draw(t, "email"),
			PreferredUsername: "", // empty
			Name:              rapid.StringMatching(`[A-Z][a-z]{2,8} [A-Z][a-z]{2,8}`).Draw(t, "name"),
		}

		user, isNew := ResolveOIDCUser(claims, nil, nil, nil)
		if !isNew {
			t.Fatal("expected isNew=true")
		}
		if user.Username != claims.Email {
			t.Fatalf("expected username to fall back to email %q, got %q", claims.Email, user.Username)
		}
	})
}

// TestProperty_PKCERoundTrip verifies that GeneratePKCE produces a verifier and
// challenge where the challenge is base64url(SHA-256(verifier)).
func TestProperty_PKCERoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		verifier, challenge, err := GeneratePKCE()
		if err != nil {
			t.Fatalf("GeneratePKCE error: %v", err)
		}

		if verifier == "" {
			t.Fatal("verifier is empty")
		}
		if challenge == "" {
			t.Fatal("challenge is empty")
		}

		// Independently compute the expected challenge.
		h := sha256.Sum256([]byte(verifier))
		expectedChallenge := base64.RawURLEncoding.EncodeToString(h[:])

		if challenge != expectedChallenge {
			t.Fatalf("challenge mismatch: got %q, want %q", challenge, expectedChallenge)
		}

		// Verify the verifier decodes to exactly 32 bytes.
		raw, err := base64.RawURLEncoding.DecodeString(verifier)
		if err != nil {
			t.Fatalf("verifier is not valid base64url: %v", err)
		}
		if len(raw) != 32 {
			t.Fatalf("verifier raw length %d, want 32", len(raw))
		}
	})
}

// TestProperty_PKCEUniqueness verifies that multiple PKCE generations produce
// unique verifiers and challenges.
func TestProperty_PKCEUniqueness(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 20).Draw(t, "n")
		verifiers := make(map[string]struct{}, n)
		challenges := make(map[string]struct{}, n)

		for i := range n {
			verifier, challenge, err := GeneratePKCE()
			if err != nil {
				t.Fatalf("GeneratePKCE[%d] error: %v", i, err)
			}

			if _, dup := verifiers[verifier]; dup {
				t.Fatalf("duplicate verifier at index %d", i)
			}
			verifiers[verifier] = struct{}{}

			if _, dup := challenges[challenge]; dup {
				t.Fatalf("duplicate challenge at index %d", i)
			}
			challenges[challenge] = struct{}{}
		}
	})
}

// TestProperty_OIDCStateGeneration verifies that GenerateOIDCState produces
// unique, 64-character hex strings (32 bytes).
func TestProperty_OIDCStateGeneration(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 20).Draw(t, "n")
		states := make(map[string]struct{}, n)

		for i := range n {
			state, err := GenerateOIDCState()
			if err != nil {
				t.Fatalf("GenerateOIDCState[%d] error: %v", i, err)
			}

			if len(state) != 64 {
				t.Fatalf("state length %d, want 64 hex chars", len(state))
			}

			if _, dup := states[state]; dup {
				t.Fatalf("duplicate state at index %d", i)
			}
			states[state] = struct{}{}
		}
	})
}

func TestNewOIDCProvider_validation_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     api.OIDCConfig
		wantErr string
	}{
		{
			name:    "empty issuer_url",
			cfg:     api.OIDCConfig{ClientID: "cid", RedirectURI: "http://localhost/cb"},
			wantErr: "issuer_url is required",
		},
		{
			name:    "empty client_id",
			cfg:     api.OIDCConfig{IssuerURL: "https://idp.example.com", RedirectURI: "http://localhost/cb"},
			wantErr: "client_id is required",
		},
		{
			name:    "empty redirect_uri",
			cfg:     api.OIDCConfig{IssuerURL: "https://idp.example.com", ClientID: "cid"},
			wantErr: "redirect_uri is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewOIDCProvider(context.Background(), tt.cfg)
			if err == nil {
				t.Fatalf("NewOIDCProvider(%+v) = nil error, want %q", tt.cfg, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewOIDCProvider(%+v) error = %q, want containing %q", tt.cfg, err, tt.wantErr)
			}
		})
	}
}
