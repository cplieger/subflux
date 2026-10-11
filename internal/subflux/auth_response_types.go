package subflux

import (
	"time"

	"github.com/cplieger/auth/v6"
)

// SetupStatus is the JSON response for GET /api/auth/setup.
type SetupStatus struct {
	SetupRequired bool `json:"setup_required"`
	ConfigValid   bool `json:"config_valid"`
	// PasskeyLoginAvailable reports whether a passkey login could succeed on
	// this server: a relying party is configured AND at least one stored
	// credential could answer a discoverable login. It says nothing about the
	// calling page's origin, which a GET cannot know; that half is the
	// availability probe's, which takes the origin explicitly.
	PasskeyLoginAvailable bool `json:"passkey_login_available"`
}

// WebAuthnUnavailableReason names why a passkey ceremony cannot be conducted.
//
//deadset:ignore DS1101 -- The wire generator registers it by name (internal/wirespec/wirespec.go:180); the passkey ceremony code imports the TypeScript type at internal/server/static-src/webauthn-ceremony.ts:12.
type WebAuthnUnavailableReason string

// The wire reasons, one per condition, so the client's sentence per reason is
// total: a parse-stage refusal of the probed origin is address_unusable, an IP
// host is unconfigurable, and only host_not_covered is origin_not_accepted.
const (
	WebAuthnUnavailableUnconfigured    WebAuthnUnavailableReason = "unconfigured"
	WebAuthnUnavailableUnconfigurable  WebAuthnUnavailableReason = "unconfigurable"
	WebAuthnUnavailableInitFailed      WebAuthnUnavailableReason = "init_failed"
	WebAuthnUnavailableInsecureScheme  WebAuthnUnavailableReason = "insecure_scheme"
	WebAuthnUnavailableAddressUnusable WebAuthnUnavailableReason = "address_unusable"
	WebAuthnUnavailableOrigin          WebAuthnUnavailableReason = "origin_not_accepted"
	WebAuthnUnavailableNotAllowlisted  WebAuthnUnavailableReason = "origin_not_allowlisted"
)

// WebAuthnAvailability is the JSON response for
// GET /api/auth/webauthn/availability. Reason names the condition and the
// client owns the sentence; RPID and SuggestedRPID are what make a refusal
// actionable, and neither is a secret (the RP ID rides every assertion
// challenge, the suggestion derives from the caller's own origin).
type WebAuthnAvailability struct {
	Reason        WebAuthnUnavailableReason `json:"reason,omitempty"`
	RPID          string                    `json:"rp_id,omitempty"`
	SuggestedRPID string                    `json:"suggested_rp_id,omitempty"`
	Available     bool                      `json:"available"`
}

// MeResponse is the JSON response for GET /api/auth/me.
type MeResponse struct {
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        auth.Role `json:"role"`
	ID          int64     `json:"id"`
	HasPasskeys bool      `json:"has_passkeys"`
	OIDCLinked  bool      `json:"oidc_linked"`
	HasPassword bool      `json:"has_password"`
	CanLinkOIDC bool      `json:"can_link_oidc"`
}

// LoginSuccess is the JSON response after successful login.
type LoginSuccess struct {
	Redirect string     `json:"redirect"`
	User     MeResponse `json:"user"`
}

// WebAuthnUnknownCredentialResponse signals an unknown credential to the client.
type WebAuthnUnknownCredentialResponse struct {
	Error  string `json:"error"`
	Signal string `json:"signal"`
}

// PasskeyRegistered is the JSON response after successful passkey registration.
type PasskeyRegistered struct {
	CreatedAt time.Time `json:"created_at"`
	Name      string    `json:"name"`
	Transport string    `json:"transport"`
	ID        int64     `json:"id"`
}

// KeyGenerated is the JSON response after generating an API key.
type KeyGenerated struct {
	CreatedAt time.Time `json:"created_at"`
	Key       string    `json:"key"`
	KeyPrefix string    `json:"key_prefix"`
	KeySuffix string    `json:"key_suffix"`
	Label     string    `json:"label"`
	ID        int64     `json:"id"`
}

// AdminUserCreatedResponse is the JSON response after admin creates a user.
type AdminUserCreatedResponse struct {
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Role     auth.Role `json:"role"`
	ID       int64     `json:"id"`
}
