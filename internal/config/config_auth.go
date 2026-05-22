package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"subflux/internal/api"
	"subflux/internal/config/defaults"
)

// --- AuthConfigProvider methods ---

// AuthEnabled returns true once setup is complete (any user exists).
// The actual check is done at the middleware level; config always returns true.
func (c *Config) AuthEnabled() bool { return true }

// BasicAuthEnabled returns whether password login is enabled.
// Defaults to true if not explicitly set.
func (c *Config) BasicAuthEnabled() bool {
	if c.Auth.BasicEnabled == nil {
		return true
	}
	return *c.Auth.BasicEnabled
}

// OIDCEnabled returns whether OIDC login is enabled.
func (c *Config) OIDCEnabled() bool { return c.Auth.OIDCEnabled }

// OIDCConfig returns the OIDC provider settings.
func (c *Config) OIDCConfig() api.OIDCConfig {
	return api.OIDCConfig{
		IssuerURL:    c.Auth.OIDC.IssuerURL,
		ClientID:     c.Auth.OIDC.ClientID,
		ClientSecret: c.Auth.OIDC.ClientSecret,
		RedirectURI:  c.Auth.OIDC.RedirectURI,
		AutoRedirect: c.Auth.OIDCAutoRedirect,
	}
}

// DefaultSessionIdleTimeout is the session idle timeout when not configured.
const DefaultSessionIdleTimeout = defaults.DefaultSessionIdleTimeout

// DefaultSessionAbsoluteTimeout is the session absolute timeout when not configured.
const DefaultSessionAbsoluteTimeout = defaults.DefaultSessionAbsoluteTimeout

// SessionIdleTimeout returns the session idle timeout.
// Defaults to 24 hours if not configured.
func (c *Config) SessionIdleTimeout() time.Duration {
	if c.Auth.SessionIdle.D == 0 {
		return DefaultSessionIdleTimeout
	}
	return c.Auth.SessionIdle.D
}

// SessionAbsoluteTimeout returns the session absolute timeout.
// Defaults to 7 days if not configured.
func (c *Config) SessionAbsoluteTimeout() time.Duration {
	if c.Auth.SessionAbsolute.D == 0 {
		return DefaultSessionAbsoluteTimeout
	}
	return c.Auth.SessionAbsolute.D
}

// validateTOTPKeyHex decodes a hex-encoded TOTP encryption key and validates
// that it is exactly 32 bytes (AES-256). Returns the decoded key or an error.
func validateTOTPKeyHex(s string) ([]byte, error) {
	key, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid totp_encryption_key (must be hex-encoded): %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid totp_encryption_key: must be 64 hex chars (32 bytes), got %d bytes", len(key))
	}
	return key, nil
}

// TOTPEncryptionKey returns the AES-256 key for TOTP secret encryption.
// If not configured, generates 32 random bytes, caches them in the config
// struct, and logs a warning. The generated key is ephemeral and will be
// lost on restart, making TOTP secrets unrecoverable.
// Thread-safe: uses sync.Once to ensure the key is initialized exactly once.
func (c *Config) TOTPEncryptionKey() ([]byte, error) {
	return c.totpKeyOnce.get(c)
}

// totpKeyInit is a sync.Once wrapper for TOTP key initialization.
type totpKeyInit struct {
	err  error
	key  []byte
	once sync.Once
}

func (t *totpKeyInit) get(c *Config) ([]byte, error) {
	t.once.Do(func() {
		if c.Auth.TOTPKey == "" {
			slog.Warn("totp_encryption_key not configured, generating ephemeral key " +
				"(TOTP secrets will be unrecoverable after restart)")
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				t.err = fmt.Errorf("generate TOTP encryption key: %w", err)
				return
			}
			c.Auth.TOTPKey = hex.EncodeToString(key)
			t.key = key
			return
		}
		t.key, t.err = validateTOTPKeyHex(c.Auth.TOTPKey)
	})
	return t.key, t.err
}

// CheckBreachedPasswords returns whether to check passwords against HIBP.
// Defaults to true if not explicitly set.
func (c *Config) CheckBreachedPasswords() bool {
	if c.Auth.CheckBreached == nil {
		return true
	}
	return *c.Auth.CheckBreached
}

// WebAuthnRPID returns the configured WebAuthn Relying Party ID.
// Returns empty string if not set (auto-detected from hostname at runtime).
func (c *Config) WebAuthnRPID() string { return c.Auth.WebAuthnRPID }

// AuthDisabled returns whether authentication is completely bypassed.
// This is an undocumented escape hatch; not part of the ConfigProvider interface.
func (c *Config) AuthDisabled() bool { return c.Auth.DisableAuth }
