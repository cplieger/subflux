package auth

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"subflux/internal/api"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// WebAuthnUser adapts api.User + credentials to the webauthn.User interface.
// User must never be nil; use NewWebAuthnUser to construct.
type WebAuthnUser struct {
	User        *api.User
	Credentials []api.PasskeyCredential
}

// NewWebAuthnUser returns a WebAuthnUser with the given user and credentials.
// Returns an error if user is nil.
func NewWebAuthnUser(user *api.User, creds []api.PasskeyCredential) (*WebAuthnUser, error) {
	if user == nil {
		return nil, errors.New("auth: NewWebAuthnUser called with nil user")
	}
	return &WebAuthnUser{User: user, Credentials: creds}, nil
}

// WebAuthnID encodes the user ID as a binary varint.
func (u *WebAuthnUser) WebAuthnID() []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutVarint(buf, u.User.ID)
	return buf[:n]
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
	creds := make([]webauthn.Credential, len(u.Credentials))
	for i := range u.Credentials {
		creds[i] = APICredentialToWebAuthn(&u.Credentials[i])
	}
	return creds
}

// AAGUIDEntry maps an authenticator AAGUID (as UUID string) to a friendly name.
type AAGUIDEntry struct {
	UUID string
	Name string
}

// KnownAAGUIDs is the registry of known authenticator AAGUIDs and their
// friendly names. Source: https://github.com/passkeydeveloper/passkey-authenticator-aaguids
var KnownAAGUIDs = []AAGUIDEntry{
	{"ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4", "Google Password Manager"},
	{"adce0002-35bc-c60a-648b-0b25f1f05503", "Chrome on Mac"},
	{"08987058-cadc-4b81-b6e1-30de50dcbe96", "Windows Hello"},
	{"9ddd1817-af5a-4672-a2b9-3e3dd95000a9", "Windows Hello"},
	{"6028b017-b1d4-4c02-b4b3-afcdafc96bb2", "Windows Hello"},
	{"dd4ec289-e01d-41c9-bb89-70fa845d4bf2", "iCloud Keychain"},
	{"fbfc3007-154e-4ecc-8c0b-6e020557d7bd", "iCloud Keychain"},
	{"d548826e-79b4-db40-a3d8-11116f7e8349", "Bitwarden"},
	{"b5397723-31d4-4c13-b037-37be46e30e9e", "1Password"},
	{"bada5566-a7aa-401f-bd96-45619a55120d", "1Password"},
	{"2fc0579f-8113-47ea-b116-bb5a8db9202a", "YubiKey 5"},
	{"fa2b99dc-9e39-4257-8f92-4a30d23c4118", "YubiKey 5 NFC"},
}

// knownAAGUIDMap is the lookup map built from KnownAAGUIDs for O(1) access.
var knownAAGUIDMap = func() map[string]string {
	m := make(map[string]string, len(KnownAAGUIDs))
	for _, e := range KnownAAGUIDs {
		m[e.UUID] = e.Name
	}
	return m
}()

// formatAAGUID formats a 16-byte AAGUID as a UUID string (8-4-4-4-12).
func formatAAGUID(aaguid []byte) string {
	if len(aaguid) != 16 {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		aaguid[0:4], aaguid[4:6], aaguid[6:8], aaguid[8:10], aaguid[10:16])
}

// PasskeyFriendlyName returns a human-friendly name for a passkey based on
// its AAGUID. Known AAGUIDs are mapped to provider names. If the name already
// exists in existingNames, a numeric suffix is appended (e.g. "Google Password
// Manager 2"). Unknown AAGUIDs fall back to "Passkey N".
func PasskeyFriendlyName(aaguid []byte, existingNames []string) string {
	uuid := formatAAGUID(aaguid)
	baseName, known := knownAAGUIDMap[uuid]
	if !known {
		// Count existing "Passkey" entries.
		n := 1
		for _, name := range existingNames {
			if name == "Passkey" || strings.HasPrefix(name, "Passkey ") {
				n++
			}
		}
		return fmt.Sprintf("Passkey %d", n)
	}

	// Count how many times this base name already appears.
	count := 0
	for _, name := range existingNames {
		if name == baseName || strings.HasPrefix(name, baseName+" ") {
			count++
		}
	}
	if count == 0 {
		return baseName
	}
	return fmt.Sprintf("%s %d", baseName, count+1)
}

// APICredentialToWebAuthn converts an api.PasskeyCredential to a webauthn.Credential.
func APICredentialToWebAuthn(c *api.PasskeyCredential) webauthn.Credential {
	var transports []protocol.AuthenticatorTransport
	if c.Transport != "" {
		for t := range strings.SplitSeq(c.Transport, ",") {
			transports = append(transports, protocol.AuthenticatorTransport(t))
		}
	}

	cred := webauthn.Credential{
		ID:              c.CredentialID,
		PublicKey:       c.PublicKey,
		AttestationType: c.AttestationType,
		Transport:       transports,
		Flags: webauthn.CredentialFlags{
			UserPresent:    c.UserPresent,
			UserVerified:   c.UserVerified,
			BackupEligible: c.BackupEligible,
			BackupState:    c.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:    c.AAGUID,
			SignCount: c.SignCount,
		},
	}

	// Restore raw attestation data if persisted.
	if len(c.RawAttestation) > 0 {
		if err := json.Unmarshal(c.RawAttestation, &cred.Attestation); err != nil {
			// Corrupted attestation data is non-fatal; the credential still works
			// for authentication, just not for future MDS verification.
			slog.Warn("webauthn: corrupted attestation data, skipping MDS verification",
				"credential_id", hex.EncodeToString(c.CredentialID[:min(8, len(c.CredentialID))]),
				"error", err)
		}
	}

	return cred
}

// WebAuthnCredentialToAPI converts a webauthn.Credential to an api.PasskeyCredential.
func WebAuthnCredentialToAPI(c *webauthn.Credential, userID int64, name string) *api.PasskeyCredential {
	transports := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		transports = append(transports, string(t))
	}

	// Serialize raw attestation data for future FIDO MDS verification.
	var rawAttestation []byte
	if len(c.Attestation.Object) > 0 || len(c.Attestation.ClientDataJSON) > 0 {
		var err error
		rawAttestation, err = json.Marshal(c.Attestation)
		if err != nil {
			slog.Warn("webauthn: failed to marshal attestation data",
				"credential_id", hex.EncodeToString(c.ID[:min(8, len(c.ID))]),
				"error", err)
			rawAttestation = nil
		}
	}

	return &api.PasskeyCredential{
		UserID:          userID,
		CredentialID:    c.ID,
		PublicKey:       c.PublicKey,
		AAGUID:          c.Authenticator.AAGUID,
		AttestationType: c.AttestationType,
		Transport:       strings.Join(transports, ","),
		SignCount:       c.Authenticator.SignCount,
		Name:            name,
		BackupEligible:  c.Flags.BackupEligible,
		BackupState:     c.Flags.BackupState,
		UserPresent:     c.Flags.UserPresent,
		UserVerified:    c.Flags.UserVerified,
		RawAttestation:  rawAttestation,
	}
}
