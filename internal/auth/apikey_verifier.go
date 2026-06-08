package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/cplieger/subflux/internal/api"
)

// APIKeyVerifier authenticates requests via X-API-Key header or api_key query param.
type APIKeyVerifier struct {
	Store SessionStore
}

// Verify checks the API key header and query param, returns the user if valid.
func (v *APIKeyVerifier) Verify(ctx context.Context, r *http.Request) (*api.User, string, error) {
	key := r.Header.Get(api.HeaderXAPIKey)
	if key == "" {
		key = r.URL.Query().Get(api.QueryParamAPIKey)
	}
	if key == "" {
		return nil, "", nil
	}
	apiKey, err := VerifyAPIKey(ctx, v.Store, key)
	if err != nil {
		if errors.Is(err, ErrInvalidAPIKey) {
			return nil, "", ErrUnauthenticated
		}
		return nil, "", err
	}
	user, err := v.Store.GetUserByID(ctx, apiKey.UserID)
	if err != nil {
		return nil, "", err
	}
	if user == nil || !user.Enabled {
		return nil, "", ErrUnauthenticated
	}
	return user, "", nil
}
