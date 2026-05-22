package authhandlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"subflux/internal/api"
	"subflux/internal/auth"
)

// --- GET /api/auth/oidc ---

func (h *Handler) HandleOIDCRedirect(w http.ResponseWriter, r *http.Request) {
	oidcProv := h.OIDCResolver()
	if oidcProv == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "OIDC not configured")
		return
	}

	state, err := auth.GenerateOIDCState()
	if err != nil {
		slog.Error("oidc: generate state", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	nonce, err := auth.GenerateOIDCState()
	if err != nil {
		slog.Error("oidc: generate nonce", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	verifier, challenge, err := auth.GeneratePKCE()
	if err != nil {
		slog.Error("oidc: generate PKCE", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	redirectURI := auth.ValidateRedirectURI(r.URL.Query().Get("redirect"))

	ctx := r.Context()
	if err := h.OidcDB.CreateOIDCState(ctx, state, nonce, verifier, redirectURI); err != nil {
		slog.Error("oidc: store state", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	authURL := oidcProv.AuthorizationURL(state, nonce, challenge)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// --- GET /api/auth/oidc/callback ---

func (h *Handler) HandleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	oidcProv := h.OIDCResolver()
	if oidcProv == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "OIDC not configured")
		return
	}

	q := r.URL.Query()
	stateParam := q.Get("state")
	code := q.Get("code")

	if errParam := q.Get("error"); errParam != "" {
		desc := q.Get("error_description")
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, "",
			slog.String("reason", "provider_error"),
			slog.String("error", errParam),
			slog.String("description", desc))
		api.UnauthorizedC(w, r, api.CodeOIDCExchangeFailed, "authentication failed")
		return
	}

	if stateParam == "" || code == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "missing state or code")
		return
	}

	ctx := r.Context()

	nonce, codeVerifier, redirectURI, err := h.OidcDB.ConsumeOIDCState(ctx, stateParam)
	if err != nil {
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, "",
			slog.String("reason", "invalid_state"))
		api.UnauthorizedC(w, r, api.CodeOIDCStateInvalid, "invalid or expired state")
		return
	}

	claims, oidcExpiry, err := oidcProv.Exchange(ctx, code, codeVerifier, nonce)
	if err != nil {
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, "",
			slog.String("reason", "exchange_failed"))
		api.UnauthorizedC(w, r, api.CodeOIDCExchangeFailed, "authentication failed")
		return
	}

	user, _, err := h.resolveOIDCUser(ctx, claims)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "oidc resolve user")
		return
	}

	if !user.Enabled {
		Audit(r, slog.LevelWarn, AuditOIDCCallback, false, user.Username,
			slog.String("reason", "account_disabled"))
		api.ForbiddenC(w, r, api.CodeAuthAccountDisabled, "account disabled")
		return
	}

	if err := h.createAndSetSession(w, r, user, api.MethodOIDC, oidcExpiry); err != nil {
		slog.Error("oidc: create session", "error", err)
		api.InternalErrorC(w, r, nil, api.CodeInternalError)
		return
	}

	redirectURI = auth.ValidateRedirectURI(redirectURI)
	Audit(r, slog.LevelInfo, AuditLoginSuccess, true, user.Username,
		slog.String("method", string(api.MethodOIDC)))
	http.Redirect(w, r, redirectURI, http.StatusFound) //nolint:gosec // G710: redirectURI validated above
}

// resolveOIDCUser looks up or creates a user from OIDC claims.
func (h *Handler) resolveOIDCUser(ctx context.Context, claims *auth.OIDCClaims) (*api.User, bool, error) {
	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}

	var (
		existingBySub             *api.User
		existingByEmail           *api.User
		existingByUsername        *api.User
		errSub, errEmail, errUser error
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		existingBySub, errSub = h.OidcDB.GetUserByOIDCSub(gctx, claims.Subject)
		return nil
	})
	g.Go(func() error {
		existingByEmail, errEmail = h.OidcDB.GetUserByEmail(gctx, claims.Email)
		return nil
	})
	g.Go(func() error {
		existingByUsername, errUser = h.OidcDB.GetUserByUsername(gctx, username)
		return nil
	})
	if err := g.Wait(); err != nil {
		slog.Warn("oidc: concurrent lookup error", "error", err)
	}

	if errSub != nil {
		slog.Warn("oidc: lookup by sub", "error", errSub)
	}
	if errEmail != nil {
		slog.Warn("oidc: lookup by email", "error", errEmail)
	}
	if errUser != nil {
		slog.Warn("oidc: lookup by username", "error", errUser)
	}

	user, isNew := auth.ResolveOIDCUser(claims, existingBySub, existingByEmail, existingByUsername)

	if isNew {
		now := time.Now()
		user.CreatedAt = now
		user.UpdatedAt = now
		if err := h.OidcDB.CreateUser(ctx, user); err != nil {
			return nil, false, fmt.Errorf("create user: %w", err)
		}
		slog.Info("oidc: new user created", "username", user.Username, "sub", claims.Subject)
	} else if user.OIDCSub == "" {
		user.OIDCSub = claims.Subject
		user.UpdatedAt = time.Now()
		if err := h.OidcDB.UpdateUser(ctx, user); err != nil {
			slog.Warn("oidc: link user", "error", err)
		}
	}

	return user, isNew, nil
}
