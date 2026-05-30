package authhandlers

import (
	"context"

	"subflux/internal/api"
	"subflux/internal/authstore"
)

// AuthAdminStore is the narrow interface consumed by admin user management handlers.
type AuthAdminStore interface {
	ListUsers(ctx context.Context) ([]api.User, error)
	CreateUser(ctx context.Context, user *api.User) error
	DeleteUser(ctx context.Context, id int64) error
}

// Compile-time assertion: the full AuthStore satisfies AuthAdminStore.
var _ AuthAdminStore = authstore.AuthStore(nil)

// SecurityStore is the narrow interface consumed by security management handlers.
type SecurityStore interface {
	UpdateUser(ctx context.Context, user *api.User) error
	DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error
	PasskeyCountForUser(ctx context.Context, userID int64) (int, error)
	GetPasskeysByUserID(ctx context.Context, userID int64) ([]api.PasskeyCredential, error)
	CreatePasskey(ctx context.Context, cred *api.PasskeyCredential) error
	DeletePasskey(ctx context.Context, id, userID int64) error
	RenamePasskey(ctx context.Context, id, userID int64, name string) error
	CreateAPIKey(ctx context.Context, key *api.Key) error
	DeleteAPIKey(ctx context.Context, id, userID int64) error
	ListAPIKeysByUserID(ctx context.Context, userID int64) ([]api.Key, error)
}

// Compile-time assertion: the full AuthStore satisfies SecurityStore.
var _ SecurityStore = authstore.AuthStore(nil)

// OIDCStore is the narrow interface consumed by OIDC authentication handlers.
type OIDCStore interface {
	CreateOIDCState(ctx context.Context, state, nonce, codeVerifier, redirectURI string) error
	ConsumeOIDCState(ctx context.Context, state string) (nonce, codeVerifier, redirectURI string, err error)
	GetUserByOIDCSub(ctx context.Context, issuer, sub string) (*api.User, error)
	GetUserByEmail(ctx context.Context, email string) (*api.User, error)
	GetUserByUsername(ctx context.Context, username string) (*api.User, error)
	CreateUser(ctx context.Context, user *api.User) error
	UpdateUser(ctx context.Context, user *api.User) error
}

// Compile-time assertion: the full AuthStore satisfies OIDCStore.
var _ OIDCStore = authstore.AuthStore(nil)

// AuthHandlerStore is the narrow interface consumed by login, registration,
// and session creation flows. Documented for discoverability;
// the Handler struct uses the full authstore.AuthStore directly.
type AuthHandlerStore interface {
	GetUserByUsername(ctx context.Context, username string) (*api.User, error)
	GetUserByID(ctx context.Context, id int64) (*api.User, error)
	UpdateUser(ctx context.Context, user *api.User) error
	UserCount(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, user *api.User) error
	CreateSession(ctx context.Context, sess *api.Session) error
	DeleteSession(ctx context.Context, tokenHash string) error
	PasskeyCountForUser(ctx context.Context, userID int64) (int, error)
}

// Compile-time assertion: authstore.AuthStore satisfies AuthHandlerStore.
var _ AuthHandlerStore = authstore.AuthStore(nil)
