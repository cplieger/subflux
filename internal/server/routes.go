package server

// Authoritative permission model for subflux.
//
// Every HTTP endpoint declares its policy by which routeGroup it joins in
// registerRoutes (server.go). The group's middleware chain is the single
// enforcement point for that policy; handlers do not re-check auth, do not
// re-check roles, and do not call reauth helpers inline. This is the only
// file in the server package that encodes per-endpoint permissions.
//
// Each middleware is a clean leaf — it performs exactly one check and
// delegates to next. Groups compose leaves in order. There are no hidden
// chains (e.g. a middleware that secretly calls another middleware
// inside its body). What you see in newRouteGroup is what runs.
//
// Six groups cover every endpoint:
//
//	public           — no auth at all (health, metrics, static assets,
//	                   credential-establishing flows: /api/auth/login,
//	                   /api/auth/setup, OIDC redirect/callback, WebAuthn
//	                   login begin/finish, TOTP verify, logout).
//	user             — requireAuth. Handlers in this chain may rely on
//	                   UserFromContext returning a non-nil, enabled user.
//	admin            — requireAuth + requireRole(admin).
//	reauth           — requireAuth + requireRecentReauth. Reserved for
//	                   operations that delete credentials or mint
//	                   long-lived tokens without providing fresh in-band
//	                   proof: DELETE passkey, DELETE TOTP, POST api key,
//	                   DELETE api key. Operations that come with their
//	                   own fresh proof (password change verifies current
//	                   password; TOTP confirm verifies the code; WebAuthn
//	                   register finishes a ceremony) stay in the `user`
//	                   group because the ceremony itself is the fresh
//	                   factor.
//	userConfigured   — requireAuth + requireConfigured. 503 if no valid
//	                   config yet.
//	adminConfigured  — requireAuth + requireRole(admin) + requireConfigured.
//
// Group membership rules (enforced at read time, not compile time):
//
//  1. Every public-endpoint handler can access no authenticated state.
//  2. Every non-public handler can read `api.UserFromContext(r.Context())`
//     without a nil check.
//  3. Every admin handler can skip role checks.
//  4. Every reauth handler runs with a session token; API-key clients are
//     allowed to proceed without reauth (the middleware no-ops for them).
//  5. Configured handlers run only when a valid config is loaded; they may
//     dereference s.state().cfg without checking s.configured.

import (
	"net/http"
	"slices"

	"subflux/internal/api"
)

// middleware wraps an http.HandlerFunc with additional behavior.
// The chain runs outside-in: the first middleware's outer wrapper is
// called first, then its body runs next() which invokes the second,
// and so on.
type middleware func(http.HandlerFunc) http.HandlerFunc

// routeGroup is a collection of routes sharing a middleware chain.
// Routes registered via Add are wrapped with every middleware in the
// chain at registration time; no caller can forget a wrapper.
type routeGroup struct {
	mux   *http.ServeMux
	chain []middleware
}

// newRouteGroup creates a group bound to `mux` with the given middleware
// chain. Applied outside-in: the first middleware wraps the second, which
// wraps the third, etc. A zero-length chain produces a pass-through group.
func newRouteGroup(mux *http.ServeMux, chain ...middleware) *routeGroup {
	return &routeGroup{mux: mux, chain: chain}
}

// Add registers a handler on the group's mux under the given pattern,
// wrapping it with every middleware in the group's chain.
//
// Pattern follows Go 1.22+ ServeMux syntax (e.g. "GET /api/search").
func (g *routeGroup) Add(pattern string, handler http.HandlerFunc) {
	wrapped := handler
	for _, mw := range slices.Backward(g.chain) {
		wrapped = mw(wrapped)
	}
	g.mux.HandleFunc(pattern, wrapped)
}

// registerRoutes registers all HTTP routes on the given mux. Routes are
// grouped by auth policy — the six groups above are the only place where
// endpoint policy is declared; handlers never re-check auth, roles, or
// reauth.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	public := newRouteGroup(mux)
	user := newRouteGroup(mux, s.requireAuth)
	admin := newRouteGroup(mux, s.requireAuth, s.requireRole(api.RoleAdmin))
	reauth := newRouteGroup(mux, s.requireAuth, s.requireRecentReauth)
	userConfigured := newRouteGroup(mux, s.requireAuth, s.requireConfigured)
	adminConfigured := newRouteGroup(mux, s.requireAuth, s.requireRole(api.RoleAdmin), s.requireConfigured)

	// --- public: no auth ---

	// Health and metrics (probes, Prometheus). Health is at
	// /api/health for cross-app consistency with the homelab's
	// custom Go apps; metrics stays at /metrics per Prometheus
	// convention. Both live in the public scope (no auth required).
	public.Add("/api/health", s.handleHealth)
	public.Add("/metrics", s.metrics.Handler())

	// Credential-establishing flows. Every endpoint here either creates
	// a session (login, setup, OIDC callback, TOTP verify, WebAuthn login
	// finish) or prepares one (OIDC redirect, WebAuthn login begin). The
	// client is by definition unauthenticated when calling these.
	public.Add("GET /api/auth/setup", s.authH.HandleSetupStatus)
	public.Add("POST /api/auth/setup", s.authH.HandleSetupCreate)
	public.Add("POST /api/auth/login", s.authH.HandleLogin)
	public.Add("POST /api/auth/totp", s.authH.HandleTOTPVerify)
	public.Add("POST /api/auth/logout", s.authH.HandleLogout)
	public.Add("GET /api/auth/oidc", s.authH.HandleOIDCRedirect)
	public.Add("GET /api/auth/oidc/callback", s.authH.HandleOIDCCallback)
	public.Add("POST /api/auth/webauthn/login/begin", s.authH.HandleWebAuthnLoginBegin)
	public.Add("POST /api/auth/webauthn/login/finish", s.authH.HandleWebAuthnLoginFinish)

	// --- user: requires a session or valid API key ---

	// Server-sent events (always available, config-independent).
	user.Add("GET /api/events", s.handleEvents)

	// Self-service account endpoints. These never delete credentials or
	// mint long-lived tokens, so reauth is not required here. Operations
	// that confirm credentials (changePassword, totpConfirm, webauthn
	// register finish) carry their own in-band proof and also live here.
	user.Add("GET /api/auth/me", s.authH.HandleAuthMe)
	user.Add("POST /api/auth/reauth", s.authH.HandleReauth)
	// Passkey reauth ceremony. Begin returns a user-scoped WebAuthn
	// challenge; finish verifies the assertion and bumps ReauthAt on the
	// current session. Both are user-group (the user is already
	// authenticated; reauth is just freshness).
	user.Add("POST /api/auth/reauth/passkey/begin", s.authH.HandleReauthPasskeyBegin)
	user.Add("POST /api/auth/reauth/passkey/finish", s.authH.HandleReauthPasskeyFinish)
	user.Add("PUT /api/auth/password", s.authH.HandleChangePassword)
	user.Add("POST /api/auth/totp/enable", s.authH.HandleTOTPEnable)
	user.Add("POST /api/auth/totp/confirm", s.authH.HandleTOTPConfirm)
	user.Add("GET /api/auth/passkeys", s.authH.HandleListPasskeys)
	user.Add("GET /api/auth/webauthn/signal-data", s.authH.HandleWebAuthnSignalData)
	user.Add("POST /api/auth/webauthn/register/begin", s.authH.HandleWebAuthnRegisterBegin)
	user.Add("POST /api/auth/webauthn/register/finish", s.authH.HandleWebAuthnRegisterFinish)
	user.Add("PUT /api/auth/passkeys/", s.authH.HandleRenamePasskey)
	user.Add("GET /api/auth/apikeys", s.authH.HandleListAPIKeys)

	// Recovery code status (count). Not sensitive; anyone who can read
	// /api/auth/me can see whether the user has TOTP configured, and
	// "how many recovery codes remain" is the next question the UI
	// needs answered. No reauth required.
	user.Add("GET /api/auth/recovery-codes", s.authH.HandleRecoveryCodesStatus)

	// Config schema (read-only; available even when unconfigured).
	user.Add("GET /api/config/schema", s.configH.HandleConfigSchema)
	user.Add("POST /api/config/validate-path", s.configH.HandleValidatePath)

	// Alerts (read + dismiss).
	user.Add("/api/alerts", s.handleGetAlerts)

	// Activity feed (user-visible history; config-independent).
	user.Add("GET /api/activity", s.handleGetActivity)
	user.Add("DELETE /api/activity", s.handleDismissActivity)

	// --- reauth: requires session + recent reauth ---
	//
	// Operations that destroy credentials (delete passkey, disable TOTP)
	// or mint long-lived tokens (create/revoke API key) must be gated
	// behind a fresh session reauth. API-key callers bypass the reauth
	// check (the middleware no-ops for them).
	reauth.Add("DELETE /api/auth/totp", s.authH.HandleTOTPDisable)
	reauth.Add("DELETE /api/auth/passkeys/", s.authH.HandleDeletePasskey)
	reauth.Add("POST /api/auth/apikeys", s.authH.HandleGenerateAPIKey)
	reauth.Add("DELETE /api/auth/apikeys/", s.authH.HandleRevokeAPIKey)
	// Regenerating recovery codes invalidates all previous codes and
	// returns fresh plaintext. Treated as a credential-mutation op and
	// guarded the same way as API key creation.
	reauth.Add("POST /api/auth/recovery-codes", s.authH.HandleRegenerateRecoveryCodes)

	// --- admin: requires admin role ---

	// User CRUD.
	admin.Add("GET /api/auth/users", s.authH.HandleListUsers)
	admin.Add("POST /api/auth/users", s.authH.HandleCreateUser)
	admin.Add("DELETE /api/auth/users/", s.authH.HandleDeleteUser)

	// Config CRUD (available even unconfigured; admin is the only party
	// allowed to modify config at runtime).
	admin.Add("GET /api/config", s.configH.HandleGetConfig)
	admin.Add("PUT /api/config", s.configH.HandleSaveConfig)
	admin.Add("POST /api/config", s.configH.HandleSaveConfig)
	admin.Add("POST /api/config/reset", s.configH.HandleResetConfig)
	admin.Add("GET /api/config/parsed", s.queryH.HandleConfigParsed)

	// --- userConfigured: requires session + valid config ---

	// Read-only query endpoints.
	userConfigured.Add("GET /api/search", s.manualH.HandleManualSearch)
	userConfigured.Add("GET /api/search/targets", s.queryH.HandleSearchTargets)
	userConfigured.Add("GET /api/state", s.queryH.HandleState)
	userConfigured.Add("GET /api/state/stats", s.queryH.HandleStateStats)
	userConfigured.Add("GET /api/state/ids", s.fileH.HandleHistoryIDs)
	userConfigured.Add("GET /api/backoff", s.queryH.HandleBackoff)
	userConfigured.Add("GET /api/backoff/prefix", s.queryH.HandleBackoffByPrefix)
	userConfigured.Add("GET /api/locks", s.queryH.HandleLocks)
	userConfigured.Add("GET /api/providers", s.queryH.HandleProviders)
	userConfigured.Add("GET /api/providers/timeout", s.queryH.HandleProviderTimeout)

	// Media browser (proxies Sonarr/Radarr).
	userConfigured.Add("GET /api/media/series", s.mediaH.HandleMediaSeries)
	userConfigured.Add("GET /api/media/movies", s.mediaH.HandleMediaMovies)
	userConfigured.Add("GET /api/media/series/", s.mediaH.HandleMediaEpisodes)

	// Coverage.
	userConfigured.Add("GET /api/coverage/series", s.coverageH.HandleCoverageSeries)
	userConfigured.Add("GET /api/coverage/movies", s.coverageH.HandleCoverageMovies)
	userConfigured.Add("GET /api/coverage/series/", s.coverageH.HandleCoverageDetail)
	userConfigured.Add("GET /api/coverage/scan-state", s.coverageH.HandleScanStates)

	// Write endpoints.
	userConfigured.Add("POST /api/search/download", s.manualH.HandleManualDownload)
	userConfigured.Add("POST /api/search/clear-lock", s.manualH.HandleClearLock)
	userConfigured.Add("POST /api/score", s.queryH.HandleScore)

	// File manager endpoints.
	userConfigured.Add("GET /api/files", s.fileH.HandleListFiles)
	userConfigured.Add("DELETE /api/files", s.fileH.HandleDeleteFile)
	userConfigured.Add("DELETE /api/files/bulk", s.fileH.HandleBulkDeleteFiles)

	// Sync endpoints.
	userConfigured.Add("POST /api/sync/audio", s.syncH.HandleSyncAudio)
	userConfigured.Add("POST /api/sync/offset", s.syncH.HandleSyncOffset)

	// Preview endpoints.
	userConfigured.Add("GET /api/preview/start", s.previewH.HandlePreviewStart)
	userConfigured.Add("GET /api/preview/subtitle", s.previewH.HandlePreviewSubtitle)
	userConfigured.Add("GET /api/preview/video", s.previewH.HandlePreviewVideo)
	userConfigured.Add("GET /api/preview/poster", s.previewH.HandlePreviewPoster)

	// --- adminConfigured: requires admin + valid config ---

	// Scan triggers.
	adminConfigured.Add("POST /api/scan", s.handleScan)
	adminConfigured.Add("POST /api/scan/series/", s.handleScanSeries)
	adminConfigured.Add("POST /api/scan/season/", s.handleScanSeason)
	adminConfigured.Add("POST /api/scan/movie/", s.handleScanMovie)
	adminConfigured.Add("POST /api/scan/item", s.handleScanItem)

	// Provider timeout reset.
	adminConfigured.Add("POST /api/providers/timeout/reset", s.queryH.HandleProviderTimeoutReset)

	// --- Web UI ---
	//
	// Static assets and the SPA shell live behind the same catch-all.
	// Because we serve both authenticated (index.html) and unauthenticated
	// (login.html) shells from one handler, it runs in the `public` group
	// and uses s.authenticator directly to decide which shell to serve.
	// Static assets (.js, .css, .svg, favicon, icons/) are never sensitive
	// and are always served.
	public.Add("/", s.handleUI)
}
