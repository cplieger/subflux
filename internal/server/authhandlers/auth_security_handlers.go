package authhandlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
)

// --- PUT /api/auth/password ---

// HandleChangePassword handles PUT /api/auth/password — changes the current user's
// password after verifying the existing one, then invalidates all other sessions.
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
	h.RateLimiter.Reset(ip, user.Username)

	ctx := r.Context()
	// Password can authenticate on its own whenever basic auth is enabled,
	// so it must meet the strong (sole-factor) floor in that case. This
	// endpoint is reachable in unconfigured mode, where Config() is nil:
	// default to the strong floor (basic auth enabled) and no breach check,
	// matching the setup handler's defaults.
	soleFactor, checkBreach := true, false
	if cfg := h.Config(); cfg != nil {
		soleFactor = cfg.BasicAuthEnabled()
		checkBreach = cfg.CheckBreachedPasswords()
	}
	hash, userMsg, hashErr := ValidateAndHashPassword(ctx, req.NewPassword, user.Username, soleFactor, checkBreach, h.HTTPClient)
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
