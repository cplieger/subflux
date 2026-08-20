package authhandlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/auth/v4"
	"github.com/cplieger/auth/v4/ratelimit"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/subflux"
)

// --- POST /api/auth/login ---

// HandleLogin handles POST /api/auth/login — authenticates with username and password.
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if cfg := h.Config(); cfg != nil && !cfg.BasicAuthEnabled() {
		httpapi.ForbiddenC(w, r, subflux.CodeForbidden, "password login is disabled")
		return
	}

	req, ok := decodeAuthBody[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](w, r)
	if !ok {
		return
	}

	// The limiter's two dimensions are distinctly typed, so the IP and the
	// username cannot be transposed on the way in.
	rlIP, rlUser := ratelimit.ClientIP(ClientIP(r)), ratelimit.Username(req.Username)

	// Rate limit check.
	allowed, retryAfter := h.RateLimiter.Allow(rlIP, rlUser)
	if !allowed {
		Audit(r, slog.LevelWarn, AuditLoginRateLimited, false, req.Username,
			slog.Int("retry_after_seconds", int(retryAfter.Seconds())+1))
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		httpapi.TooManyRequestsC(w, r, subflux.CodeRateLimited, "too many attempts")
		return
	}

	ctx := r.Context()
	dbCtx, dbCancel := dbCtx(ctx)
	user, found, err := h.Store.UserByUsername(dbCtx, req.Username)
	dbCancel()
	if err != nil {
		slog.Error("login: db error", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	if !found {
		_, _ = auth.VerifyPassword(req.Password, auth.DummyHash())
		h.RateLimiter.Record(rlIP, rlUser)
		Audit(r, slog.LevelWarn, AuditLoginFailure, false, req.Username,
			slog.String("reason", "unknown_username"))
		httpapi.UnauthorizedC(w, r, subflux.CodeAuthInvalidCredentials, "invalid credentials")
		return
	}

	if !user.Enabled {
		_, _ = auth.VerifyPassword(req.Password, auth.DummyHash())
		h.RateLimiter.Record(rlIP, rlUser)
		Audit(r, slog.LevelWarn, AuditLoginFailure, false, req.Username,
			slog.String("reason", "account_disabled"))
		httpapi.UnauthorizedC(w, r, subflux.CodeAuthInvalidCredentials, "invalid credentials")
		return
	}

	passOK, err := auth.VerifyPassword(req.Password, user.PasswordHash)
	if err != nil || !passOK {
		h.RateLimiter.Record(rlIP, rlUser)
		Audit(r, slog.LevelWarn, AuditLoginFailure, false, req.Username,
			slog.String("reason", "invalid_password"))
		httpapi.UnauthorizedC(w, r, subflux.CodeAuthInvalidCredentials, "invalid credentials")
		return
	}

	// Password verified: clear failure counters (anti soft-lockout, OWASP
	// ASVS 2.2.1) and create session directly.
	h.RateLimiter.Reset(rlIP, rlUser)
	if err := h.createSessionAndRespond(w, r, user, auth.MethodPassword); err != nil {
		slog.Error("login: create session", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(auth.MethodPassword)))
}

// --- POST /api/auth/logout ---

// HandleLogout handles POST /api/auth/logout — invalidates the current session cookie.
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	// Best-effort: pull the session subject if available so the audit
	// record carries the user that logged out. The session lookup might
	// fail (invalid cookie, race against expiry) — emit the audit event
	// with empty user in that case rather than skip it.
	user := ""
	token := SessionCookie.ReadCookie(r)
	if token != "" {
		if sess, ok, err := h.Store.SessionByHash(r.Context(), auth.SessionHash(token)); err == nil && ok {
			if u, ok, err := h.Store.UserByID(r.Context(), sess.UserID); err == nil && ok {
				user = u.Username
			}
		}
		if err := h.Store.DeleteSession(r.Context(), auth.SessionHash(token)); err != nil {
			slog.Warn("logout: delete session", "error", err)
		}
	}

	SessionCookie.ClearCookie(w, r)
	httpapi.Ok(w)
	Audit(r, slog.LevelInfo, AuditLogout, true, user)
}

// --- GET /api/auth/setup ---

// HandleSetupStatus handles GET /api/auth/setup — returns whether initial setup is required.
func (h *Handler) HandleSetupStatus(w http.ResponseWriter, r *http.Request) {
	dbCtx, dbCancel := dbCtx(r.Context())
	count, err := h.Store.UserCount(dbCtx)
	dbCancel()
	if err != nil {
		slog.Error("setup: user count", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	httpapi.WriteJSON(w, subflux.SetupStatus{
		SetupRequired: count == 0,
		ConfigValid:   h.Configured(),
	})
}

// --- POST /api/auth/setup ---

// HandleSetupCreate handles POST /api/auth/setup — creates the initial admin account.
// Only succeeds when no users exist; subsequent calls return 409.
func (h *Handler) HandleSetupCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeAuthBody[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](w, r)
	if !ok {
		return
	}

	if req.Username == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "username required")
		return
	}
	if len([]rune(req.Username)) > maxUsernameLen {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "username too long")
		return
	}

	// During setup, password is the sole auth method: enforce minimum length.
	checkBreach := false
	cfg := h.Config()
	if cfg != nil {
		checkBreach = cfg.CheckBreachedPasswords()
	}
	hash, userMsg, err := ValidateAndHashPassword(r.Context(), PasswordCheck{
		Password:    req.Password,
		Username:    req.Username,
		SoleFactor:  true,
		CheckBreach: checkBreach,
	}, h.HTTPClient)
	if userMsg != "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, userMsg)
		return
	}
	if err != nil {
		slog.Error("setup: hash password", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	ctx := r.Context()

	count, err := h.Store.UserCount(ctx)
	if err != nil {
		slog.Error("setup: user count", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}
	if count > 0 {
		httpapi.ConflictC(w, r, subflux.CodeSetupAlreadyComplete, "setup already completed")
		return
	}

	now := time.Now()
	user := &auth.User{
		Username:     req.Username,
		PasswordHash: string(hash),
		Role:         auth.RoleAdmin,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := h.Store.CreateUser(ctx, user); err != nil {
		slog.Error("setup: create user", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	slog.Info("setup: admin account created", "username", user.Username)

	if err := h.createSessionAndRespond(w, r, user, auth.MethodPassword); err != nil {
		slog.Error("setup: create session", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
	}
}

// --- GET /api/auth/me ---

// HandleAuthMe handles GET /api/auth/me — returns profile information for the current user.
func (h *Handler) HandleAuthMe(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	passkeyCount, errPK := h.Store.PasskeyCountForUser(r.Context(), user.ID)
	if errPK != nil {
		slog.Warn("auth me: passkey count", "error", errPK)
	}

	// The last local admin can't migrate to SSO-only (break-glass guard), so
	// the UI shouldn't offer the link.
	canLinkOIDC := true
	if last, err := h.isLastLocalAdmin(r.Context(), user); err != nil {
		slog.Warn("auth me: admin check", "error", err)
	} else if last {
		canLinkOIDC = false
	}

	httpapi.WriteJSON(w, subflux.MeResponse{
		ID:          user.ID,
		Username:    user.Username,
		Role:        user.Role,
		HasPasskeys: passkeyCount > 0,
		OIDCLinked:  user.OIDCSub != "",
		HasPassword: user.PasswordHash != "",
		CanLinkOIDC: canLinkOIDC,
	})
}
