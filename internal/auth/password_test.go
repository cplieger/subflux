package auth

import (
	"testing"

	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 1: Password hash round-trip
// **Validates: Requirements 1.1, 1.4, 16.1**
func TestProperty_PasswordHashRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		password := rapid.StringN(8, 128, -1).Draw(t, "password")

		hash, err := HashPassword(password)
		if err != nil {
			t.Fatalf("HashPassword(%q) error: %v", password, err)
		}

		// Same password must verify.
		ok, err := VerifyPassword(password, hash)
		if err != nil {
			t.Fatalf("VerifyPassword(same) error: %v", err)
		}
		if !ok {
			t.Fatalf("VerifyPassword(same) = false, want true")
		}

		// Different password must not verify.
		other := password + "x"
		ok, err = VerifyPassword(other, hash)
		if err != nil {
			t.Fatalf("VerifyPassword(different) error: %v", err)
		}
		if ok {
			t.Fatalf("VerifyPassword(different) = true, want false")
		}
	})
}

// Feature: subflux-authentication, Property 2: Unique salt per hash
// **Validates: Requirements 1.3**
func TestProperty_UniqueSaltPerHash(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		password := rapid.StringN(1, 128, -1).Draw(t, "password")

		hash1, err := HashPassword(password)
		if err != nil {
			t.Fatalf("HashPassword(1) error: %v", err)
		}

		hash2, err := HashPassword(password)
		if err != nil {
			t.Fatalf("HashPassword(2) error: %v", err)
		}

		if hash1 == hash2 {
			t.Fatalf("two hashes of same password are identical (salt reuse): %s", hash1)
		}
	})
}

// Feature: subflux-authentication, Property 3: Password length validation
// **Validates: Requirements 1.6**
func TestProperty_PasswordLengthValidation(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// passwordOnly=false: threshold is 8.
		short := rapid.StringN(0, 7, -1).Draw(t, "short")
		if err := ValidatePasswordLength(short, false); err == nil {
			t.Fatalf("ValidatePasswordLength(%q, false) = nil, want error", short)
		}

		valid := rapid.StringN(8, 128, -1).Draw(t, "valid")
		if err := ValidatePasswordLength(valid, false); err != nil {
			t.Fatalf("ValidatePasswordLength(%q, false) = %v, want nil", valid, err)
		}

		// passwordOnly=true: threshold is 15.
		shortSolo := rapid.StringN(0, 14, -1).Draw(t, "shortSolo")
		if err := ValidatePasswordLength(shortSolo, true); err == nil {
			t.Fatalf("ValidatePasswordLength(%q, true) = nil, want error", shortSolo)
		}

		validSolo := rapid.StringN(15, 128, -1).Draw(t, "validSolo")
		if err := ValidatePasswordLength(validSolo, true); err != nil {
			t.Fatalf("ValidatePasswordLength(%q, true) = %v, want nil", validSolo, err)
		}
	})
}

func TestVerifyPassword_rejects_malformed_hashes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		hash string
	}{
		{"empty_string", ""},
		{"wrong_algorithm", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"},
		{"too_few_parts", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA"},
		{"bad_version", "$argon2id$v=abc$m=19456,t=2,p=1$c2FsdA$aGFzaA"},
		{"bad_params", "$argon2id$v=19$garbage$c2FsdA$aGFzaA"},
		{"bad_salt_base64", "$argon2id$v=19$m=19456,t=2,p=1$!!!invalid!!!$aGFzaA"},
		{"bad_key_base64", "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$!!!invalid!!!"},
		{"wrong_version_number", "$argon2id$v=18$m=19456,t=2,p=1$c2FsdA$aGFzaA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, err := VerifyPassword("anything", tc.hash)
			if err == nil {
				t.Fatalf("VerifyPassword(_, %q) = (%v, nil), want error", tc.hash, ok)
			}
			if ok {
				t.Fatalf("VerifyPassword(_, %q) = (true, _), want false on error", tc.hash)
			}
		})
	}
}
