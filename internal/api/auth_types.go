package api

import "github.com/cplieger/auth"

// --- Authentication types ---
//
// Subflux's auth domain types are type aliases of the standalone
// github.com/cplieger/auth library types. The library is the single source
// of truth; aliasing lets subflux pass its own api.* values straight into the
// library's authenticator, verifiers, WebAuthn, and OIDC helpers with no
// conversion, while keeping the rest of the codebase referring to api.* names.

// AuthMethod is the authentication mechanism used to establish a session.
type AuthMethod = auth.Method

// Auth method identifiers stored in sessions and used for method guards.
const (
	MethodPassword = auth.MethodPassword
	MethodPasskey  = auth.MethodPasskey
	MethodOIDC     = auth.MethodOIDC
)

// Role identifies a user's authorization level.
type Role = auth.Role

// User role constants. Users have exactly one role; admin is a strict
// superset of user and bypasses user-scoped role checks.
const (
	RoleAdmin = auth.RoleAdmin
	RoleUser  = auth.RoleUser
)

// User represents an authenticated user account.
type User = auth.User

// Session represents a server-side authenticated session.
type Session = auth.Session

// PasskeyCredential represents a WebAuthn/FIDO2 credential registered to a user.
type PasskeyCredential = auth.PasskeyCredential

// PasskeyFlags holds the boolean authenticator flags for a credential update.
type PasskeyFlags = auth.PasskeyFlags

// Key represents a machine-to-machine API key for a user.
type Key = auth.Key

// OIDCConfig holds OIDC provider settings.
type OIDCConfig = auth.OIDCConfig
