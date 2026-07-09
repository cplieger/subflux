package authhandlers

import (
	"context"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/authstore"
)

// AuthAdminStore is the narrow interface consumed by admin user management handlers.
type AuthAdminStore interface {
	ListUsers(ctx context.Context) ([]auth.User, error)
	CreateUser(ctx context.Context, user *auth.User) error
	DeleteUser(ctx context.Context, id int64) error
}

// Compile-time assertion: the full AuthStore satisfies AuthAdminStore.
var _ AuthAdminStore = authstore.AuthStore(nil)

// SecurityStore is the narrow interface consumed by security management handlers.
type SecurityStore interface {
	UpdateUser(ctx context.Context, user *auth.User) error
	DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error
	PasskeyCountForUser(ctx context.Context, userID int64) (int, error)
	GetPasskeysByUserID(ctx context.Context, userID int64) ([]auth.PasskeyCredential, error)
	CreatePasskey(ctx context.Context, cred *auth.PasskeyCredential) error
	DeletePasskey(ctx context.Context, id, userID int64) error
	RenamePasskey(ctx context.Context, id, userID int64, name string) error
	CreateAPIKey(ctx context.Context, key *auth.Key) error
	DeleteAPIKey(ctx context.Context, id, userID int64) error
	ListAPIKeysByUserID(ctx context.Context, userID int64) ([]auth.Key, error)
}

// Compile-time assertion: the full AuthStore satisfies SecurityStore.
var _ SecurityStore = authstore.AuthStore(nil)

// OIDCStore is the narrow interface consumed by OIDC authentication handlers.
type OIDCStore interface {
	CreateOIDCState(ctx context.Context, state, nonce, codeVerifier, redirectURI string) error
	ConsumeOIDCState(ctx context.Context, state string) (nonce, codeVerifier, redirectURI string, err error)
	GetUserByOIDCSub(ctx context.Context, issuer, sub string) (*auth.User, error)
	GetUserByEmail(ctx context.Context, email string) (*auth.User, error)
	GetUserByUsername(ctx context.Context, username string) (*auth.User, error)
	CreateUser(ctx context.Context, user *auth.User) error
	UpdateUser(ctx context.Context, user *auth.User) error
}

// Compile-time assertion: the full AuthStore satisfies OIDCStore.
var _ OIDCStore = authstore.AuthStore(nil)
