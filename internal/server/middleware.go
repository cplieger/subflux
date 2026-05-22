package server

import (
	"net/http"
	"sync"
	"time"

	"subflux/internal/api"
	"subflux/internal/auth"
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
	RequireAuth(w http.ResponseWriter, r *http.Request) (*api.User, string, bool)
	Authenticate(r *http.Request) (*api.User, string, error)
}

// sessionActivityDebouncer tracks per-session last-update times to avoid
// writing to the DB on every single authenticated request.
type sessionActivityDebouncer struct {
	lastSeen map[string]time.Time
	mu       sync.Mutex
}

// sessionDebounceInterval is the minimum time between session activity
// updates for the same session. Reduces DB writes under high request rates.
const sessionDebounceInterval = 60 * time.Second

func newSessionActivityDebouncer() *sessionActivityDebouncer {
	return &sessionActivityDebouncer{lastSeen: make(map[string]time.Time)}
}

// shouldUpdate returns true if enough time has passed since the last update
// for this session hash, and records the current time.
func (d *sessionActivityDebouncer) shouldUpdate(hash string, now time.Time) bool {
	if d == nil {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if last, ok := d.lastSeen[hash]; ok && now.Sub(last) < sessionDebounceInterval {
		return false
	}
	d.lastSeen[hash] = now
	return true
}

// prune removes entries older than 2× the debounce interval to prevent
// unbounded map growth from accumulated expired session hashes.
func (d *sessionActivityDebouncer) prune(now time.Time) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cutoff := now.Add(-2 * sessionDebounceInterval)
	for k, t := range d.lastSeen {
		if t.Before(cutoff) {
			delete(d.lastSeen, k)
		}
	}
}

// requireConfigured returns 503 if the server has no valid config yet.
func (s *Server) requireConfigured(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.configured.Load() {
			api.ServiceUnavailableC(w, r, api.CodeServiceUnavailable, "not configured; save a valid configuration first")
			return
		}
		next(w, r)
	}
}

// requireAuth authenticates the request, injects the resolved user into
// the request context, and bumps session activity. On failure, it writes
// a 401 JSON response (API clients) or a 302 to /login (browsers) and
// does not call next. Auth bypass is handled inside Authenticator.Authenticate.
//
// Handlers downstream read the user with api.UserFromContext and the
// session hash with api.SessionHashFromContext. The latter is empty for
// API-key-authenticated requests.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, sessHash, ok := s.authenticator.RequireAuth(w, r)
		if !ok {
			return
		}
		if sessHash != "" {
			now := time.Now()
			if s.sessDebounce.shouldUpdate(sessHash, now) {
				if s.sessBatcher != nil {
					s.sessBatcher.Send(sessHash, now)
				}
			}
		}
		ctx := api.NewUserContext(r.Context(), user)
		ctx = api.NewSessionHashContext(ctx, sessHash)
		next(w, r.WithContext(ctx))
	}
}

// requireRole returns a middleware that authorizes requests based on the
// user's role. Must be chained after requireAuth so UserFromContext is
// populated. Admin is a superset of user (see auth.HasRole).
func (s *Server) requireRole(role api.Role) middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			user := api.UserFromContext(r.Context())
			if !auth.HasRole(user, role) {
				api.ForbiddenC(w, r, api.CodeAuthRoleRequired, "forbidden")
				return
			}
			next(w, r)
		}
	}
}

// requireRecentReauth gates the handler behind a fresh session reauth
// (within auth.CeremonyTimeout). API-key-authenticated requests bypass this
// check because API keys don't participate in the reauth flow. Must
// chain after requireAuth so the session hash is in the context.
func (s *Server) requireRecentReauth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessHash := api.SessionHashFromContext(r.Context())
		// API key callers: no session to refresh; accept.
		if sessHash == "" {
			next(w, r)
			return
		}
		sess, err := s.authStore.GetSessionByHash(r.Context(), sessHash)
		if err != nil || sess == nil ||
			sess.ReauthAt == nil || time.Since(*sess.ReauthAt) > auth.CeremonyTimeout {
			api.WriteJSONStatus(w, http.StatusForbidden, map[string]any{
				"error":           "reauthentication required",
				"reauth_required": true,
			})
			return
		}
		next(w, r)
	}
}
