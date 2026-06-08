package auth

import (
	"context"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
)

// (AuthStore composite moved to internal/authstore/ to break a test-time
// import cycle: auth/_test → store/ → store/authdb/ → would-be auth/.)

// SessionStore is the narrow interface consumed by [Authenticator].
// It declares only the store methods needed for session and API-key
// authentication, enabling focused testing with minimal fakes.
// The concrete store.DB satisfies this interface via structural typing.
type SessionStore interface {
	GetSessionByHash(ctx context.Context, tokenHash string) (*api.Session, error)
	GetUserByID(ctx context.Context, id int64) (*api.User, error)
	GetAPIKeyByHash(ctx context.Context, hash string) (*api.Key, error)
}

// Compile-time assertion: authstore.AuthStore satisfies SessionStore.
var _ SessionStore = authstore.AuthStore(nil)
