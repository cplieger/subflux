package auth

import (
	"errors"

	"subflux/internal/api"

	authlib "github.com/cplieger/auth"
	authwebauthn "github.com/cplieger/auth/webauthn"

	"github.com/go-webauthn/webauthn/webauthn"
)

// WebAuthnUser adapts api.User + credentials to the webauthn.User interface.
type WebAuthnUser struct {
	User        *api.User
	Credentials []api.PasskeyCredential
}

// NewWebAuthnUser returns a WebAuthnUser with the given user and credentials.
func NewWebAuthnUser(user *api.User, creds []api.PasskeyCredential) (*WebAuthnUser, error) {
	if user == nil {
		return nil, errors.New("auth: NewWebAuthnUser called with nil user")
	}
	return &WebAuthnUser{User: user, Credentials: creds}, nil
}

// toLibUser converts a WebAuthnUser to the lib's webauthn.User for delegation.
func (u *WebAuthnUser) toLibUser() *authwebauthn.User {
	libCreds := make([]authlib.PasskeyCredential, len(u.Credentials))
	for i := range u.Credentials {
		libCreds[i] = toLibPasskeyCred(&u.Credentials[i])
	}
	libUser := &authlib.User{
		ID:          u.User.ID,
		Username:    u.User.Username,
		DisplayName: u.User.DisplayName,
		Email:       u.User.Email,
		Role:        authlib.Role(u.User.Role),
		Enabled:     u.User.Enabled,
	}
	wu, _ := authwebauthn.NewWebAuthnUser(libUser, libCreds)
	return wu
}

// WebAuthnID encodes the user ID as a binary varint.
func (u *WebAuthnUser) WebAuthnID() []byte {
	return u.toLibUser().WebAuthnID()
}

// WebAuthnName returns the username.
func (u *WebAuthnUser) WebAuthnName() string {
	return u.User.Username
}

// WebAuthnDisplayName returns the display name, falling back to username.
func (u *WebAuthnUser) WebAuthnDisplayName() string {
	if u.User.DisplayName != "" {
		return u.User.DisplayName
	}
	return u.User.Username
}

// WebAuthnCredentials converts the stored credentials to webauthn.Credential.
func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.toLibUser().WebAuthnCredentials()
}

// KnownAAGUIDs is the registry of known authenticator AAGUIDs.
var KnownAAGUIDs = authwebauthn.KnownAAGUIDs

// PasskeyFriendlyName returns a human-friendly name for a passkey based on its AAGUID.
func PasskeyFriendlyName(aaguid []byte, existingNames []string) string {
	return authwebauthn.PasskeyFriendlyName(aaguid, existingNames)
}

// APICredentialToWebAuthn converts an api.PasskeyCredential to a webauthn.Credential.
func APICredentialToWebAuthn(c *api.PasskeyCredential) webauthn.Credential {
	libCred := toLibPasskeyCred(c)
	return authwebauthn.APICredentialToWebAuthn(&libCred)
}

// WebAuthnCredentialToAPI converts a webauthn.Credential to an api.PasskeyCredential.
func WebAuthnCredentialToAPI(c *webauthn.Credential, userID int64, name string) *api.PasskeyCredential {
	libCred := authwebauthn.CredentialToAPI(c, userID, name)
	return fromLibPasskeyCred(libCred)
}

// toLibPasskeyCred converts api.PasskeyCredential to authlib.PasskeyCredential.
func toLibPasskeyCred(c *api.PasskeyCredential) authlib.PasskeyCredential {
	return authlib.PasskeyCredential{
		CreatedAt:       c.CreatedAt,
		AttestationType: c.AttestationType,
		Transport:       c.Transport,
		Name:            c.Name,
		CredentialID:    c.CredentialID,
		PublicKey:       c.PublicKey,
		AAGUID:          c.AAGUID,
		RawAttestation:  c.RawAttestation,
		ID:              c.ID,
		UserID:          c.UserID,
		SignCount:       c.SignCount,
		BackupEligible:  c.BackupEligible,
		BackupState:     c.BackupState,
		UserPresent:     c.UserPresent,
		UserVerified:    c.UserVerified,
	}
}

// fromLibPasskeyCred converts authlib.PasskeyCredential to api.PasskeyCredential.
func fromLibPasskeyCred(c *authlib.PasskeyCredential) *api.PasskeyCredential {
	return &api.PasskeyCredential{
		CreatedAt:       c.CreatedAt,
		AttestationType: c.AttestationType,
		Transport:       c.Transport,
		Name:            c.Name,
		CredentialID:    c.CredentialID,
		PublicKey:       c.PublicKey,
		AAGUID:          c.AAGUID,
		RawAttestation:  c.RawAttestation,
		ID:              c.ID,
		UserID:          c.UserID,
		SignCount:       c.SignCount,
		BackupEligible:  c.BackupEligible,
		BackupState:     c.BackupState,
		UserPresent:     c.UserPresent,
		UserVerified:    c.UserVerified,
	}
}
