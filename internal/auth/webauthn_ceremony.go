package auth

import (
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// CeremonyTimeout is the maximum duration a user has to complete an auth
// ceremony (TOTP, WebAuthn registration/login). Used for both server-side
// session expiry and client-side WebAuthn timeout configuration.
const CeremonyTimeout = 5 * time.Minute

// NewWebAuthn creates a configured webauthn.WebAuthn instance.
func NewWebAuthn(rpID, rpDisplayName string, rpOrigins []string) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: rpDisplayName,
		RPOrigins:     rpOrigins,
		Timeouts: webauthn.TimeoutsConfig{
			Login: webauthn.TimeoutConfig{
				Enforce:    true,
				Timeout:    CeremonyTimeout,
				TimeoutUVD: CeremonyTimeout,
			},
			Registration: webauthn.TimeoutConfig{
				Enforce:    true,
				Timeout:    CeremonyTimeout,
				TimeoutUVD: CeremonyTimeout,
			},
		},
	})
}

// BeginRegistration starts a WebAuthn registration ceremony.
// Requires resident key (discoverable credential) and excludes already-registered
// authenticators to prevent duplicate registrations. Requests the credProps
// extension so the browser reports whether the credential is discoverable.
func BeginRegistration(wa *webauthn.WebAuthn, user *WebAuthnUser) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	return wa.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(user.WebAuthnCredentials()).CredentialDescriptors()),
		webauthn.WithExtensions(map[string]any{"credProps": true}),
	)
}

// FinishRegistration completes a WebAuthn registration ceremony.
func FinishRegistration(wa *webauthn.WebAuthn, user *WebAuthnUser, sessionData *webauthn.SessionData, response *http.Request) (*webauthn.Credential, error) {
	return wa.FinishRegistration(user, *sessionData, response)
}

// BeginLogin starts a WebAuthn assertion ceremony (discoverable login).
func BeginLogin(wa *webauthn.WebAuthn) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return wa.BeginDiscoverableLogin()
}

// BeginUserLogin starts a WebAuthn assertion ceremony scoped to a specific
// user's credentials. Unlike BeginLogin, the resulting challenge can only be
// completed by one of the given user's passkeys. Used for reauth flows where
// we already know who the user is and just need fresh proof-of-possession.
func BeginUserLogin(wa *webauthn.WebAuthn, user *WebAuthnUser) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return wa.BeginLogin(user)
}

// BeginConditionalLogin starts a WebAuthn assertion ceremony with conditional
// mediation. The browser shows passkeys in the autofill dropdown instead of
// a full-screen modal.
func BeginConditionalLogin(wa *webauthn.WebAuthn) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return wa.BeginDiscoverableMediatedLogin(protocol.MediationConditional)
}

// FinishLogin completes a WebAuthn assertion ceremony (discoverable login).
// The userFinder callback resolves the user from the credential's user handle.
// Returns both the resolved user and the validated credential.
func FinishLogin(wa *webauthn.WebAuthn, sessionData *webauthn.SessionData, response *http.Request, userFinder func(rawID, userHandle []byte) (webauthn.User, error)) (webauthn.User, *webauthn.Credential, error) {
	return wa.FinishPasskeyLogin(userFinder, *sessionData, response)
}

// FinishUserLogin completes a WebAuthn assertion ceremony scoped to a specific
// user (started with BeginUserLogin). Returns the validated credential so the
// caller can update the stored sign count and backup flags.
func FinishUserLogin(wa *webauthn.WebAuthn, user *WebAuthnUser, sessionData *webauthn.SessionData, response *http.Request) (*webauthn.Credential, error) {
	return wa.FinishLogin(user, *sessionData, response)
}
