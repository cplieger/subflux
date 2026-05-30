package authhandlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"subflux/internal/api"
	"subflux/internal/auth"
)

// --- POST /api/auth/login ---

func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if cfg := h.Config(); cfg != nil && !cfg.BasicAuthEnabled() {
		api.ForbiddenC(w, r, api.CodeForbidden, "password login is disabled")
		return
	}

	req, ok := decodeAuthBody[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](w, r)
	if !ok {
		return
	}

	ip := ClientIP(r)

	// Rate limit check.
	allowed, retryAfter := h.RateLimiter.Allow(ip, req.Username)
	if !allowed {
		Audit(r, slog.LevelWarn, AuditLoginRateLimited, false, req.Username,
			slog.Int("retry_after_seconds", int(retryAfter.Seconds())+1))
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		api.TooManyRequestsC(w, r, api.CodeRateLimited, "too many attempts")
		return
	}

	ctx := r.Context()
	dbCtx, dbCancel := dbCtx(ctx)
	user, err := h.Store.GetUserByUsername(dbCtx, req.Username)
	dbCancel()
	if err != nil {
		slog.Error("login: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if user == nil {
		_, _ = auth.VerifyPassword(req.Password, auth.DummyHash())
		h.RateLimiter.Record(ip, req.Username)
		Audit(r, slog.LevelWarn, AuditLoginFailure, false, req.Username,
			slog.String("reason", "unknown_username"))
		api.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "invalid credentials")
		return
	}

	if !user.Enabled {
		_, _ = auth.VerifyPassword(req.Password, auth.DummyHash())
		h.RateLimiter.Record(ip, req.Username)
		Audit(r, slog.LevelWarn, AuditLoginFailure, false, req.Username,
			slog.String("reason", "account_disabled"))
		api.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "invalid credentials")
		return
	}

	passOK, err := auth.VerifyPassword(req.Password, user.PasswordHash)
	if err != nil || !passOK {
		h.RateLimiter.Record(ip, req.Username)
		Audit(r, slog.LevelWarn, AuditLoginFailure, false, req.Username,
			slog.String("reason", "invalid_password"))
		api.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "invalid credentials")
		return
	}

	// Password verified: create session directly.
	if err := h.createSessionAndRespond(w, r, user, api.MethodPassword); err != nil {
		slog.Error("login: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(api.MethodPassword)))
}

// --- POST /api/auth/logout ---

func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	// Best-effort: pull the session subject if available so the audit
	// record carries the user that logged out. The session lookup might
	// fail (invalid cookie, race against expiry) — emit the audit event
	// with empty user in that case rather than skip it.
	user := ""
	token := auth.ReadSessionCookie(r)
	if token != "" {
		if sess, err := h.Store.GetSessionByHash(r.Context(), auth.SessionHash(token)); err == nil && sess != nil {
			if u, err := h.Store.GetUserByID(r.Context(), sess.UserID); err == nil && u != nil {
				user = u.Username
			}
		}
		if err := h.Store.DeleteSession(r.Context(), auth.SessionHash(token)); err != nil {
			slog.Warn("logout: delete session", "error", err)
		}
	}

	auth.ClearSessionCookie(w, r)
	api.Ok(w)
	Audit(r, slog.LevelInfo, AuditLogout, true, user)
}

// --- GET /api/auth/setup ---

func (h *Handler) HandleSetupStatus(w http.ResponseWriter, r *http.Request) {
	dbCtx, dbCancel := dbCtx(r.Context())
	count, err := h.Store.UserCount(dbCtx)
	dbCancel()
	if err != nil {
		slog.Error("setup: user count", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	api.WriteJSON(w, api.SetupStatusResponse{
		SetupRequired: count == 0,
		ConfigValid:   h.Configured(),
	})
}

// --- POST /api/auth/setup ---

func (h *Handler) HandleSetupCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeAuthBody[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](w, r)
	if !ok {
		return
	}

	if req.Username == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "username required")
		return
	}
	if len([]rune(req.Username)) > maxUsernameLen {
		api.BadRequestC(w, r, api.CodeBadRequest, "username too long")
		return
	}

	// During setup, password is the sole auth method: enforce minimum length.
	checkBreach := false
	cfg := h.Config()
	if cfg != nil {
		checkBreach = cfg.CheckBreachedPasswords()
	}
	hash, userMsg, err := ValidateAndHashPassword(r.Context(), req.Password, req.Username, true, checkBreach, h.HTTPClient)
	if userMsg != "" {
		api.BadRequestC(w, r, api.CodeBadRequest, userMsg)
		return
	}
	if err != nil {
		slog.Error("setup: hash password", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	ctx := r.Context()

	count, err := h.Store.UserCount(ctx)
	if err != nil {
		slog.Error("setup: user count", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	if count > 0 {
		api.ConflictC(w, r, api.CodeSetupAlreadyComplete, "setup already completed")
		return
	}

	now := time.Now()
	user := &api.User{
		Username:     req.Username,
		PasswordHash: hash,
		Role:         api.RoleAdmin,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := h.Store.CreateUser(ctx, user); err != nil {
		slog.Error("setup: create user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("setup: admin account created", "username", user.Username)

	if err := h.createSessionAndRespond(w, r, user, api.MethodPassword); err != nil {
		slog.Error("setup: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
	}
}

// --- GET /api/auth/me ---

func (h *Handler) HandleAuthMe(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	passkeyCount, errPK := h.Store.PasskeyCountForUser(r.Context(), user.ID)
	if errPK != nil {
		slog.Warn("auth me: passkey count", "error", errPK)
	}

	api.WriteJSON(w, api.UserMeResponse{
		ID:          user.ID,
		Username:    user.Username,
		Role:        user.Role,
		HasPasskeys: passkeyCount > 0,
		OIDCLinked:  user.OIDCSub != "",
		HasPassword: user.PasswordHash != "",
	})
}
