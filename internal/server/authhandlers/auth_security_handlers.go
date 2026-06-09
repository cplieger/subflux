package authhandlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	authlib "github.com/cplieger/auth"
	"github.com/cplieger/subflux/internal/api"
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

	ok, err := authlib.VerifyPassword(req.CurrentPassword, user.PasswordHash)
	if err != nil || !ok {
		h.RateLimiter.Record(ip, user.Username)
		api.UnauthorizedC(w, r, api.CodeAuthInvalidCredentials, "invalid current password")
		return
	}
	h.RateLimiter.Reset(ip, user.Username)

	ctx := r.Context()
	cfg := h.Config()
	// Password can authenticate on its own whenever basic auth is enabled,
	// so it must meet the strong (sole-factor) floor in that case.
	hash, userMsg, hashErr := ValidateAndHashPassword(ctx, req.NewPassword, user.Username, cfg.BasicAuthEnabled(), cfg.CheckBreachedPasswords(), h.HTTPClient)
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
