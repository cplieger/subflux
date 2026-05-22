package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 5: TOTP time window acceptance
// **Validates: Requirements 2.3**
func TestProperty_TOTPTimeWindowAcceptance(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		username := rapid.StringN(1, 32, -1).Draw(t, "username")

		secret, uri, err := GenerateTOTPSecret(username, "SubfluxTest")
		if err != nil {
			t.Fatalf("GenerateTOTPSecret error: %v", err)
		}

		if secret == "" {
			t.Fatalf("secret is empty")
		}
		if uri == "" {
			t.Fatalf("URI is empty")
		}

		// Generate a code for the current time and validate it.
		now := time.Now()
		code, err := totp.GenerateCode(secret, now)
		if err != nil {
			t.Fatalf("GenerateCode(now) error: %v", err)
		}

		if !ValidateTOTPCode(secret, code) {
			t.Fatalf("ValidateTOTPCode rejected code generated for current time")
		}

		// Generate a code for 90 seconds in the past (outside ±1 window).
		oldCode, err := totp.GenerateCode(secret, now.Add(-90*time.Second))
		if err != nil {
			t.Fatalf("GenerateCode(-90s) error: %v", err)
		}

		// The old code should fail unless it happens to collide with a
		// valid window code (unlikely but possible). We only assert failure
		// when the old code differs from the current code.
		if oldCode != code && ValidateTOTPCode(secret, oldCode) {
			t.Fatalf("ValidateTOTPCode accepted code from 90s ago")
		}
	})
}

// Feature: subflux-authentication, Property 7: Recovery code hash round-trip
// **Validates: Requirements 2.6**
func TestProperty_RecoveryCodeHashRoundTrip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		codes, hashes, err := GenerateRecoveryCodes()
		if err != nil {
			t.Fatalf("GenerateRecoveryCodes error: %v", err)
		}

		if len(codes) != 8 {
			t.Fatalf("expected 8 codes, got %d", len(codes))
		}
		if len(hashes) != 8 {
			t.Fatalf("expected 8 hashes, got %d", len(hashes))
		}

		// All 8 codes must be unique.
		codeSet := make(map[string]struct{}, 8)
		for _, c := range codes {
			if _, dup := codeSet[c]; dup {
				t.Fatalf("duplicate code: %s", c)
			}
			codeSet[c] = struct{}{}
		}

		// All 8 hashes must be unique.
		hashSet := make(map[string]struct{}, 8)
		for _, h := range hashes {
			if _, dup := hashSet[h]; dup {
				t.Fatalf("duplicate hash: %s", h)
			}
			hashSet[h] = struct{}{}
		}

		// Each hash must equal HexSHA256(code) — the same function used in
		// the login flow to hash the user-provided code before the DB lookup.
		for i, code := range codes {
			if hashes[i] != HexSHA256(code) {
				t.Fatalf("code[%d] hash mismatch: got %s, want HexSHA256(%q)=%s",
					i, hashes[i], code, HexSHA256(code))
			}
		}

		// A different code must produce a different hash.
		for i, code := range codes {
			otherIdx := (i + 1) % len(hashes)
			if HexSHA256(code) == hashes[otherIdx] {
				t.Fatalf("code[%d] hashed to hash[%d] (hash collision)", i, otherIdx)
			}
		}
	})
}

func TestRandomAlphanumeric_output_properties(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		length int
	}{
		{"standard_length", 8},
		{"short", 1},
		{"long", 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := randomAlphanumeric(tt.length)
			if err != nil {
				t.Fatalf("randomAlphanumeric(%d) error: %v", tt.length, err)
			}
			if len(got) != tt.length {
				t.Errorf("randomAlphanumeric(%d) length = %d, want %d", tt.length, len(got), tt.length)
			}
			for i, c := range got {
				found := false
				for _, a := range recoveryAlphabet {
					if c == a {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("randomAlphanumeric(%d)[%d] = %q, not in alphabet %q", tt.length, i, string(c), recoveryAlphabet)
				}
			}
		})
	}
}

// FuzzValidateTOTPCode verifies ValidateTOTPCode never panics on arbitrary
// secret/code combinations, including malformed base32 and unexpected lengths.
func FuzzValidateTOTPCode(f *testing.F) {
	f.Add("JBSWY3DPEHPK3PXP", "123456")
	f.Add("", "")
	f.Add("INVALIDBASE32!!!", "000000")
	f.Add("JBSWY3DPEHPK3PXP", "99999999999")
	f.Add("A", "1")

	f.Fuzz(func(t *testing.T, secret, code string) {
		// Must not panic on any input.
		_ = ValidateTOTPCode(secret, code)
	})
}
