package server

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	authlib "github.com/cplieger/auth"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/webhttp"
)

// handleAdminBootstrap serves CLI auth commands (reset-password, generate-api-key)
// routed through the running server. bbolt's exclusive OS file lock prevents the
// CLI from opening the store directly while the server holds it, so the CLI posts
// to this endpoint instead. Access is restricted to loopback (127.0.0.1 / ::1)
// so only processes on the same host (i.e. docker exec) can call it.
func (s *Server) handleAdminBootstrap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action   string `json:"action"`
		Username string `json:"username"`
		Password string `json:"password"`
		Label    string `json:"label"`
	}
	// Bound the request body (a handful of short fields) with webhttp.LimitBody
	// rather than reading it unbounded; an oversized body then fails the decode
	// and yields the same 400 envelope below.
	webhttp.LimitBody(w, r, webhttp.MaxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid request body")
		return
	}

	switch req.Action {
	case "reset-password":
		s.bootstrapResetPassword(w, r, req.Username, req.Password)
	case "generate-api-key":
		s.bootstrapGenerateAPIKey(w, r, req.Username, req.Label)
	default:
		api.BadRequestC(w, r, api.CodeBadRequest, "unknown action: "+req.Action)
	}
}

func (s *Server) bootstrapResetPassword(w http.ResponseWriter, r *http.Request, username, password string) {
	if username == "" || password == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "username and password are required")
		return
	}

	ctx := r.Context()
	user, err := s.authStore.GetUserByUsername(ctx, username)
	if err != nil {
		slog.Error("admin bootstrap: reset-password lookup", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	if user == nil {
		api.NotFoundC(w, r, api.CodeNotFound, "user not found: "+username)
		return
	}

	if errLen := authlib.ValidatePasswordLength(password, true); errLen != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, errLen.Error())
		return
	}
	if errCtx := authlib.ValidatePasswordContext(password, username, []string{"subflux"}); errCtx != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, errCtx.Error())
		return
	}

	hash, err := authlib.HashPassword(password)
	if err != nil {
		slog.Error("admin bootstrap: hash password", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	user.PasswordHash = hash
	user.UpdatedAt = time.Now()
	if err := s.authStore.UpdateUser(ctx, user); err != nil {
		slog.Error("admin bootstrap: update user", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	if err := s.authStore.DeleteUserSessions(ctx, user.ID, ""); err != nil {
		slog.Warn("admin bootstrap: invalidate sessions", "error", err)
	}

	slog.Info("admin bootstrap: password reset", "username", username, "ip", clientIPFromReq(r))
	api.WriteJSON(w, map[string]string{keyStatus: "ok", "username": username})
}

func (s *Server) bootstrapGenerateAPIKey(w http.ResponseWriter, r *http.Request, username, label string) {
	if username == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "username is required")
		return
	}

	ctx := r.Context()
	user, err := s.authStore.GetUserByUsername(ctx, username)
	if err != nil {
		slog.Error("admin bootstrap: generate-api-key lookup", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}
	if user == nil {
		api.NotFoundC(w, r, api.CodeNotFound, "user not found: "+username)
		return
	}

	plaintext, hash, prefix, suffix, err := authlib.GenerateAPIKey("sfx_")
	if err != nil {
		slog.Error("admin bootstrap: generate api key", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	apiKey := &api.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     label,
		CreatedAt: time.Now(),
	}
	if err := s.authStore.CreateAPIKey(ctx, apiKey); err != nil {
		slog.Error("admin bootstrap: store api key", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	slog.Info("admin bootstrap: API key generated",
		"username", username, "label", label, "ip", clientIPFromReq(r))
	api.WriteJSON(w, map[string]string{keyStatus: "ok", "key": plaintext})
}

// requireLocalhost is a middleware that rejects requests not originating from
// loopback (127.0.0.1 or ::1). This guards the admin bootstrap endpoint so
// only processes on the same host (docker exec) can call it — no auth token
// is needed, matching the first-boot recovery use case.
func (s *Server) requireLocalhost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// webhttp.ClientIP with no trusted proxy ranges returns the unspoofable
		// socket-peer host (X-Forwarded-For is ignored) — the same value the
		// former hand-rolled net.SplitHostPort produced.
		ip := net.ParseIP(webhttp.ClientIP(r))
		if ip == nil || !ip.IsLoopback() {
			api.ForbiddenC(w, r, api.CodeForbidden, "admin bootstrap is localhost-only")
			return
		}
		next(w, r)
	}
}

// clientIPFromReq extracts the client IP via the shared spoof-aware resolver.
// With no trusted proxy ranges it returns the unspoofable socket-peer host,
// matching the previous port-strip behavior.
func clientIPFromReq(r *http.Request) string {
	return webhttp.ClientIP(r)
}
