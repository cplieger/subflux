package authhandlers

import (
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/go-webauthn/webauthn/protocol"
)

// WebAuthnLoginBeginResponse wraps WebAuthn assertion options with a session token.
// Used for login ceremonies.
type WebAuthnLoginBeginResponse struct {
	PublicKey    *protocol.CredentialAssertion `json:"publicKey"`
	SessionToken string                        `json:"session_token"`
}

// UserInfo is one entry of the GET /api/auth/users response.
type UserInfo struct {
	CreatedAt time.Time `json:"created_at"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      auth.Role `json:"role"`
	ID        int64     `json:"id"`
	Enabled   bool      `json:"enabled"`
}

// APIKeyInfo is one entry of the GET /api/auth/apikeys response.
type APIKeyInfo struct {
	CreatedAt time.Time `json:"created_at"`
	KeyPrefix string    `json:"key_prefix"`
	KeySuffix string    `json:"key_suffix"`
	Label     string    `json:"label"`
	ID        int64     `json:"id"`
}

// PasskeyInfo is one entry of the GET /api/auth/passkeys response.
type PasskeyInfo struct {
	CreatedAt      time.Time `json:"created_at"`
	Name           string    `json:"name"`
	Transport      string    `json:"transport,omitempty"`
	ID             int64     `json:"id"`
	BackupEligible bool      `json:"backup_eligible"`
}

// WebAuthnRegisterBeginResponse wraps WebAuthn creation options with a session token.
// Used for passkey registration ceremonies.
type WebAuthnRegisterBeginResponse struct {
	PublicKey    *protocol.CredentialCreation `json:"publicKey"`
	SessionToken string                       `json:"session_token"`
}
