package api

import "context"

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
func NewUserContext(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// UserFromContext extracts the authenticated user from the request context.
// Returns nil if no user is present.
func UserFromContext(ctx context.Context) *User {
	u, ok := ctx.Value(userContextKey).(*User)
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
// Handlers that need to touch the current session (update reauth_at, delete
// on logout, exclude from bulk session invalidation) read it here instead
// of re-parsing the cookie.
func SessionHashFromContext(ctx context.Context) string {
	h, ok := ctx.Value(sessHashContextKey).(string)
	if !ok {
		return ""
	}
	return h
}
