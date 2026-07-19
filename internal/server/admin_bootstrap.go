package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/webhttp"
)

// handleAdminBootstrap serves CLI auth commands (reset-password, generate-api-key)
// routed through the running server. bbolt's exclusive OS file lock prevents the
// CLI from opening the store directly while the server holds it, so the CLI posts
// to this endpoint instead. It is served ONLY on the Unix-socket admin plane
// (see AdminHandler); the kernel's socket custody — a 0700 directory only
// same-container processes can traverse — is the security boundary, so the
// handler itself requires no credentials (matching the first-boot recovery
// use case via docker exec).
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

// AdminHandler returns the admin-plane handler: a one-route mux serving
// POST /api/admin/bootstrap, wrapped in the same access-log/request-ID and
// panic-recovery middleware the TCP mux uses (the body limit is inside the
// handler via webhttp.DecodeBody, exactly as on the TCP plane). main.go
// serves it on a second http.Server bound to the Unix socket in the 0700
// directory config.AdminSocketDir — kernel socket custody replaces the
// former requireLocalhost peer-address check, so the zero-credential
// bootstrap channel is unreachable over every TCP path (netns-sharing peers
// and proxied clients included). Both configured and unconfigured server
// modes expose it.
func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+config.AdminBootstrapURLPath, s.handleAdminBootstrap)
	return webhttp.Chain(mux,
		webhttp.Logging(
			webhttp.WithLogger(slog.Default()),
		),
		webhttp.Recoverer(
			webhttp.WithRecoverLogger(slog.Default()),
			webhttp.WithPanicHook(func(_ any, _ []byte) { s.metrics.RecordPanic() }),
		),
	)
}

// RecordPersistentAlert records a manually-dismissable operator alert.
// Exported for the composition root: main.go owns the admin-socket listener
// lifecycle and reports its failure as a persistent alert here (degraded
// mode — bootstrap unavailable — rather than fatal).
func (s *Server) RecordPersistentAlert(source, msg string) {
	s.alerts.RecordPersistent(source, msg)
}
