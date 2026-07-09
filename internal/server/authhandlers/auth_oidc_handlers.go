package authhandlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/auth/v2"
	authoidc "github.com/cplieger/auth/v2/oidc"
	"github.com/cplieger/subflux/internal/api"
)

// --- GET /api/auth/oidc ---

// HandleOIDCRedirect handles GET /api/auth/oidc — initiates the OIDC authorization
// code flow by generating state, nonce, and PKCE and redirecting to the provider.
func (h *Handler) HandleOIDCRedirect(w http.ResponseWriter, r *http.Request) {
	oidcProv := h.OIDCResolver()
	if oidcProv == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "OIDC not configured")
		return
	}

	state, err := authoidc.GenerateState()
	if err != nil {
		slog.Error("oidc: generate state", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	nonce, err := authoidc.GenerateState()
	if err != nil {
		slog.Error("oidc: generate nonce", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	verifier, challenge, err := authoidc.GeneratePKCE()
	if err != nil {
		slog.Error("oidc: generate PKCE", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	redirectURI := auth.ValidateRedirectURI(r.URL.Query().Get("redirect"))

	ctx := r.Context()
	if err := h.OidcDB.CreateOIDCState(ctx, state, nonce, verifier, redirectURI); err != nil {
		slog.Error("oidc: store state", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	authURL := oidcProv.AuthorizationURL(state, nonce, challenge)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// --- GET /api/auth/oidc/callback ---

// HandleOIDCCallback handles GET /api/auth/oidc/callback — completes the OIDC
// authorization code exchange, resolves or JIT-provisions the user, and creates
// a session. Redirects to /login.html when a link ceremony is required.
func (h *Handler) HandleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	oidcProv := h.OIDCResolver()
	if oidcProv == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "OIDC not configured")
		return
	}

	q := r.URL.Query()
	stateParam := q.Get("state")
	code := q.Get("code")

	if errParam := q.Get("error"); errParam != "" {
		desc := q.Get("error_description")
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, "",
			slog.String("reason", "provider_error"),
			slog.String("error", errParam),
			slog.String("description", desc))
		api.UnauthorizedC(w, r, api.CodeOIDCExchangeFailed, "authentication failed")
		return
	}

	if stateParam == "" || code == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "missing state or code")
		return
	}

	ctx := r.Context()

	nonce, codeVerifier, redirectURI, err := h.OidcDB.ConsumeOIDCState(ctx, stateParam)
	if err != nil {
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, "",
			slog.String("reason", "invalid_state"))
		api.UnauthorizedC(w, r, api.CodeOIDCStateInvalid, "invalid or expired state")
		return
	}

	claims, oidcExpiry, err := oidcProv.Exchange(ctx, code, codeVerifier, nonce)
	if err != nil {
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, "",
			slog.String("reason", "exchange_failed"))
		api.UnauthorizedC(w, r, api.CodeOIDCExchangeFailed, "authentication failed")
		return
	}

	user, linkToken, err := h.resolveOrLinkOIDC(ctx, claims)
	if err != nil {
		if errors.Is(err, errOIDCLinkNoPassword) {
			Audit(r, slog.LevelWarn, AuditOIDCCallback, false, "",
				slog.String("reason", "username_conflict_no_password"))
			api.ConflictC(w, r, api.CodeConflict, "an account with this username already exists")
			return
		}
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "oidc resolve user")
		return
	}
	if linkToken != "" {
		// Username collides with a password-protected local account. Redirect
		// to the login page to prove ownership and link (link-on-login).
		Audit(r, slog.LevelInfo, AuditOIDCCallback, true, "",
			slog.String("stage", "link_required"))
		//nolint:gosec // G710: linkToken is a server-generated hex token, not user input
		http.Redirect(w, r, "/login.html?oidc_link="+linkToken, http.StatusFound)
		return
	}

	if !user.Enabled {
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, user.Username,
			slog.String("reason", "account_disabled"))
		api.ForbiddenC(w, r, api.CodeAuthAccountDisabled, "account disabled")
		return
	}

	if err := h.createAndSetSession(w, r, user, auth.MethodOIDC, oidcExpiry); err != nil {
		slog.Error("oidc: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	redirectURI = auth.ValidateRedirectURI(redirectURI)
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(auth.MethodOIDC)))
	http.Redirect(w, r, redirectURI, http.StatusFound) //nolint:gosec // G710: redirectURI validated above
}

// errOIDCLinkNoPassword is returned when an OIDC login matches an existing
// local account by username but that account has no password to prove
// ownership with, so it cannot be safely linked.
var errOIDCLinkNoPassword = errors.New("oidc: username conflict with passwordless account")

// resolveOrLinkOIDC resolves an OIDC identity by (issuer, sub) only. Outcomes:
//   - (user, "", nil)       matched by sub, or a fresh JIT-provisioned user → log in
//   - (nil, linkToken, nil) username collides with a password account → caller
//     must complete link-on-login (the user proves the existing password)
//   - (nil, "", err)        lookup/create error, or errOIDCLinkNoPassword
//
// Email and username are never used to auto-link; that is an account-takeover
// vector. A username collision triggers an explicit, password-proven link.
func (h *Handler) resolveOrLinkOIDC(ctx context.Context, claims *authoidc.Claims) (user *auth.User, linkToken string, err error) {
	bySub, err := h.OidcDB.GetUserByOIDCSub(ctx, claims.Issuer, claims.Subject)
	if err != nil {
		return nil, "", fmt.Errorf("lookup by sub: %w", err)
	}
	if bySub != nil {
		return bySub, "", nil
	}

	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	byName, err := h.OidcDB.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, "", fmt.Errorf("lookup by username: %w", err)
	}
	if byName != nil {
		// Do NOT auto-link. Require the user to prove ownership of the
		// existing account with its password before linking the OIDC sub.
		if byName.PasswordHash == "" {
			return nil, "", errOIDCLinkNoPassword
		}
		token, gerr := GenerateCeremonyToken()
		if gerr != nil {
			return nil, "", gerr
		}
		h.Ceremonies.Link.Store(token, &PendingLink{
			UserID:     byName.ID,
			OIDCSub:    claims.Subject,
			OIDCIssuer: claims.Issuer,
			CreatedAt:  time.Now(),
		})
		return nil, token, nil
	}

	// No sub match and no username collision → JIT-provision a new user.
	newUser, _, err := authoidc.ResolveUser(claims, nil)
	if err != nil {
		return nil, "", fmt.Errorf("resolve oidc user: %w", err)
	}
	now := time.Now()
	newUser.CreatedAt = now
	newUser.UpdatedAt = now
	if err := h.OidcDB.CreateUser(ctx, newUser); err != nil {
		return nil, "", fmt.Errorf("create user: %w", err)
	}
	slog.Info("oidc: new user created", "username", newUser.Username, "sub", claims.Subject)
	return newUser, "", nil
}

// --- POST /api/auth/oidc/link ---

// HandleOIDCLink completes link-on-login: the user proves ownership of the
// existing local account by password, and the pending OIDC identity is linked
// to it. The link token is single-use and TTL-bounded.
func (h *Handler) HandleOIDCLink(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeAuthBody[struct {
		LinkToken string `json:"link_token"`
		Password  string `json:"password"`
	}](w, r)
	if !ok {
		return
	}

	pending, ok := h.Ceremonies.Link.LoadAndDelete(req.LinkToken)
	if !ok || time.Since(pending.CreatedAt) > CeremonyTTL {
		api.UnauthorizedC(w, r, api.CodeAuthSessionInvalid, "invalid or expired link token")
		return
	}

	ctx := r.Context()
	user, err := h.Store.GetUserByID(ctx, pending.UserID)
	if err != nil || user == nil {
		slog.Error("oidc link: user lookup", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	ip := ClientIP(r)
	allowed, retryAfter := h.RateLimiter.Allow(ip, user.Username)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		api.TooManyRequestsC(w, r, api.CodeRateLimited, "too many attempts")
		return
	}

	okPass, err := auth.VerifyPassword(req.Password, user.PasswordHash)
	if err != nil || !okPass {
		h.RateLimiter.Record(ip, user.Username)
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, user.Username,
			slog.String("reason", "link_password_invalid"))
		api.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "invalid credentials")
		return
	}
	h.RateLimiter.Reset(ip, user.Username)

	// Link-on-login is a MIGRATION to SSO governance: it removes the local
	// password and passkeys so the IdP becomes the sole control point. Never
	// strip the last local (password) admin — the break-glass account.
	// Serialize so the last-admin check and the clear are atomic in-process.
	h.migrateMu.Lock()
	defer h.migrateMu.Unlock()
	if last, lerr := h.isLastLocalAdmin(ctx, user); lerr != nil {
		slog.Error("oidc link: admin check", "error", lerr)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	} else if last {
		api.ConflictC(w, r, api.CodeConflict,
			"cannot switch the last local admin to SSO-only; keep a break-glass admin or reset via the CLI")
		return
	}

	user.OIDCSub = pending.OIDCSub
	user.OIDCIssuer = pending.OIDCIssuer
	user.PasswordHash = ""
	user.UpdatedAt = time.Now()
	if err := h.Store.UpdateUser(ctx, user); err != nil {
		slog.Error("oidc link: update user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	h.clearPasskeys(ctx, user.ID)

	if err := h.createSessionAndRespond(w, r, user, auth.MethodOIDC); err != nil {
		slog.Error("oidc link: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	slog.Info("oidc: account linked", "username", user.Username)
	Audit(r, slog.LevelInfo, AuditOIDCCallback, true, user.Username,
		slog.String("stage", "linked"))
}

// isLastLocalAdmin reports whether u is an admin and the only admin with a
// local password — i.e. migrating it to SSO-only would remove the break-glass
// account. Non-admins always return false.
func (h *Handler) isLastLocalAdmin(ctx context.Context, u *auth.User) (bool, error) {
	if u.Role != auth.RoleAdmin {
		return false, nil
	}
	users, err := h.Store.ListUsers(ctx)
	if err != nil {
		return false, err
	}
	localAdmins := 0
	for i := range users {
		if users[i].Role == auth.RoleAdmin && users[i].PasswordHash != "" {
			localAdmins++
		}
	}
	return localAdmins <= 1, nil
}

// clearPasskeys removes all of a user's passkeys (best-effort) so that an
// OIDC-migrated account retains no local login method.
func (h *Handler) clearPasskeys(ctx context.Context, userID int64) {
	passkeys, err := h.Store.GetPasskeysByUserID(ctx, userID)
	if err != nil {
		slog.Warn("oidc link: list passkeys", "error", err)
		return
	}
	for i := range passkeys {
		if derr := h.Store.DeletePasskey(ctx, passkeys[i].ID, userID); derr != nil {
			slog.Warn("oidc link: delete passkey", "error", derr)
		}
	}
}

// --- DELETE /api/auth/oidc/link ---

// HandleOIDCUnlink removes the OIDC binding from the current user's account.
// It refuses if doing so would leave the account with no way to log in (no
// password and no passkey), mirroring the disable-password lockout guard.
func (h *Handler) HandleOIDCUnlink(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())
	ctx := r.Context()

	if user.OIDCSub == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "no OIDC account linked")
		return
	}
	if user.PasswordHash == "" {
		passkeys, err := h.Store.PasskeyCountForUser(ctx, user.ID)
		if err != nil {
			slog.Error("oidc unlink: passkey count", "error", err)
			api.InternalErrorC(w, r, nil, api.CodeInternalError)
			return
		}
		if passkeys == 0 {
			api.ConflictC(w, r, api.CodeConflict, "cannot unlink: set a password or add a passkey first")
			return
		}
	}

	user.OIDCSub = ""
	user.OIDCIssuer = ""
	user.UpdatedAt = time.Now()
	if err := h.Store.UpdateUser(ctx, user); err != nil {
		slog.Error("oidc unlink: update user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	slog.Info("oidc: account unlinked", "username", user.Username)
	api.Ok(w)
}
