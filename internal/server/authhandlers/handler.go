package authhandlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/auth/v5"
	authoidc "github.com/cplieger/auth/v5/oidc"
	"github.com/cplieger/auth/v5/ratelimit"
	authwebauthn "github.com/cplieger/auth/v5/webauthn"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/webhttp/v2"
)

// AuthConfig is the authentication half of the configuration, and the only
// part these handlers read: whether password login is on at all, whether a new
// password must be checked against the breach corpus, and whether OIDC is
// available as an alternative factor. 3 of the 37 values the config offers.
//
// Exported because the composition root names it: the Config resolver below is
// how the handlers see a hot-reloaded config, and the root has to write that
// function's type.
type AuthConfig interface {
	BasicAuthEnabled() bool
	CheckBreachedPasswords() bool
	OIDCEnabled() bool
}

// Handler holds all dependencies for the auth handler family.
// Constructed by the server package and stored on the Server struct.
type Handler struct {
	Store       AccountStore
	AdminDB     AuthAdminStore
	SecDB       SecurityStore
	OidcDB      OIDCStore
	RateLimiter ratelimit.Checker
	// WebAuthnResolver resolves the current relying party from the live
	// snapshot per request (may resolve nil: RP ID unset or construction
	// degraded). A direct field would freeze the boot-time instance across
	// hot config edits; the resolver is the same seam OIDCResolver and
	// Config already use.
	WebAuthnResolver func() *authwebauthn.RelyingParty
	OIDCResolver     func() *authoidc.Provider
	Ceremonies       *CeremonyStore
	Config           func() AuthConfig // returns current config (hot-reloadable)
	Configured       func() bool       // returns whether server has valid config
	HTTPClient       *http.Client      // shared client for outbound requests (HIBP, etc.)
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

	// maxDisplayNameLen is the maximum length for a user's display name. It
	// reaches the user's password manager as the passkey entry's label, so it
	// is bounded like every other name a user chooses.
	maxDisplayNameLen = 128

	// maxAPIKeyLabelLen is the maximum length for API key labels.
	maxAPIKeyLabelLen = 128
)

// --- Helpers ---

// decodeAuthBody decodes the JSON request body into T under a size cap, via
// webhttp.DecodeJSONInto (http.MaxBytesReader cap + single-value decode +
// trailing-data rejection): a body exceeding maxAuthBodySize (a tight 4 KB for
// auth endpoints), malformed JSON, or trailing data fails the decode (yielding
// the 400 below). Returns the decoded value and true on success, or writes a
// 400 response and returns the zero value and false on failure.
func decodeAuthBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := webhttp.DecodeJSONInto(w, r, &v, maxAuthBodySize); err != nil {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid request body")
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
	user *auth.User, authMethod auth.Method, oidcExpiry time.Time,
) error {
	token, hash := auth.GenerateSessionToken()
	now := time.Now()
	sess := &auth.Session{
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
	SessionCookie.SetCookie(w, r, token, 0)
	slog.Info("login successful", "username", user.Username, "method", authMethod, "ip", sess.IPAddress)
	return nil
}

// respondLoginSuccess writes the standard login JSON response.
func (h *Handler) respondLoginSuccess(w http.ResponseWriter, r *http.Request, user *auth.User) {
	passkeyCount, err := h.Store.PasskeyCountForUser(r.Context(), user.ID)
	if err != nil {
		slog.Warn("login response: passkey count", "error", err)
	}
	httpapi.WriteJSON(w, subflux.LoginSuccess{
		Redirect: "/",
		User: subflux.MeResponse{
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
func (h *Handler) createSessionAndRespond(w http.ResponseWriter, r *http.Request, user *auth.User, authMethod auth.Method) error {
	// The zero Time: a password or passkey login has no OIDC token behind it.
	if err := h.createAndSetSession(w, r, user, authMethod, time.Time{}); err != nil {
		return err
	}
	h.respondLoginSuccess(w, r, user)
	return nil
}

// PasswordCheck is one password to validate, with the two policy choices that
// change what "valid" means.
//
// All four are named rather than positional because both pairs are silently
// transposable: the two strings are free-form user input, and swapping them
// validates the username as a password and admits any password containing the
// real one; the two booleans are independent policy switches, and a reversed
// CheckBreach disables the breach lookup on a path the operator believes is
// checked while shortening the length floor on one that is not.
type PasswordCheck struct {
	// Password is the candidate password.
	Password string
	// Username is the account it belongs to, rejected as a substring.
	Username string
	// SoleFactor is true when the password is the account's only
	// authentication factor, which raises the length floor.
	SoleFactor bool
	// CheckBreach is true when the password must also be looked up in the
	// breach corpus.
	CheckBreach bool
}

// PasswordHash is an Argon2id password hash. It is a distinct type so it
// cannot be transposed with ValidateAndHashPassword's other string result, the
// user-facing rejection message every call site writes into a 400 body: as two
// plain strings a swapped assignment compiles and publishes the hash to the
// client. Convert to string only at the auth.User boundary.
type PasswordHash string

// ValidateAndHashPassword validates password length and context (rejecting
// passwords that contain the username or app name), checks against breach
// databases (when check.CheckBreach is set), and returns the Argon2id hash.
// A non-empty userMsg means the password was rejected on policy grounds and is
// safe to show the caller; hash is empty in that case.
func ValidateAndHashPassword(ctx context.Context, check PasswordCheck, client *http.Client) (hash PasswordHash, userMsg string, err error) {
	validateLen := auth.ValidateMultiFactorPasswordLength
	if check.SoleFactor {
		validateLen = auth.ValidateSoloPasswordLength
	}
	if errLen := validateLen(check.Password); errLen != nil {
		return "", errLen.Error(), nil
	}
	pctx := auth.PasswordContext{Username: check.Username, ForbiddenWords: []string{"subflux"}}
	if errCtx := auth.ValidatePasswordContext(check.Password, pctx); errCtx != nil {
		return "", errCtx.Error(), nil
	}
	if check.CheckBreach {
		breached, errBreach := auth.CheckBreachedPassword(ctx, client, check.Password)
		if errBreach != nil {
			slog.Warn("breached password check error", "error", errBreach)
		}
		if breached {
			return "", msgBreachedPassword, nil
		}
	}
	return PasswordHash(auth.HashPassword(check.Password)), "", nil
}

// requireWebAuthn resolves the current relying party from the live snapshot,
// writing a 400 error and returning ok=false when WebAuthn is not configured
// (no RP ID, cold-boot degrade, or no resolver wired in tests).
func (h *Handler) requireWebAuthn(w http.ResponseWriter) (*authwebauthn.RelyingParty, bool) {
	var rp *authwebauthn.RelyingParty
	if h.WebAuthnResolver != nil {
		rp = h.WebAuthnResolver()
	}
	if rp == nil {
		httpapi.BadRequestC(w, nil, subflux.CodeBadRequest, "WebAuthn not configured")
		return nil, false
	}
	return rp, true
}

// consumeWebAuthnSession reads the session token from the request header,
// consumes the ceremony it names from the ceremony store, and writes an error
// response on failure.
func (h *Handler) consumeWebAuthnSession(w http.ResponseWriter, r *http.Request) (authwebauthn.Ceremony, bool) {
	sessionToken := r.Header.Get(HeaderWebAuthnSession)
	if sessionToken == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "missing session token")
		return authwebauthn.Ceremony{}, false
	}
	ceremony, found := h.Ceremonies.ConsumeWebAuthnSession(sessionToken)
	if !found {
		httpapi.UnauthorizedC(w, r, subflux.CodeWebAuthnSessionInvalid, "invalid or expired session")
		return authwebauthn.Ceremony{}, false
	}
	return ceremony, true
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
		httpapi.BadRequestC(w, nil, subflux.CodeBadRequest, "missing "+label)
		return 0, false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		httpapi.BadRequestC(w, nil, subflux.CodeBadRequest, "invalid "+label)
		return 0, false
	}
	return id, true
}
