package authhandlers

import (
	"context"

	"github.com/cplieger/auth/v5"
)

// The request-scoped authentication values, and the only place they are put
// into or read out of a context. They live in this package rather than in the
// types package because they are authenticator glue, like the session cookie
// config and the unauthorized-response policy beside them: the middleware in
// internal/server writes both, and fourteen of the seventeen production reads
// are handlers here. internal/server can import this package; the reverse would
// be a cycle, which is what rules out the other direction.
//
// Context keys. Each uses a distinct private type so keys from different
// categories cannot collide, and external packages cannot fabricate them.
type (
	userContextKeyT     struct{}
	sessHashContextKeyT struct{}
)

var (
	userContextKey     = userContextKeyT{}
	sessHashContextKey = sessHashContextKeyT{}
)

// NewUserContext returns a new context with the given user stored in it.
func NewUserContext(ctx context.Context, u *auth.User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// UserFromContext extracts the authenticated user from the request context.
// Returns nil if no user is present.
func UserFromContext(ctx context.Context) *auth.User {
	u, ok := ctx.Value(userContextKey).(*auth.User)
	if !ok {
		return nil
	}
	return u
}

// NewSessionHashContext returns a new context carrying the session token hash
// for the current request. Only requireAuth populates this; API-key callers
// have an empty session hash.
func NewSessionHashContext(ctx context.Context, sessHash string) context.Context {
	return context.WithValue(ctx, sessHashContextKey, sessHash)
}

// SessionHashFromContext returns the session token hash for the current
// request, or "" if the request was authenticated via API key (no session).
// Handlers that need to touch the current session (delete on logout,
// exclude from bulk session invalidation) read it here instead
// of re-parsing the cookie.
func SessionHashFromContext(ctx context.Context) string {
	h, ok := ctx.Value(sessHashContextKey).(string)
	if !ok {
		return ""
	}
	return h
}
