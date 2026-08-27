package server

import (
	"net/http"

	"github.com/cplieger/auth/v5"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/subflux"
)

// --- Request middleware ---
//
// Every middleware has signature `func(http.HandlerFunc) http.HandlerFunc`
// so they compose via routeGroup chains in routes.go. This is the complete
// set; there is no per-handler auth check elsewhere in the package.

// sessionAuthenticator is the narrow interface that middleware needs from
// the auth layer. Decouples middleware from the concrete *auth.Authenticator
// type, enabling test doubles without constructing a full authenticator.
type sessionAuthenticator interface {
	RequireAuth(w http.ResponseWriter, r *http.Request) (*auth.User, string, bool)
	Authenticate(r *http.Request) (*auth.User, string, error)
}

// requireConfigured returns 503 if the server has no valid config yet.
func (s *Server) requireConfigured(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.configured.Load() {
			httpapi.ServiceUnavailableC(w, r, subflux.CodeServiceUnavailable, "not configured; save a valid configuration first")
			return
		}
		next(w, r)
	}
}

// requireAuth authenticates the request and injects the resolved user into
// the request context. On failure the authenticator writes subflux's
// unauthorized response (401 JSON for API clients, 302 to /login for
// browsers — see authhandlers.UnauthorizedResponse) and next is not called.
// Auth bypass is handled inside Authenticator.Authenticate; session-activity
// writes happen inside the library's session verifier, throttled per session.
//
// Handlers downstream read the user with authhandlers.UserFromContext and the
// session hash with authhandlers.SessionHashFromContext. The latter is empty for
// API-key-authenticated requests.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, sessHash, ok := s.authenticator.RequireAuth(w, r)
		if !ok {
			return
		}
		ctx := authhandlers.NewUserContext(r.Context(), user)
		ctx = authhandlers.NewSessionHashContext(ctx, sessHash)
		next(w, r.WithContext(ctx))
	}
}

// requireRole returns a middleware that authorizes requests based on the
// user's role. Must be chained after requireAuth so UserFromContext is
// populated. Admin is a superset of user (see auth.HasRole).
func requireRole(role auth.Role) middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			user := authhandlers.UserFromContext(r.Context())
			if !auth.HasRole(user, role) {
				httpapi.ForbiddenC(w, r, subflux.CodeAuthRoleRequired, "forbidden")
				return
			}
			next(w, r)
		}
	}
}

// requireRecentReauth removed: reauthentication step-up was dropped in favor
// of client-side confirmation on destructive actions (session cookie flags are
// the compensating control).
