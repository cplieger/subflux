package authhandlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/auth/v4"
	"github.com/cplieger/auth/v4/ratelimit"
	"github.com/cplieger/runesafe/v2"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/subflux"
)

// --- PUT /api/auth/password ---

// HandleChangePassword handles PUT /api/auth/password — changes the current user's
// password after verifying the existing one, then invalidates all other sessions.
func (h *Handler) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	req, ok := decodeAuthBody[struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}](w, r)
	if !ok {
		return
	}

	// The limiter's two dimensions are distinctly typed, so the IP and the
	// username cannot be transposed on the way in.
	rlIP, rlUser := ratelimit.ClientIP(ClientIP(r)), ratelimit.Username(user.Username)
	allowed, retryAfter := h.RateLimiter.Allow(rlIP, rlUser)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		httpapi.TooManyRequestsC(w, r, subflux.CodeRateLimited, "too many attempts")
		return
	}

	ok, err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash)
	if err != nil || !ok {
		h.RateLimiter.Record(rlIP, rlUser)
		httpapi.UnauthorizedC(w, r, subflux.CodeAuthInvalidCredentials, "invalid current password")
		return
	}
	h.RateLimiter.Reset(rlIP, rlUser)

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
	hash, userMsg, hashErr := ValidateAndHashPassword(ctx, PasswordCheck{
		Password:    req.NewPassword,
		Username:    user.Username,
		SoleFactor:  soleFactor,
		CheckBreach: checkBreach,
	}, h.HTTPClient)
	if userMsg != "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, userMsg)
		return
	}
	if hashErr != nil {
		slog.Error("password change: hash", "error", hashErr)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	user.PasswordHash = string(hash)
	user.UpdatedAt = time.Now()
	if err := h.SecDB.UpdateUser(ctx, user); err != nil {
		slog.Error("password change: update user", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	currentHash := SessionHashFromContext(ctx)
	if err := h.SecDB.DeleteUserSessions(ctx, user.ID, currentHash); err != nil {
		slog.Warn("password change: invalidate sessions", "error", err)
	}

	slog.Info("security: password changed",
		"username", user.Username, "ip", ClientIP(r))

	httpapi.Ok(w)
	Audit(r, slog.LevelInfo, AuditPasswordChange, true, user.Username)
}

// --- PUT /api/auth/profile ---

// HandleUpdateProfile handles PUT /api/auth/profile — sets the current user's
// display name.
//
// An empty value clears it, which makes the username the label again. The
// username itself is deliberately not editable here: it is the login
// identifier, and every session, passkey and API key is keyed to the account
// rather than to the name.
//
// The client sends the WebAuthn current-user-details signal after a successful
// save, so a stored passkey's label follows the account instead of keeping the
// name it was registered under.
func (h *Handler) HandleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	req, ok := decodeAuthBody[struct {
		DisplayName string `json:"display_name"`
	}](w, r)
	if !ok {
		return
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if len([]rune(displayName)) > maxDisplayNameLen {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "display name too long")
		return
	}
	// Refuse rather than sanitize. This value reaches every surface that lists
	// the account and the user's own password manager, where a bidi control
	// reorders what a human reads, and silently rewriting what someone typed
	// hides the refusal from them.
	if strings.ContainsFunc(displayName, runesafe.IsUnsafeSingleLine) {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "display name contains a disallowed character")
		return
	}

	if displayName == user.DisplayName {
		httpapi.Ok(w)
		return
	}

	user.DisplayName = displayName
	user.UpdatedAt = time.Now()
	if err := h.SecDB.UpdateUser(r.Context(), user); err != nil {
		slog.Error("profile update: update user", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	httpapi.Ok(w)
	Audit(r, slog.LevelInfo, AuditProfileUpdate, true, user.Username)
}
