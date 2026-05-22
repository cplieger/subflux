package config

import (
	"context"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Auth config tests
// =============================================================================

func TestAuthConfig_Defaults(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}

	// basic_enabled defaults to true.
	if !cfg.BasicAuthEnabled() {
		t.Error("BasicAuthEnabled() = false, want true (default)")
	}

	// oidc_enabled defaults to false.
	if cfg.OIDCEnabled() {
		t.Error("OIDCEnabled() = true, want false (default)")
	}

	// Session timeouts default to 24h idle, 7d absolute.
	if cfg.SessionIdleTimeout() != 24*time.Hour {
		t.Errorf("SessionIdleTimeout() = %v, want 24h", cfg.SessionIdleTimeout())
	}
	if cfg.SessionAbsoluteTimeout() != 7*24*time.Hour {
		t.Errorf("SessionAbsoluteTimeout() = %v, want 168h", cfg.SessionAbsoluteTimeout())
	}

	// CheckBreachedPasswords defaults to true.
	if !cfg.CheckBreachedPasswords() {
		t.Error("CheckBreachedPasswords() = false, want true (default)")
	}
}

func TestAuthConfig_DisableAuth(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  disable_auth: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}

	if !cfg.AuthDisabled() {
		t.Error("AuthDisabled() = false, want true")
	}
}

// --- Auth accessor coverage ---

func TestAuthEnabled_always_true(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if !cfg.AuthEnabled() {
		t.Error("AuthEnabled() = false, want true")
	}
}

func TestWebAuthnRPID_empty_default(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if got := cfg.WebAuthnRPID(); got != "" {
		t.Errorf("WebAuthnRPID() = %q, want empty", got)
	}
}

func TestWebAuthnRPID_configured(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  webauthn_rp_id: "subflux.example.com"
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if got := cfg.WebAuthnRPID(); got != "subflux.example.com" {
		t.Errorf("WebAuthnRPID() = %q, want %q", got, "subflux.example.com")
	}
}

func TestOIDCConfig_returns_configured_values(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  oidc_enabled: true
  oidc_auto_redirect: true
  oidc:
    issuer_url: "https://auth.example.com/app/o/subflux/"
    client_id: "my-client"
    client_secret: "my-secret"
    redirect_uri: "https://subflux.example.com/api/auth/oidc/callback"
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	oidc := cfg.OIDCConfig()
	if oidc.IssuerURL != "https://auth.example.com/app/o/subflux/" {
		t.Errorf("OIDCConfig().IssuerURL = %q, want %q", oidc.IssuerURL, "https://auth.example.com/app/o/subflux/")
	}
	if oidc.ClientID != "my-client" {
		t.Errorf("OIDCConfig().ClientID = %q, want %q", oidc.ClientID, "my-client")
	}
	if oidc.ClientSecret != "my-secret" {
		t.Errorf("OIDCConfig().ClientSecret = %q, want %q", oidc.ClientSecret, "my-secret")
	}
	if oidc.RedirectURI != "https://subflux.example.com/api/auth/oidc/callback" {
		t.Errorf("OIDCConfig().RedirectURI = %q, want %q", oidc.RedirectURI, "https://subflux.example.com/api/auth/oidc/callback")
	}
	if !oidc.AutoRedirect {
		t.Error("OIDCConfig().AutoRedirect = false, want true")
	}
}

func TestBasicAuthEnabled_explicit_false(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  basic_enabled: false
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if cfg.BasicAuthEnabled() {
		t.Error("BasicAuthEnabled() = true, want false")
	}
}

func TestSessionIdleTimeout_configured(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  session_idle_timeout: "12h"
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if got := cfg.SessionIdleTimeout(); got != 12*time.Hour {
		t.Errorf("SessionIdleTimeout() = %v, want 12h", got)
	}
}

func TestSessionAbsoluteTimeout_configured(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  session_absolute_timeout: "14D"
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	want := 14 * 24 * time.Hour
	if got := cfg.SessionAbsoluteTimeout(); got != want {
		t.Errorf("SessionAbsoluteTimeout() = %v, want %v", got, want)
	}
}

func TestCheckBreachedPasswords_explicit_false(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  check_breached_passwords: false
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if cfg.CheckBreachedPasswords() {
		t.Error("CheckBreachedPasswords() = true, want false")
	}
}

// --- TOTPEncryptionKey coverage ---

func TestTOTPEncryptionKey_generates_ephemeral_when_empty(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	key, keyErr := cfg.TOTPEncryptionKey()
	if keyErr != nil {
		t.Fatalf("TOTPEncryptionKey() unexpected error: %v", keyErr)
	}
	if len(key) != 32 {
		t.Errorf("TOTPEncryptionKey() len = %d, want 32", len(key))
	}
	// Second call should return the same key (cached in TOTPKey field).
	key2, keyErr2 := cfg.TOTPEncryptionKey()
	if keyErr2 != nil {
		t.Fatalf("TOTPEncryptionKey() second call unexpected error: %v", keyErr2)
	}
	if len(key2) != 32 {
		t.Errorf("TOTPEncryptionKey() second call len = %d, want 32", len(key2))
	}
}

func TestTOTPEncryptionKey_valid_hex(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  totp_encryption_key: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	key, keyErr := cfg.TOTPEncryptionKey()
	if keyErr != nil {
		t.Fatalf("TOTPEncryptionKey() unexpected error: %v", keyErr)
	}
	if len(key) != 32 {
		t.Errorf("TOTPEncryptionKey() len = %d, want 32", len(key))
	}
}

func TestTOTPEncryptionKey_errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  string
	}{
		{"invalid_hex", "not-valid-hex"},
		{"wrong_length", "0123456789abcdef0123456789abcdef"}, // 16 bytes instead of 32
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{Auth: yamlAuthConfig{TOTPKey: tc.key}}
			key, keyErr := cfg.TOTPEncryptionKey()
			if keyErr == nil {
				t.Errorf("TOTPEncryptionKey() error = nil, want error for %s", tc.name)
			}
			if key != nil {
				t.Errorf("TOTPEncryptionKey() = %v, want nil for %s", key, tc.name)
			}
		})
	}
}

// --- TOTP key validation at config load ---

func TestValidate_totp_key_errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		key        string
		wantSubstr string
	}{
		{"invalid_hex", "not-valid-hex", "totp_encryption_key"},
		{"wrong_length", "0123456789abcdef0123456789abcdef", "32 bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			yaml := minimalValidYAML() + "\nauth:\n  totp_encryption_key: \"" + tc.key + "\"\n"
			_, err := LoadFromBytes(context.Background(), []byte(yaml))
			if err == nil {
				t.Fatalf("LoadFromBytes() = nil, want error for %s totp key", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %q, want mention of %q", err, tc.wantSubstr)
			}
		})
	}
}
