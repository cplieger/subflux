package auth

import (
	"context"
	"net/http"

	"github.com/cplieger/subflux/internal/api"
)

// CredentialVerifier resolves an HTTP request to an authenticated user
// using a specific credential type (session, API key, passkey).
type CredentialVerifier interface {
	// Verify attempts to authenticate the request. Returns the user and
	// session hash on success, or a nil user if this verifier cannot
	// authenticate the request (credential not present). Returns an error
	// only on internal failures (DB errors, etc.).
	Verify(ctx context.Context, r *http.Request) (*api.User, string, error)
}
