package server

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/authhandlers"
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
	// Cap + decode via webhttp.DecodeBody: it bounds the body at MaxJSONBody
	// (1 MiB) with an http.MaxBytesReader and, on any decode failure (including
	// trailing data past the single JSON object), writes the 400 envelope
	// {error,code:"bad_request",request_id} — byte-identical to the previous
	// api.BadRequestC(api.CodeBadRequest) since both route through
	// webhttp.WriteError.
	if !webhttp.DecodeBody(w, r, &req, "invalid request body") {
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

	if errLen := auth.ValidatePasswordLength(password, true); errLen != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, errLen.Error())
		return
	}
	if errCtx := auth.ValidatePasswordContext(password, username, []string{"subflux"}); errCtx != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, errCtx.Error())
		return
	}

	hash, err := auth.HashPassword(password)
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

	slog.Info("admin bootstrap: password reset", "username", username, "ip", authhandlers.ClientIP(r))
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

	plaintext, hash, prefix, suffix, err := auth.GenerateAPIKey("sfx_")
	if err != nil {
		slog.Error("admin bootstrap: generate api key", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	apiKey := &auth.Key{
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
		"username", username, "label", label, "ip", authhandlers.ClientIP(r))
	api.WriteJSON(w, map[string]string{keyStatus: "ok", "key": plaintext})
}

// requireLocalhost is a middleware that rejects requests not originating from
// loopback (127.0.0.1 or ::1). This guards the admin bootstrap endpoint so
// only processes on the same host (docker exec) can call it — no auth token
// is needed, matching the first-boot recovery use case.
func (s *Server) requireLocalhost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check the raw SOCKET PEER, never the proxy-aware resolver. The
		// trusted-proxy ClientIP consults X-Forwarded-For when the peer is in
		// a trusted CIDR, so any host in that subnet could forge
		// "X-Forwarded-For: 127.0.0.1" and reach this unauthenticated
		// endpoint. The socket peer cannot be spoofed: a same-host docker
		// exec connects from loopback directly, and a request arriving via a
		// reverse proxy has the proxy's (non-loopback) address and is
		// correctly rejected — a proxied request is not a local call.
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			api.ForbiddenC(w, r, api.CodeForbidden, "admin bootstrap is localhost-only")
			return
		}
		next(w, r)
	}
}
