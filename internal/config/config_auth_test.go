package config

import (
	"context"
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
	// Disabling password login requires OIDC enabled (lockout guard).
	yaml := minimalValidYAML() + `
auth:
  basic_enabled: false
  oidc_enabled: true
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	if cfg.BasicAuthEnabled() {
		t.Error("BasicAuthEnabled() = true, want false")
	}
}

// TestBasicAuthDisabled_requires_oidc verifies the lockout guard: password
// login cannot be disabled unless OIDC is enabled.
func TestBasicAuthDisabled_requires_oidc(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
auth:
  basic_enabled: false
`
	if _, err := LoadFromBytes(context.Background(), []byte(yaml)); err == nil {
		t.Fatal("LoadFromBytes() = nil error, want lockout-guard error")
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
