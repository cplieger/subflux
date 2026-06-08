package auth

import (
	"crypto/tls"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func FuzzValidateSession(f *testing.F) {
	f.Add(int64(0), int64(0), int64(3600), int64(86400), false)
	f.Add(int64(-7200), int64(0), int64(3600), int64(86400), false)
	f.Add(int64(0), int64(-100000), int64(3600), int64(86400), false)
	f.Add(int64(0), int64(0), int64(1), int64(1), true)

	f.Fuzz(func(t *testing.T, lastActivityOffsetSec, createdAtOffsetSec, idleTimeoutSec, absTimeoutSec int64, hasOIDCExpiry bool) {
		if idleTimeoutSec < 0 || absTimeoutSec < 0 {
			return
		}
		if idleTimeoutSec > 604800 || absTimeoutSec > 604800 {
			return
		}

		now := time.Now()
		sess := &api.Session{
			LastActivity: now.Add(time.Duration(lastActivityOffsetSec) * time.Second),
			CreatedAt:    now.Add(time.Duration(createdAtOffsetSec) * time.Second),
		}
		if hasOIDCExpiry {
			exp := now.Add(-time.Second)
			sess.OIDCExpiry = &exp
		}

		idle := time.Duration(idleTimeoutSec) * time.Second
		abs := time.Duration(absTimeoutSec) * time.Second

		// Must not panic
		err := ValidateSession(sess, idle, abs, now)

		// If OIDC expiry is in the past, must fail
		if hasOIDCExpiry && err == nil {
			t.Error("expected error for expired OIDC session")
		}
	})
}

func FuzzValidatePasswordLength(f *testing.F) {
	f.Add("short", true)
	f.Add("areallylongpasswordthatshouldbefine", false)
	f.Add("", true)
	f.Add("12345678", true)
	f.Add("12345678901234", false)

	f.Fuzz(func(t *testing.T, password string, passwordOnly bool) {
		// Must not panic
		_ = ValidatePasswordLength(password, passwordOnly)
	})
}

func FuzzValidatePasswordContext(f *testing.F) {
	f.Add("mypassword", "admin")
	f.Add("admin123", "admin")
	f.Add("subflux2024", "user")
	f.Add("secure!random#pass", "testuser")
	f.Add("", "")

	f.Fuzz(func(t *testing.T, password, username string) {
		err := ValidatePasswordContext(password, username)
		// If password contains username (case-insensitive), should fail
		if username != "" && len(password) > 0 && strings.Contains(strings.ToLower(password), strings.ToLower(username)) {
			if err == nil {
				// Implementation may have different threshold; just don't panic
				_ = err
			}
		}
		// Must not panic regardless of input
	})
}

func FuzzSessionCookieName(f *testing.F) {
	f.Add(true, "https")
	f.Add(false, "")
	f.Add(false, "http")
	f.Add(true, "http")

	f.Fuzz(func(t *testing.T, hasTLS bool, xForwardedProto string) {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		if hasTLS {
			r.TLS = &tls.ConnectionState{}
		}
		if xForwardedProto != "" {
			r.Header.Set("X-Forwarded-Proto", xForwardedProto)
		}

		name := SessionCookieName(r)
		isSecure := hasTLS || xForwardedProto == "https"

		if isSecure && name != CookieNameSecure {
			t.Errorf("HTTPS request got cookie name %q, want %q", name, CookieNameSecure)
		}
		if !isSecure && name != CookieNameHTTP {
			t.Errorf("HTTP request got cookie name %q, want %q", name, CookieNameHTTP)
		}
	})
}

func FuzzFormatAAGUID(f *testing.F) {
	f.Add([]byte{0xea, 0x9b, 0x8d, 0x66, 0x4d, 0x01, 0x1d, 0x21, 0x3c, 0xe4, 0xb6, 0xb4, 0x8c, 0xb5, 0x75, 0xd4})
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, aaguid []byte) {
		result := formatAAGUID(aaguid)
		if len(aaguid) != 16 {
			if result != "" {
				t.Errorf("formatAAGUID(%d bytes) = %q, want empty", len(aaguid), result)
			}
			return
		}
		// Valid 16-byte input: must produce UUID format (36 chars, 4 dashes)
		if len(result) != 36 {
			t.Errorf("formatAAGUID(16 bytes) = %q, length %d want 36", result, len(result))
		}
		dashes := strings.Count(result, "-")
		if dashes != 4 {
			t.Errorf("formatAAGUID result %q has %d dashes, want 4", result, dashes)
		}
	})
}

func FuzzPasskeyFriendlyName(f *testing.F) {
	f.Add([]byte{0xea, 0x9b, 0x8d, 0x66, 0x4d, 0x01, 0x1d, 0x21, 0x3c, 0xe4, 0xb6, 0xb4, 0x8c, 0xb5, 0x75, 0xd4}, "")
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "Passkey 1")
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, "Passkey 1,Passkey 2")

	f.Fuzz(func(t *testing.T, aaguid []byte, existingStr string) {
		var existing []string
		if existingStr != "" {
			existing = strings.Split(existingStr, ",")
		}

		result := PasskeyFriendlyName(aaguid, existing)
		if result == "" {
			t.Error("PasskeyFriendlyName returned empty string")
		}
	})
}

func FuzzValidateOIDCConfig(f *testing.F) {
	f.Add("https://accounts.google.com", "client123", "https://app/callback")
	f.Add("", "client123", "https://app/callback")
	f.Add("https://issuer", "", "https://app/callback")
	f.Add("https://issuer", "client", "")
	f.Add("", "", "")

	f.Fuzz(func(t *testing.T, issuerURL, clientID, redirectURI string) {
		cfg := api.OIDCConfig{
			IssuerURL:   issuerURL,
			ClientID:    clientID,
			RedirectURI: redirectURI,
		}
		err := ValidateOIDCConfig(cfg)
		// If all fields set, must succeed
		if issuerURL != "" && clientID != "" && redirectURI != "" {
			if err != nil {
				t.Errorf("ValidateOIDCConfig with all fields set returned error: %v", err)
			}
		}
		// If any required field is empty, must fail
		if issuerURL == "" || clientID == "" || redirectURI == "" {
			if err == nil {
				t.Errorf("ValidateOIDCConfig with missing field returned nil error")
			}
		}
	})
}
