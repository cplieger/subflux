package authhandlers

import "github.com/go-webauthn/webauthn/protocol"

// WebAuthnLoginBeginResponse wraps WebAuthn assertion options with a session token.
// Used for login and reauth ceremonies.
type WebAuthnLoginBeginResponse struct {
	PublicKey    *protocol.CredentialAssertion `json:"publicKey"`
	SessionToken string                        `json:"session_token"`
}

// WebAuthnRegisterBeginResponse wraps WebAuthn creation options with a session token.
// Used for passkey registration ceremonies.
type WebAuthnRegisterBeginResponse struct {
	PublicKey    *protocol.CredentialCreation `json:"publicKey"`
	SessionToken string                       `json:"session_token"`
}
