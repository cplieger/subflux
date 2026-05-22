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

	// Password verified. Check if TOTP is required.
	if user.TOTPEnabled {
		token, err := GenerateCeremonyToken()
		if err != nil {
			slog.Error("login: generate totp token", "error", err)
			api.InternalErrorC(w, r, nil, api.CodeInternalError)
			return
		}

		if !h.Ceremonies.TOTP.Store(token, &PendingTOTP{
			UserID:    user.ID,
			SessHash:  "", // filled on completion
			IP:        ip,
			CreatedAt: time.Now(),
		}) {
			slog.Warn("login: ceremony session limit reached")
			api.ServiceUnavailableC(w, r, api.CodeServiceUnavailable, "too many pending sessions")
			return
		}

		api.WriteJSONStatus(w, http.StatusUnauthorized, api.TOTPRequiredResponse{
			Error:        "totp_required",
			TOTPRequired: true,
			TOTPToken:    token,
		})
		// Password verified, awaiting TOTP. Audited as login.success
		// happens once TOTP completes; this intermediate stage is not
		// itself a login.
		return
	}

	// No TOTP: create session directly.
	if err := h.createSessionAndRespond(w, r, user, api.MethodPassword); err != nil {
		slog.Error("login: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(api.MethodPassword)))
}

// --- POST /api/auth/totp ---

func (h *Handler) HandleTOTPVerify(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeAuthBody[struct {
		Code      string `json:"code"`
		TOTPToken string `json:"totp_token"`
	}](w, r)
	if !ok {
		return
	}

	// Look up and consume the pending TOTP entry.
	pending, ok := h.Ceremonies.TOTP.LoadAndDelete(req.TOTPToken)

	if !ok || time.Since(pending.CreatedAt) > CeremonyTTL {
		api.UnauthorizedC(w, r, api.CodeAuthSessionInvalid, "invalid or expired totp token")
		return
	}

	ctx := r.Context()
	dbCtx, dbCancel := dbCtx(ctx)
	user, err := h.Store.GetUserByID(dbCtx, pending.UserID)
	dbCancel()
	if err != nil || user == nil {
		slog.Error("totp: user lookup failed", "error", err, "user_id", pending.UserID)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	// Get the TOTP secret and validate the code.
	ok, err = h.decryptAndVerifyTOTP(ctx, user.ID, req.Code)
	if err != nil {
		slog.Error("totp: verify", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	// Try TOTP code first, then recovery code.
	ip := ClientIP(r)
	if ok {
		// Check replay: track last_totp_step to prevent reuse within the same 30s window.
		currentStep, replayed := checkTOTPReplay(user)
		if replayed {
			Audit(r, slog.LevelWarn, AuditTOTPVerify, false, user.Username,
				slog.String("reason", "replay_detected"))
			api.UnauthorizedC(w, r, api.CodeTOTPReplay, "code already used")
			return
		}
		user.LastTOTPStep = currentStep
		if err := h.Store.UpdateUser(ctx, user); err != nil {
			slog.Error("totp: update last step", "error", err)
		}
	} else {
		// Try as recovery code: hash it and attempt to consume from DB.
		codeHash := auth.HexSHA256(req.Code)
		used, err := h.Store.UseRecoveryCode(ctx, user.ID, codeHash)
		if err != nil || !used {
			h.RateLimiter.Record(ip, user.Username)
			Audit(r, slog.LevelWarn, AuditTOTPVerify, false, user.Username,
				slog.String("reason", "invalid_code"))
			api.UnauthorizedC(w, r, api.CodeTOTPInvalid, "invalid code")
			return
		}
		remaining, rcErr := h.Store.RecoveryCodeCount(ctx, user.ID)
		if rcErr != nil {
			slog.Warn("totp: recovery code count", "error", rcErr)
		}
		Audit(r, slog.LevelInfo, AuditRecoveryUse, true, user.Username,
			slog.Int("remaining", remaining))
	}

	if err := h.createSessionAndRespond(w, r, user, api.MethodPasswordTOTP); err != nil {
		slog.Error("totp: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(api.MethodPasswordTOTP)))
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
	hash, userMsg, err := ValidateAndHashPassword(r.Context(), req.Password, true, checkBreach, h.HTTPClient)
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
		TOTPEnabled: user.TOTPEnabled,
		HasPasskeys: passkeyCount > 0,
	})
}

// --- POST /api/auth/reauth ---

func (h *Handler) HandleReauth(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	req, ok := decodeAuthBody[struct {
		Method   string `json:"method"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}](w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	verified := false

	switch api.AuthMethod(req.Method) {
	case api.MethodPassword:
		if user.PasswordHash == "" {
			api.BadRequestC(w, r, api.CodeBadRequest, "no password set")
			return
		}
		ip := ClientIP(r)
		allowed, retryAfter := h.RateLimiter.Allow(ip, user.Username)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			api.TooManyRequestsC(w, r, api.CodeRateLimited, "too many attempts")
			return
		}
		ok, err := auth.VerifyPassword(req.Password, user.PasswordHash)
		if err != nil {
			slog.Error("reauth: verify password", "error", err)
			api.InternalErrorC(w, r, nil, api.CodeInternalError)
			return
		}
		if !ok {
			h.RateLimiter.Record(ip, user.Username)
			Audit(r, slog.LevelWarn, AuditReauthFailure, false, user.Username,
				slog.String("method", string(api.MethodPassword)),
				slog.String("reason", "invalid_password"))
		}
		verified = ok

	case api.MethodTOTP:
		if !user.TOTPEnabled {
			api.BadRequestC(w, r, api.CodeTOTPNotEnabled, "TOTP not enabled")
			return
		}
		ok, err := h.decryptAndVerifyTOTP(ctx, user.ID, req.Code)
		if err != nil {
			slog.Error("reauth: TOTP verify", "error", err)
			api.InternalErrorC(w, r, nil, api.CodeInternalError)
			return
		}
		if ok {
			currentStep, replayed := checkTOTPReplay(user)
			if replayed {
				Audit(r, slog.LevelWarn, AuditReauthFailure, false, user.Username,
					slog.String("method", string(api.MethodTOTP)),
					slog.String("reason", "replay_detected"))
				api.UnauthorizedC(w, r, api.CodeTOTPReplay, "code already used")
				return
			}
			user.LastTOTPStep = currentStep
			if err := h.Store.UpdateUser(ctx, user); err != nil {
				slog.Error("reauth: update last TOTP step", "error", err)
			}
			verified = true
		} else {
			Audit(r, slog.LevelWarn, AuditReauthFailure, false, user.Username,
				slog.String("method", string(api.MethodTOTP)),
				slog.String("reason", "invalid_code"))
		}

	default:
		api.BadRequestC(w, r, api.CodeBadRequest, "unsupported method")
		return
	}

	if !verified {
		api.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "verification failed")
		return
	}

	// Update session reauth_at timestamp.
	if sessHash := api.SessionHashFromContext(ctx); sessHash != "" {
		if err := h.Store.UpdateSessionReauth(ctx, sessHash, time.Now()); err != nil {
			slog.Warn("reauth: update session", "error", err)
		}
	}

	api.Ok(w)
	Audit(r, slog.LevelInfo, AuditReauthSuccess, true, user.Username,
		slog.String("method", req.Method))
}

// checkTOTPReplay detects TOTP code reuse within the same 30-second window.
// Returns the current step and whether the code was already used (replayed).
func checkTOTPReplay(user *api.User) (currentStep int64, replayed bool) {
	currentStep = time.Now().Unix() / 30
	return currentStep, user.LastTOTPStep >= currentStep
}
