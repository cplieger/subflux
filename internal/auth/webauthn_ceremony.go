package auth

import (
	"net/http"

	authwebauthn "github.com/cplieger/auth/webauthn"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// CeremonyTimeout is the maximum duration a user has to complete an auth ceremony.
const CeremonyTimeout = authwebauthn.CeremonyTimeout

// NewWebAuthn creates a configured webauthn.WebAuthn instance.
func NewWebAuthn(rpID, rpDisplayName string, rpOrigins []string) (*webauthn.WebAuthn, error) {
	return authwebauthn.NewWebAuthn(rpID, rpDisplayName, rpOrigins)
}

// BeginRegistration starts a WebAuthn registration ceremony.
func BeginRegistration(wa *webauthn.WebAuthn, user *WebAuthnUser) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	return authwebauthn.BeginRegistration(wa, user.toLibUser())
}

// FinishRegistration completes a WebAuthn registration ceremony.
func FinishRegistration(wa *webauthn.WebAuthn, user *WebAuthnUser, sessionData *webauthn.SessionData, response *http.Request) (*webauthn.Credential, error) {
	return authwebauthn.FinishRegistration(wa, user.toLibUser(), sessionData, response)
}

// BeginLogin starts a WebAuthn assertion ceremony (discoverable login).
func BeginLogin(wa *webauthn.WebAuthn) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return authwebauthn.BeginLogin(wa)
}

// BeginConditionalLogin starts a WebAuthn assertion with conditional mediation.
func BeginConditionalLogin(wa *webauthn.WebAuthn) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return authwebauthn.BeginConditionalLogin(wa)
}

// FinishLogin completes a WebAuthn assertion ceremony (discoverable login).
func FinishLogin(wa *webauthn.WebAuthn, sessionData *webauthn.SessionData, response *http.Request, userFinder func(rawID, userHandle []byte) (webauthn.User, error)) (webauthn.User, *webauthn.Credential, error) {
	return authwebauthn.FinishLogin(wa, sessionData, response, userFinder)
}
