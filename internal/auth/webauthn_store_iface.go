package auth

import (
	"context"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
)

// WebAuthnStore is the narrow interface consumed by WebAuthn/passkey
// handlers. It declares only the 3 store methods needed for passkey
// authentication, enabling focused testing with minimal fakes.
type WebAuthnStore interface {
	GetPasskeysByUserID(ctx context.Context, userID int64) ([]api.PasskeyCredential, error)
	UpdatePasskeyAfterLogin(ctx context.Context, credID []byte, signCount uint32, flags api.PasskeyFlags) error
	GetUserByID(ctx context.Context, id int64) (*api.User, error)
}

// Compile-time assertion: authstore.AuthStore satisfies WebAuthnStore.
var _ WebAuthnStore = authstore.AuthStore(nil)
