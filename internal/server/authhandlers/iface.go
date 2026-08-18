package authhandlers

import (
	"context"

	"github.com/cplieger/auth/v4"
)

// AccountStore is the narrow interface consumed by the account handlers on
// Handler.Store: the session, user, and passkey-inspection surface the
// self-service account flows (login, logout, me, password change, OIDC
// link/unlink) reach for. It is wider than the three role interfaces below
// because those flows span all three domains; it is still only the twelve
// methods they call.
type AccountStore interface {
	CreateSession(ctx context.Context, sess *auth.Session) error
	GetSessionByHash(ctx context.Context, tokenHash string) (sess *auth.Session, found bool, err error)
	DeleteSession(ctx context.Context, tokenHash string) error
	CreateUser(ctx context.Context, user *auth.User) error
	GetUserByID(ctx context.Context, id int64) (user *auth.User, found bool, err error)
	GetUserByUsername(ctx context.Context, username string) (user *auth.User, found bool, err error)
	UpdateUser(ctx context.Context, user *auth.User) error
	ListUsers(ctx context.Context) ([]auth.User, error)
	UserCount(ctx context.Context) (int, error)
	GetPasskeysByUserID(ctx context.Context, userID int64) ([]auth.PasskeyCredential, error)
	PasskeyCountForUser(ctx context.Context, userID int64) (int, error)
	DeletePasskey(ctx context.Context, ref auth.PasskeyRef) error
	// UpdatePasskeyAfterLogin is the post-login credential-custody write the
	// library's CompleteLogin performs through this store.
	UpdatePasskeyAfterLogin(ctx context.Context, credID []byte, signCount uint32, flags auth.PasskeyFlags) error
}

// AuthAdminStore is the narrow interface consumed by admin user management handlers.
type AuthAdminStore interface {
	ListUsers(ctx context.Context) ([]auth.User, error)
	CreateUser(ctx context.Context, user *auth.User) error
	DeleteUser(ctx context.Context, id int64) error
}

// SecurityStore is the narrow interface consumed by security management handlers.
type SecurityStore interface {
	UpdateUser(ctx context.Context, user *auth.User) error
	DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error
	PasskeyCountForUser(ctx context.Context, userID int64) (int, error)
	GetPasskeysByUserID(ctx context.Context, userID int64) ([]auth.PasskeyCredential, error)
	CreatePasskey(ctx context.Context, cred *auth.PasskeyCredential) error
	DeletePasskey(ctx context.Context, ref auth.PasskeyRef) error
	RenamePasskey(ctx context.Context, ref auth.PasskeyRef, name string) error
	CreateAPIKey(ctx context.Context, key *auth.Key) error
	DeleteAPIKey(ctx context.Context, ref auth.KeyRef) error
	ListAPIKeysByUserID(ctx context.Context, userID int64) ([]auth.Key, error)
}

// OIDCStore is the narrow interface consumed by OIDC authentication handlers:
// the login/callback leg (state custody plus the identity lookups that resolve
// or create the user). The link/unlink leg needs the wider account surface
// (GetUserByID, ListUsers, passkey inspection) and goes through Handler.Store.
type OIDCStore interface {
	CreateOIDCState(ctx context.Context, state auth.OIDCState, nonce auth.OIDCNonce, codeVerifier auth.OIDCCodeVerifier, redirectURI string) error
	ConsumeOIDCState(ctx context.Context, state auth.OIDCState) (nonce auth.OIDCNonce, codeVerifier auth.OIDCCodeVerifier, redirectURI string, err error)
	GetUserByOIDCSub(ctx context.Context, issuer, sub string) (user *auth.User, found bool, err error)
	GetUserByUsername(ctx context.Context, username string) (user *auth.User, found bool, err error)
	CreateUser(ctx context.Context, user *auth.User) error
}
