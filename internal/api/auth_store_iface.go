package api

import (
	"context"
	"time"
)

// UserStore persists user account data.
type UserStore interface {
	CreateUser(ctx context.Context, user *User) error
	GetUserByID(ctx context.Context, id int64) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByOIDCSub(ctx context.Context, issuer, sub string) (*User, error)
	ListUsers(ctx context.Context) ([]User, error)
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, id int64) error
	UserCount(ctx context.Context) (int, error)
}

// SessionPersister persists session data.
type SessionPersister interface {
	CreateSession(ctx context.Context, sess *Session) error
	GetSessionByHash(ctx context.Context, tokenHash string) (*Session, error)
	UpdateSessionActivity(ctx context.Context, tokenHash string, now time.Time) error
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error
	CleanupExpiredSessions(ctx context.Context, now time.Time, idleTimeout, absTimeout time.Duration) (int64, error)
}

// PasskeyStore persists WebAuthn/FIDO2 credentials.
type PasskeyStore interface {
	CreatePasskey(ctx context.Context, cred *PasskeyCredential) error
	GetPasskeysByUserID(ctx context.Context, userID int64) ([]PasskeyCredential, error)
	GetPasskeyByCredentialID(ctx context.Context, credID []byte) (*PasskeyCredential, error)
	UpdatePasskeyAfterLogin(ctx context.Context, credID []byte, signCount uint32, flags PasskeyFlags) error
	RenamePasskey(ctx context.Context, id, userID int64, name string) error
	DeletePasskey(ctx context.Context, id, userID int64) error
	PasskeyCountForUser(ctx context.Context, userID int64) (int, error)
}

// KeyStore persists machine-to-machine API keys.
type KeyStore interface {
	// CreateAPIKey persists a new API key. The Key.KeyHash field must be
	// pre-computed by the caller (SHA-256 of the raw key, hex-encoded).
	CreateAPIKey(ctx context.Context, key *Key) error
	// GetAPIKeyByHash retrieves an API key by its SHA-256 hash (hex-encoded,
	// lowercase). The hash is computed by the auth middleware from the raw
	// key prefix+secret. Returns nil and no error if no key matches.
	GetAPIKeyByHash(ctx context.Context, hash string) (*Key, error)
	// ListAPIKeysByUserID returns all API keys belonging to the given user.
	ListAPIKeysByUserID(ctx context.Context, userID int64) ([]Key, error)
	// DeleteAPIKey removes an API key. Both id and userID are required to
	// prevent cross-user deletion.
	DeleteAPIKey(ctx context.Context, id, userID int64) error
}

// OIDCStateStore persists OIDC authentication state.
type OIDCStateStore interface {
	CreateOIDCState(ctx context.Context, state, nonce, codeVerifier, redirectURI string) error
	ConsumeOIDCState(ctx context.Context, state string) (nonce, codeVerifier, redirectURI string, err error)
	CleanupExpiredOIDCStates(ctx context.Context, now time.Time, maxAge time.Duration) (int64, error)
}
