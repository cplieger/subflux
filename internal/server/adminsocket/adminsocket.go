// Package adminsocket serves the out-of-band admin plane: the one route the CLI
// posts to for password reset and API-key generation.
//
// It exists because bbolt takes an exclusive OS file lock, so the CLI cannot open
// the store while the server holds it. The commands travel over HTTP to the
// running process instead, on a Unix socket in a 0700 directory rather than the
// TCP mux — the kernel's socket custody IS the security boundary, which is why
// these handlers require no credentials and why that is safe. main.go owns the
// listener; this package owns the handlers.
//
// The three of them read one collaborator (the auth store) plus a metrics hook and
// were the last coherent handler cluster on Server. Everything the plane needs is
// in Deps: nothing here reads the live config, the live snapshot, or the main
// store.
package adminsocket

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/auth/v5"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/webhttp/v2"
)

// AuthStore is the four writes and one read the bootstrap actions perform. Five
// of the auth SPI's methods, named here because this is the package that calls
// them.
type AuthStore interface {
	UserByUsername(ctx context.Context, username string) (user *auth.User, found bool, err error)
	UpdateUser(ctx context.Context, user *auth.User) error
	DeleteUserSessions(ctx context.Context, userID int64, exceptHash string) error
	CreateAPIKey(ctx context.Context, key *auth.Key) error
}

// PanicRecorder counts a recovered panic. One method: this plane installs no
// request-metric hook at all (see Handler).
type PanicRecorder interface {
	RecordPanic()
}

// Deps is what the plane needs.
type Deps struct {
	Store   AuthStore
	Metrics PanicRecorder
}

// Plane serves the admin socket's single route.
type Plane struct {
	deps Deps
}

// New returns a Plane over deps.
func New(deps Deps) *Plane { return &Plane{deps: deps} }

// Handler returns the admin-plane handler: a one-route mux serving
// POST /api/admin/bootstrap, wrapped in the same access-log/request-ID and
// panic-recovery middleware the TCP mux uses (the body limit is inside the
// handler via webhttp.DecodeBody, exactly as on the TCP plane). It installs no
// request-metric hook, deliberately: http_requests_total describes the public
// HTTP surface, and this plane is one route reachable only by a same-container
// process, so a series for it would carry no signal. Every http_requests_total
// sample therefore comes from the server's own middleware chain.
//
// main.go serves it on a second http.Server bound to the Unix socket in the 0700
// directory config.AdminSocketDir — kernel socket custody replaces the former
// requireLocalhost peer-address check, so the zero-credential bootstrap channel
// is unreachable over every TCP path (netns-sharing peers and proxied clients
// included). Both configured and unconfigured server modes expose it.
func (p *Plane) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+config.AdminBootstrapURLPath, p.handleBootstrap)
	return webhttp.Chain(mux,
		webhttp.Logging(
			webhttp.WithLogger(slog.Default()),
		),
		webhttp.Recoverer(
			webhttp.WithRecoverLogger(slog.Default()),
			webhttp.WithPanicHook(func(_ any, _ []byte) { p.deps.Metrics.RecordPanic() }),
		),
	)
}

// handleBootstrap serves the CLI auth commands routed through the running server.
func (p *Plane) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action   string `json:"action"`
		Username string `json:"username"`
		Password string `json:"password"`
		Label    string `json:"label"`
	}
	// Cap + decode via webhttp.DecodeBody: it bounds the body at MaxJSONBody
	// (1 MiB) with an http.MaxBytesReader and, on any decode failure (including
	// trailing data past the single JSON object), writes the 400 envelope
	// {error,code:"bad_request",request_id}.
	if !webhttp.DecodeBody(w, r, &req, "invalid request body") {
		return
	}

	switch req.Action {
	case "reset-password":
		p.resetPassword(w, r, req.Username, req.Password)
	case "generate-api-key":
		p.generateAPIKey(w, r, req.Username, req.Label)
	default:
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "unknown action: "+req.Action)
	}
}

// resetPassword sets a user's password and invalidates their sessions.
func (p *Plane) resetPassword(w http.ResponseWriter, r *http.Request, username, password string) {
	if username == "" || password == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "username and password are required")
		return
	}

	ctx := r.Context()
	user, found, err := p.deps.Store.UserByUsername(ctx, username)
	if err != nil {
		slog.Error("admin bootstrap: reset-password lookup", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}
	if !found {
		httpapi.NotFoundC(w, r, subflux.CodeNotFound, "user not found: "+username)
		return
	}

	if errLen := auth.ValidateSoloPasswordLength(password); errLen != nil {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, errLen.Error())
		return
	}
	pctx := auth.PasswordContext{Username: username, ForbiddenWords: []string{"subflux"}}
	if errCtx := auth.ValidatePasswordContext(password, pctx); errCtx != nil {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, errCtx.Error())
		return
	}

	user.PasswordHash = auth.HashPassword(password)
	user.UpdatedAt = time.Now()
	if err := p.deps.Store.UpdateUser(ctx, user); err != nil {
		slog.Error("admin bootstrap: update user", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	if err := p.deps.Store.DeleteUserSessions(ctx, user.ID, ""); err != nil {
		slog.Warn("admin bootstrap: invalidate sessions", "error", err)
	}

	slog.Info("admin bootstrap: password reset", "username", username, "ip", authhandlers.ClientIP(r))
	httpapi.WriteJSON(w, map[string]string{subflux.KeyStatus: "ok", "username": username})
}

// generateAPIKey mints a new API key for a user and returns the plaintext once.
func (p *Plane) generateAPIKey(w http.ResponseWriter, r *http.Request, username, label string) {
	if username == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "username is required")
		return
	}

	ctx := r.Context()
	user, found, err := p.deps.Store.UserByUsername(ctx, username)
	if err != nil {
		slog.Error("admin bootstrap: generate-api-key lookup", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}
	if !found {
		httpapi.NotFoundC(w, r, subflux.CodeNotFound, "user not found: "+username)
		return
	}

	plaintext, hash, prefix, suffix := auth.GenerateAPIKey("sfx_")

	apiKey := &auth.Key{
		UserID:    user.ID,
		KeyHash:   hash,
		KeyPrefix: prefix,
		KeySuffix: suffix,
		Label:     label,
		CreatedAt: time.Now(),
	}
	if err := p.deps.Store.CreateAPIKey(ctx, apiKey); err != nil {
		slog.Error("admin bootstrap: store api key", "error", err)
		httpapi.InternalErrorC(w, r, nil, subflux.CodeInternalError)
		return
	}

	slog.Info("admin bootstrap: API key generated",
		"username", username, "label", label, "ip", authhandlers.ClientIP(r))
	httpapi.WriteJSON(w, map[string]string{subflux.KeyStatus: "ok", "key": plaintext})
}
