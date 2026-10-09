package server

// Authoritative permission model for subflux.
//
// Every HTTP endpoint declares its policy by which routeGroup it joins in
// registerRoutes (server.go). The group's middleware chain is the single
// enforcement point for that policy; handlers do not re-check auth or
// roles. This is the only
// file in the server package that encodes per-endpoint permissions.
//
// Each middleware is a clean leaf — it performs exactly one check and
// delegates to next. Groups compose leaves in order. There are no hidden
// chains (e.g. a middleware that secretly calls another middleware
// inside its body). What you see in newRouteGroup is what runs.
//
// Five groups cover every TCP endpoint:
//
//	public           — no auth at all (health, metrics, static assets,
//	                   credential-establishing flows: /api/auth/login,
//	                   /api/auth/setup, OIDC redirect/callback, WebAuthn
//	                   login begin/finish, logout).
//	user             — requireAuth. Handlers in this chain may rely on
//	                   UserFromContext returning a non-nil, enabled user.
//	admin            — requireAuth + requireRole(admin).
//	userConfigured   — requireAuth + requireConfigured. 503 if no valid
//	                   config yet.
//	adminConfigured  — requireAuth + requireRole(admin) + requireConfigured.
//
// The admin bootstrap endpoint is NOT on this mux: it is served exclusively
// on the Unix-socket admin plane (AdminHandler in admin_bootstrap.go), where
// the 0700 socket directory is the security boundary. The TCP mux never
// registers /api/admin/bootstrap, so that path falls through to the SPA
// catch-all like any unknown route.
//
// Group membership rules (enforced at read time, not compile time):
//
//  1. Every public-endpoint handler can access no authenticated state.
//  2. Every non-public handler can read `authhandlers.UserFromContext(r.Context())`
//     without a nil check.
//  3. Every admin handler can skip role checks.
//  4. Configured handlers run only when a valid config is loaded; they may
//     dereference s.state().cfg without checking s.configured.

import (
	"net/http"
	"slices"

	"github.com/cplieger/auth/v6"
	"github.com/cplieger/subflux/internal/server/confighandlers"
	"github.com/cplieger/subflux/internal/server/events"
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
	mux    *http.ServeMux
	record func(reg routeReg)
	name   string
	chain  []middleware
}

// routeReg is one recorded route registration: the ServeMux pattern and the
// route group it was declared in. The wirespec consistency test compares
// these against the endpoint table (internal/wirespec), so the generated
// client and the permission table cannot drift silently.
type routeReg struct {
	//deadset:ignore DS1301 -- Recorded so the route consistency test can check every registration against the wire contract table.
	Group string
	//deadset:ignore DS1301 -- Recorded so the route consistency test can check every registration against the wire contract table.
	Pattern string
}

// newRouteGroup creates a named group bound to `mux` with the given
// middleware chain. Applied outside-in: the first middleware wraps the
// second, which wraps the third, etc. A zero-length chain produces a
// pass-through group. record receives every add for the registration log.
func newRouteGroup(mux *http.ServeMux, name string, record func(routeReg), chain ...middleware) *routeGroup {
	return &routeGroup{mux: mux, name: name, record: record, chain: chain}
}

// add registers a handler on the group's mux under the given pattern,
// wrapping it with every middleware in the group's chain.
//
// Pattern follows Go 1.22+ ServeMux syntax (e.g. "GET /api/search").
func (g *routeGroup) add(pattern string, handler http.HandlerFunc) {
	wrapped := handler
	for _, mw := range slices.Backward(g.chain) {
		wrapped = mw(wrapped)
	}
	g.mux.HandleFunc(pattern, wrapped)
	g.record(routeReg{Group: g.name, Pattern: pattern})
}

// registerRoutes registers all HTTP routes on the given mux. Routes are
// grouped by auth policy — the six groups above are the only place where
// endpoint policy is declared; handlers never re-check auth, roles, or
// reauth.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	s.routeRegs = nil
	record := func(reg routeReg) { s.routeRegs = append(s.routeRegs, reg) }
	public := newRouteGroup(mux, "public", record)
	user := newRouteGroup(mux, "user", record, s.requireAuth)
	admin := newRouteGroup(mux, "admin", record, s.requireAuth, requireRole(auth.RoleAdmin))
	userConfigured := newRouteGroup(mux, "userConfigured", record, s.requireAuth, s.requireConfigured)
	adminConfigured := newRouteGroup(mux, "adminConfigured", record, s.requireAuth, requireRole(auth.RoleAdmin), s.requireConfigured)

	// --- public: no auth ---

	// Health and metrics (probes, Prometheus). Health is at
	// /api/health for cross-app consistency with the cplieger Go
	// apps; metrics stays at /metrics per Prometheus
	// convention. Both live in the public scope (no auth required).
	public.add("/api/health", s.handleHealth)
	public.add("/metrics", s.metrics.Handler())

	// Credential-establishing flows. Every endpoint here either creates
	// a session (login, setup, OIDC callback, WebAuthn login finish) or
	// prepares one (OIDC redirect, WebAuthn login begin). The client is
	// by definition unauthenticated when calling these. The availability
	// probe establishes nothing: it is the pre-session read the login page
	// needs to decide what to offer.
	public.add("GET /api/auth/setup", s.authH.HandleSetupStatus)
	public.add("POST /api/auth/setup", s.authH.HandleSetupCreate)
	public.add("POST /api/auth/login", s.authH.HandleLogin)
	public.add("POST /api/auth/logout", s.authH.HandleLogout)
	public.add("GET /api/auth/oidc", s.authH.HandleOIDCRedirect)
	public.add("GET /api/auth/oidc/callback", s.authH.HandleOIDCCallback)
	public.add("POST /api/auth/oidc/link", s.authH.HandleOIDCLink)
	public.add("POST /api/auth/webauthn/login/begin", s.authH.HandleWebAuthnLoginBegin)
	public.add("POST /api/auth/webauthn/login/finish", s.authH.HandleWebAuthnLoginFinish)
	public.add("GET /api/auth/webauthn/availability", s.authH.HandleWebAuthnAvailability)

	// --- user: requires a session or valid API key ---

	// Server-sent events (always available, config-independent): the stream,
	// the state digest the client reconciles through on wake, and the
	// keepalive acknowledgement that feeds the presence table.
	user.add("GET /api/events", s.activityH.HandleEvents)
	user.add("POST /api/events/sync", s.activityH.HandleEventsSync)
	user.add("POST /api/events/alive", s.activityH.HandleEventsAlive)

	// Self-service account endpoints. These never delete credentials or
	// mint long-lived tokens, so reauth is not required here. Operations
	// that confirm credentials (changePassword, webauthn register finish)
	// carry their own in-band proof and also live here.
	user.add("GET /api/auth/me", s.authH.HandleAuthMe)
	user.add("PUT /api/auth/password", s.authH.HandleChangePassword)
	user.add("PUT /api/auth/profile", s.authH.HandleUpdateProfile)
	user.add("GET /api/auth/passkeys", s.authH.HandleListPasskeys)
	user.add("GET /api/auth/webauthn/signal-data", s.authH.HandleWebAuthnSignalData)
	user.add("POST /api/auth/webauthn/register/begin", s.authH.HandleWebAuthnRegisterBegin)
	user.add("POST /api/auth/webauthn/register/finish", s.authH.HandleWebAuthnRegisterFinish)
	user.add("PUT /api/auth/passkeys/", s.authH.HandleRenamePasskey)

	// Config schema (read-only; available even when unconfigured).
	user.add("GET /api/config/schema", s.configH.HandleConfigSchema)

	// Alerts (read + dismiss).
	user.add("GET /api/alerts", s.stamp(events.SubjectAlerts, nil)(s.activityH.HandleGetAlerts))
	user.add("DELETE /api/alerts", s.activityH.HandleDismissAlert)

	// Activity feed (user-visible history; config-independent).
	user.add("GET /api/activity", s.stamp(events.SubjectActivity, nil)(s.activityH.HandleGetActivity))
	user.add("DELETE /api/activity", s.activityH.HandleDismissActivity)

	// Credential management on your own account (delete own passkey, unlink
	// own OIDC). Destructive actions are confirmed client-side. API-key
	// management is admin-only (see the admin group).
	user.add("DELETE /api/auth/passkeys/", s.authH.HandleDeletePasskey)
	user.add("DELETE /api/auth/oidc/link", s.authH.HandleOIDCUnlink)

	// --- admin: requires admin role ---

	// User CRUD.
	admin.add("GET /api/auth/users", s.authH.HandleListUsers)
	admin.add("POST /api/auth/users", s.authH.HandleCreateUser)
	admin.add("DELETE /api/auth/users/", s.authH.HandleDeleteUser)

	// API key management (admin-only: keys are bearer credentials that carry
	// the owner's role).
	admin.add("GET /api/auth/apikeys", s.authH.HandleListAPIKeys)
	admin.add("POST /api/auth/apikeys", s.authH.HandleGenerateAPIKey)
	admin.add("DELETE /api/auth/apikeys/", s.authH.HandleRevokeAPIKey)

	// Config CRUD (available even unconfigured; admin is the only party
	// allowed to modify config at runtime).
	admin.add("GET /api/config", s.configH.HandleGetConfig)
	admin.add("PUT /api/config", s.configH.HandleSaveConfig)
	admin.add("POST /api/config", s.configH.HandleSaveConfig)
	// Structured (typed JSON) config surface: the settings UI's read/write
	// path; the raw YAML endpoints above remain for hand editing and the API.
	admin.add("GET /api/config/structured", s.configH.HandleGetConfigStructured)
	admin.add("PUT /api/config/structured", s.configH.HandleSaveConfigStructured)
	admin.add("POST /api/config/reset", s.configH.HandleResetConfig)
	admin.add("GET /api/config/parsed", s.queryH.HandleConfigParsed)
	// Config-time directory probe. The one deliberate path-accepting
	// endpoint (S7 survivor): it probes candidate media_roots while EDITING
	// config, so it cannot be store-resolved by construction. Admin-only —
	// it reveals filesystem structure and belongs to the config-editing
	// role that consumes it.
	admin.add("POST /api/config/validate-path", confighandlers.HandleValidatePath)
	// Config-time connection probe: the same ping a save runs, on its own, so
	// the settings UI and the setup wizard can answer "is this URL and key
	// right" at the field. `kind` is the config section, so the surface
	// generalizes by growing an arm rather than an endpoint. Admin for the same
	// reason as the rest of the config surface, and unconfigured-tolerant
	// because the wizard is exactly where it is most useful.
	admin.add("POST /api/config/test-connection", s.configH.HandleTestConnection)

	// --- userConfigured: requires session + valid config ---

	// Read-only query endpoints.
	userConfigured.add("GET /api/search", s.manualH.HandleManualSearch)
	userConfigured.add("GET /api/search/resolve", s.manualH.HandleSearchResolve)
	userConfigured.add("GET /api/search/targets", s.queryH.HandleSearchTargets)
	userConfigured.add("GET /api/state", s.stamp(events.SubjectHistory, nil)(s.queryH.HandleState))
	userConfigured.add("GET /api/state/stats", s.queryH.HandleStateStats)
	userConfigured.add("GET /api/state/ids", s.fileH.HandleHistoryIDs)
	userConfigured.add("GET /api/backoff", s.queryH.HandleBackoff)
	userConfigured.add("GET /api/backoff/prefix", s.queryH.HandleBackoffByPrefix)
	userConfigured.add("GET /api/locks", s.queryH.HandleLocks)
	userConfigured.add("GET /api/providers", s.queryH.HandleProviders)
	userConfigured.add("GET /api/providers/timeout", s.stamp(events.SubjectProviders, nil)(s.queryH.HandleProviderTimeout))

	// Media browser (proxies Sonarr/Radarr).
	userConfigured.add("GET /api/media/series", s.mediaH.HandleMediaSeries)
	userConfigured.add("GET /api/media/movies", s.mediaH.HandleMediaMovies)
	userConfigured.add("GET /api/media/series/", s.mediaH.HandleMediaEpisodes)

	// Coverage. The per-item summary routes coexist with the legacy
	// trailing-slash detail prefix under ServeMux specificity: {tvdbId}/summary
	// is the more specific pattern, everything else under the prefix still
	// reaches the detail handler.
	userConfigured.add("GET /api/coverage/series", s.stamp(events.SubjectSeries, nil)(s.coverageH.HandleCoverageSeries))
	userConfigured.add("GET /api/coverage/movies", s.stamp(events.SubjectMovies, nil)(s.coverageH.HandleCoverageMovies))
	userConfigured.add("GET /api/coverage/series/", s.stamp(events.SubjectDetail, seriesDetailRef)(s.coverageH.HandleCoverageDetail))
	userConfigured.add("GET /api/coverage/scan-state", s.coverageH.HandleScanStates)
	userConfigured.add("GET /api/coverage/series/{tvdbId}/summary", s.coverageH.HandleCoverageSeriesSummary)
	userConfigured.add("GET /api/coverage/movies/{tmdbId}/summary", s.coverageH.HandleCoverageMovieSummary)
	userConfigured.add("GET /api/coverage/movies/{tmdbId}/subs", s.stamp(events.SubjectDetail, movieDetailRef)(s.coverageH.HandleCoverageMovieSubs))

	// Write endpoints.
	userConfigured.add("POST /api/search/download", s.manualH.HandleManualDownload)
	userConfigured.add("POST /api/search/clear-lock", s.manualH.HandleClearLock)
	userConfigured.add("POST /api/score", s.queryH.HandleScore)

	// File manager: listing is available to any user; deletion is admin-only.
	userConfigured.add("GET /api/files", s.fileH.HandleListFiles)
	adminConfigured.add("DELETE /api/files", s.fileH.HandleDeleteFile)
	adminConfigured.add("DELETE /api/files/bulk", s.fileH.HandleBulkDeleteFiles)

	// Sync endpoints.
	userConfigured.add("POST /api/sync/audio", s.syncH.HandleSyncAudio)
	userConfigured.add("POST /api/sync/season", s.syncH.HandleSyncSeason)
	userConfigured.add("POST /api/sync/offset", s.syncH.HandleSyncOffset)
	userConfigured.add("GET /api/sync/jobs", s.stamp(events.SubjectJobs, nil)(s.syncH.HandleSyncJobs))

	// Preview endpoints.
	userConfigured.add("GET /api/preview/start", s.previewH.HandlePreviewStart)
	userConfigured.add("GET /api/preview/subtitle", s.previewH.HandlePreviewSubtitle)
	userConfigured.add("GET /api/preview/video", s.previewH.HandlePreviewVideo)
	userConfigured.add("GET /api/preview/poster", s.previewH.HandlePreviewPoster)

	// --- adminConfigured: requires admin + valid config ---

	// Full-library scan is an admin/maintenance operation.
	adminConfigured.add("POST /api/scan", s.handleScan)
	// Per-item subtitle search/download is a normal user action.
	userConfigured.add("POST /api/scan/series/", s.scanH.HandleScanSeries)
	userConfigured.add("POST /api/scan/season/", s.scanH.HandleScanSeason)
	userConfigured.add("POST /api/scan/movie/", s.scanH.HandleScanMovie)
	userConfigured.add("POST /api/scan/item", s.scanH.HandleScanItem)

	// Explicit graceful stop for running background scans — the repo's first
	// {id} wildcard route (deliberate; Go 1.22 ServeMux). The group is the
	// per-item scan START group; the handler enforces the object-level role
	// (full scans: admin) against the entry's required_role. Distinct from
	// the dismiss idiom (DELETE /api/activity?id=), which never stops
	// running work.
	userConfigured.add("POST /api/activity/{id}/cancel", s.activityH.HandleCancelActivity)

	// Provider timeout reset.
	adminConfigured.add("POST /api/providers/timeout/reset", s.queryH.HandleProviderTimeoutReset)

	// --- Web UI ---
	//
	// Static assets and the SPA shell live behind the same catch-all.
	// Because we serve both authenticated (index.html) and unauthenticated
	// (login.html) shells from one handler, it runs in the `public` group
	// and uses s.authenticator directly to decide which shell to serve.
	// Static assets (.js, .css, .svg, favicon, icons/) are never sensitive
	// and are always served.
	public.add("/", s.handleUI)
}
