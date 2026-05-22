package authhandlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"subflux/internal/api"
	"subflux/internal/auth"
)

// --- PUT /api/auth/password ---

func (h *Handler) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	req, ok := decodeAuthBody[struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}](w, r)
	if !ok {
		return
	}

	ip := ClientIP(r)
	allowed, retryAfter := h.RateLimiter.Allow(ip, user.Username)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		api.TooManyRequestsC(w, r, api.CodeRateLimited, "too many attempts")
		return
	}

	ok, err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash)
	if err != nil || !ok {
		h.RateLimiter.Record(ip, user.Username)
		api.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "invalid current password")
		return
	}

	ctx := r.Context()
	passkeyCount, errPK := h.SecDB.PasskeyCountForUser(ctx, user.ID)
	if errPK != nil {
		slog.Warn("password change: passkey count", "error", errPK)
	}
	cfg := h.Config()
	passwordOnly := passkeyCount == 0 && (!cfg.OIDCEnabled() || user.OIDCSub == "")

	hash, userMsg, hashErr := ValidateAndHashPassword(ctx, req.NewPassword, passwordOnly, cfg.CheckBreachedPasswords(), h.HTTPClient)
	if userMsg != "" {
		api.BadRequestC(w, r, api.CodeBadRequest, userMsg)
		return
	}
	if hashErr != nil {
		slog.Error("password change: hash", "error", hashErr)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	user.PasswordHash = hash
	user.UpdatedAt = time.Now()
	if err := h.SecDB.UpdateUser(ctx, user); err != nil {
		slog.Error("password change: update user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	currentHash := api.SessionHashFromContext(ctx)
	if err := h.SecDB.DeleteUserSessions(ctx, user.ID, currentHash); err != nil {
		slog.Warn("password change: invalidate sessions", "error", err)
	}

	slog.Info("security: password changed",
		"username", user.Username, "ip", ClientIP(r))

	api.Ok(w)
	Audit(r, slog.LevelInfo, AuditPasswordChange, true, user.Username)
}

// --- POST /api/auth/totp/enable ---

func (h *Handler) HandleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	secret, uri, err := auth.GenerateTOTPSecret(user.Username, TOTPIssuer)
	if err != nil {
		slog.Error("totp enable: generate secret", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	api.WriteJSON(w, api.TOTPSetupResponse{
		Secret: secret,
		URI:    uri,
	})
}

// --- POST /api/auth/totp/confirm ---

func (h *Handler) HandleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	req, ok := decodeAuthBody[struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}](w, r)
	if !ok {
		return
	}

	if !auth.ValidateTOTPCode(req.Secret, req.Code) {
		api.BadRequestC(w, r, api.CodeTOTPInvalid, "invalid TOTP code")
		return
	}

	ctx := r.Context()
	cfg := h.Config()

	totpKey, keyErr := cfg.TOTPEncryptionKey()
	if keyErr != nil {
		slog.Error("totp confirm: encryption key", "error", keyErr)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	encSecret, err := auth.Encrypt([]byte(req.Secret), totpKey)
	if err != nil {
		slog.Error("totp confirm: encrypt secret", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if errStore := h.SecDB.SetTOTPSecret(ctx, user.ID, encSecret); errStore != nil {
		slog.Error("totp confirm: store secret", "error", errStore)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	user.TOTPEnabled = true
	user.UpdatedAt = time.Now()
	if errUpdate := h.SecDB.UpdateUser(ctx, user); errUpdate != nil {
		slog.Error("totp confirm: update user", "error", errUpdate)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	codes, hashes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		slog.Error("totp confirm: generate recovery codes", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if err := h.SecDB.SetRecoveryCodes(ctx, user.ID, hashes); err != nil {
		slog.Error("totp confirm: store recovery codes", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: TOTP enabled",
		"username", user.Username, "ip", ClientIP(r))

	api.WriteJSON(w, api.RecoveryCodesResponse{
		RecoveryCodes: codes,
	})
	Audit(r, slog.LevelInfo, AuditTOTPEnable, true, user.Username)
}

// --- DELETE /api/auth/totp ---

func (h *Handler) HandleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	req, ok := decodeAuthBody[struct {
		Code string `json:"code"`
	}](w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	ok, err := h.decryptAndVerifyTOTP(ctx, user.ID, req.Code)
	if err != nil {
		slog.Error("totp disable: verify", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	if !ok {
		api.UnauthorizedC(w, r, api.CodeTOTPInvalid, "invalid TOTP code")
		return
	}

	currentStep, replayed := checkTOTPReplay(user)
	if replayed {
		Audit(r, slog.LevelWarn, AuditTOTPDisable, false, user.Username,
			slog.String("reason", "replay_detected"))
		api.UnauthorizedC(w, r, api.CodeTOTPReplay, "code already used")
		return
	}
	user.LastTOTPStep = currentStep

	if err := h.SecDB.ClearTOTPSecret(ctx, user.ID); err != nil {
		slog.Error("totp disable: clear secret", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if err := h.SecDB.SetRecoveryCodes(ctx, user.ID, nil); err != nil {
		slog.Warn("totp disable: clear recovery codes", "error", err)
	}

	user.TOTPEnabled = false
	user.UpdatedAt = time.Now()
	if err := h.SecDB.UpdateUser(ctx, user); err != nil {
		slog.Error("totp disable: update user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: TOTP disabled",
		"username", user.Username, "ip", ClientIP(r))

	api.Ok(w)
	Audit(r, slog.LevelInfo, AuditTOTPDisable, true, user.Username)
}

// --- GET /api/auth/recovery-codes ---

func (h *Handler) HandleRecoveryCodesStatus(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	count, err := h.SecDB.RecoveryCodeCount(r.Context(), user.ID)
	if err != nil {
		slog.Error("recovery codes status: db error", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	api.WriteJSON(w, api.RecoveryCodesStatusResponse{
		Remaining: count,
		Total:     auth.RecoveryCodeTotal,
	})
}

// --- POST /api/auth/recovery-codes ---

func (h *Handler) HandleRegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user := api.UserFromContext(r.Context())

	if !user.TOTPEnabled {
		api.BadRequestC(w, r, api.CodeTOTPNotEnabled, "TOTP not enabled")
		return
	}

	codes, hashes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		slog.Error("regenerate recovery codes: generate", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if err := h.SecDB.SetRecoveryCodes(r.Context(), user.ID, hashes); err != nil {
		slog.Error("regenerate recovery codes: store", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("security: recovery codes regenerated",
		"username", user.Username, "ip", ClientIP(r))

	api.WriteJSON(w, api.RecoveryCodesResponse{
		RecoveryCodes: codes,
	})
}
