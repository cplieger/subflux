package authhandlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/auth"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/ratelimit"
	"github.com/go-webauthn/webauthn/webauthn"
)

// AuthConfig is the narrow interface consumed by auth handlers for
// configuration access. Mirrors the auth-related subset of api.ConfigProvider.
type AuthConfig interface {
	BasicAuthEnabled() bool
	CheckBreachedPasswords() bool
	OIDCEnabled() bool
}

// Handler holds all dependencies for the auth handler family.
// Constructed by the server package and stored on the Server struct.
type Handler struct {
	Store        authstore.AuthStore
	AdminDB      AuthAdminStore
	SecDB        SecurityStore
	OidcDB       OIDCStore
	RateLimiter  ratelimit.Checker
	WebAuthn     *webauthn.WebAuthn // may be nil
	OIDCResolver func() *auth.OIDCProvider
	Ceremonies   *CeremonyStore
	Config       func() AuthConfig // returns current config (hot-reloadable)
	Configured   func() bool       // returns whether server has valid config
	HTTPClient   *http.Client      // shared client for outbound requests (HIBP, etc.)
	// migrateMu serializes OIDC link-migrations so the last-local-admin check
	// and the password clear are atomic within this (single-binary) process.
	migrateMu sync.Mutex
}

const (
	// authDBTimeout is the context timeout applied to auth handler DB operations.
	authDBTimeout = 5 * time.Second

	// msgBreachedPassword is the user-facing error when a password appears in a breach database.
	msgBreachedPassword = "this password has appeared in a data breach; please choose a different one"

	// maxAuthBodySize limits request body size for auth endpoints (4 KB).
	maxAuthBodySize = 4096

	// maxUsernameLen is the maximum length for usernames.
	maxUsernameLen = 64

	// maxPasskeyNameLen is the maximum length for passkey names.
	maxPasskeyNameLen = 128

	// maxAPIKeyLabelLen is the maximum length for API key labels.
	maxAPIKeyLabelLen = 128
)

// --- Helpers ---

// decodeAuthBody decodes the JSON request body into T with a size cap.
// Returns the decoded value and true on success, or writes a 400 response
// and returns zero value and false on failure.
func decodeAuthBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAuthBodySize)).Decode(&v); err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid request body")
		return v, false
	}
	return v, true
}

// dbCtx returns a context with authDBTimeout applied, suitable for all
// auth handler DB operations. The caller must call the returned cancel func.
func dbCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, authDBTimeout)
}

// createAndSetSession persists a session for the authenticated user and sets
// the session cookie on the response.
func (h *Handler) createAndSetSession(w http.ResponseWriter, r *http.Request,
	user *api.User, authMethod api.AuthMethod, oidcExpiry *time.Time,
) error {
	token, hash, err := auth.GenerateSessionToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	now := time.Now()
	sess := &api.Session{
		TokenHash:    hash,
		UserID:       user.ID,
		AuthMethod:   authMethod,
		IPAddress:    ClientIP(r),
		CreatedAt:    now,
		LastActivity: now,
		OIDCExpiry:   oidcExpiry,
	}
	if err := h.Store.CreateSession(r.Context(), sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	auth.SetSessionCookie(w, r, token, 0)
	slog.Info("login successful", "username", user.Username, "method", authMethod, "ip", sess.IPAddress)
	return nil
}

// respondLoginSuccess writes the standard login JSON response.
func (h *Handler) respondLoginSuccess(w http.ResponseWriter, r *http.Request, user *api.User) {
	passkeyCount, err := h.Store.PasskeyCountForUser(r.Context(), user.ID)
	if err != nil {
		slog.Warn("login response: passkey count", "error", err)
	}
	api.WriteJSON(w, api.LoginSuccessResponse{
		Redirect: "/",
		User: api.UserMeResponse{
			ID:          user.ID,
			Username:    user.Username,
			Role:        user.Role,
			HasPasskeys: passkeyCount > 0,
			OIDCLinked:  user.OIDCSub != "",
			HasPassword: user.PasswordHash != "",
		},
	})
}

// createSessionAndRespond is the standard login completion: create session,
// set cookie, write JSON.
func (h *Handler) createSessionAndRespond(w http.ResponseWriter, r *http.Request, user *api.User, authMethod api.AuthMethod) error {
	if err := h.createAndSetSession(w, r, user, authMethod, nil); err != nil {
		return err
	}
	h.respondLoginSuccess(w, r, user)
	return nil
}

// ValidateAndHashPassword validates password length and context (rejecting
// passwords that contain the username or app name), checks against breach
// databases (if checkBreach is true), and returns the Argon2id hash.
func ValidateAndHashPassword(ctx context.Context, password, username string, passwordOnly, checkBreach bool, client *http.Client) (hash, userMsg string, err error) {
	if errLen := auth.ValidatePasswordLength(password, passwordOnly); errLen != nil {
		return "", errLen.Error(), nil
	}
	if errCtx := auth.ValidatePasswordContext(password, username); errCtx != nil {
		return "", errCtx.Error(), nil
	}
	if checkBreach {
		breached, errBreach := auth.CheckBreachedPassword(ctx, client, password)
		if errBreach != nil {
			slog.Warn("breached password check error", "error", errBreach)
		}
		if breached {
			return "", msgBreachedPassword, nil
		}
	}
	h, err := auth.HashPassword(password)
	if err != nil {
		return "", "", err
	}
	return h, "", nil
}

// requireWebAuthn checks that WebAuthn is configured and writes a 400 error if not.
func (h *Handler) requireWebAuthn(w http.ResponseWriter) bool {
	if h.WebAuthn == nil {
		api.BadRequestC(w, nil, api.CodeBadRequest, "WebAuthn not configured")
		return false
	}
	return true
}

// consumeWebAuthnSession reads the session token from the request header,
// consumes it from the ceremony store, and writes an error response on failure.
func (h *Handler) consumeWebAuthnSession(w http.ResponseWriter, r *http.Request) *webauthn.SessionData {
	sessionToken := r.Header.Get(HeaderWebAuthnSession)
	if sessionToken == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "missing session token")
		return nil
	}
	sessData := h.Ceremonies.ConsumeWebAuthnSession(sessionToken)
	if sessData == nil {
		api.UnauthorizedC(w, r, api.CodeWebAuthnSessionInvalid, "invalid or expired session")
		return nil
	}
	return sessData
}

// extractPathSegment extracts a path segment between prefix and suffix.
func extractPathSegment(path, prefix, suffix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	if suffix != "" {
		idx := strings.Index(rest, suffix)
		if idx < 0 {
			return ""
		}
		rest = rest[:idx]
	}
	return rest
}

// parseIDFromPath extracts and validates a numeric ID from a URL path segment.
func parseIDFromPath(w http.ResponseWriter, path, prefix, label string) (int64, bool) {
	idStr := extractPathSegment(path, prefix, "")
	if idStr == "" {
		api.BadRequestC(w, nil, api.CodeBadRequest, "missing "+label)
		return 0, false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		api.BadRequestC(w, nil, api.CodeBadRequest, "invalid "+label)
		return 0, false
	}
	return id, true
}
