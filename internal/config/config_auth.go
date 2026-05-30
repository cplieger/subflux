package config

import (
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
