package server

// Authoritative permission model for subflux.
//
// Every HTTP endpoint declares its baseline policy by the group its entry
// names in routes, and this file is the only place that sets it. The
// group's middleware chain is the single enforcement point for that
// baseline; handlers do not repeat it, but may narrow it with object-level
// authorization (activityhandlers' HandleCancelActivity requires admin to
// cancel a full scan).
//
// Each middleware is a clean leaf — it performs exactly one check and
// delegates to next. Groups compose leaves in order. There are no hidden
// chains (e.g. a middleware that secretly calls another middleware
// inside its body). What you see in chain is what runs.
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
//  1. A public handler gets no principal from middleware, so
//     UserFromContext is nil there. One that needs the caller reads the
//     session itself and treats a missing or invalid one as anonymous:
//     handleUI authenticates to choose the app or the login shell, and
//     HandleLogout looks up the session it revokes for the audit record.
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

// authGroup names the middleware chain a route runs behind. The five values
// are the permission model the comment above describes.
type authGroup string

const (
	groupPublic          authGroup = "public"
	groupUser            authGroup = "user"
	groupAdmin           authGroup = "admin"
	groupUserConfigured  authGroup = "userConfigured"
	groupAdminConfigured authGroup = "adminConfigured"
)

// chain returns a group's middleware, applied outside-in: the first wraps
// the second, which wraps the third. A public route runs behind none.
func (s *Server) chain(g authGroup) []middleware {
	switch g {
	case groupPublic:
		return nil
	case groupUser:
		return []middleware{s.requireAuth}
	case groupAdmin:
		return []middleware{s.requireAuth, requireRole(auth.RoleAdmin)}
	case groupUserConfigured:
		return []middleware{s.requireAuth, s.requireConfigured}
	case groupAdminConfigured:
		return []middleware{s.requireAuth, requireRole(auth.RoleAdmin), s.requireConfigured}
	}
	panic("server: route group " + string(g) + " has no middleware chain")
}

// route is one TCP endpoint: its group, its ServeMux pattern (Go 1.22+
// syntax, e.g. "GET /api/search") and its handler. The wirespec consistency
// test checks every group and pattern against the endpoint table
// (internal/wirespec), so the generated client and the permission table
// cannot drift silently.
type route struct {
	handler http.HandlerFunc
	group   authGroup
	pattern string
}

// routeTable collects routes in declaration order.
type routeTable []route

func (t *routeTable) add(group authGroup, pattern string, handler http.HandlerFunc) {
	*t = append(*t, route{handler: handler, group: group, pattern: pattern})
}

// registerRoutes mounts every route of the table on mux, wrapped in its
// group's chain at registration time, so no route can miss a wrapper.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	for _, r := range s.routes() {
		wrapped := r.handler
		for _, mw := range slices.Backward(s.chain(r.group)) {
			wrapped = mw(wrapped)
		}
		mux.HandleFunc(r.pattern, wrapped)
	}
}

// routes is the endpoint table. The group each entry names is its baseline
// policy; a handler may only narrow it with an object-level check.
func (s *Server) routes() []route {
	var t routeTable

	// --- public: no auth ---

	// Health and metrics (probes, Prometheus). Health is at
	// /api/health for cross-app consistency with the cplieger Go
	// apps; metrics stays at /metrics per Prometheus
	// convention. Both live in the public scope (no auth required).
	t.add(groupPublic, "/api/health", s.handleHealth)
	t.add(groupPublic, "/metrics", s.metrics.Handler())

	// Credential-establishing flows. Every endpoint here either creates
	// a session (login, setup, OIDC callback, WebAuthn login finish) or
	// prepares one (OIDC redirect, WebAuthn login begin). The client is
	// by definition unauthenticated when calling these. The availability
	// probe establishes nothing: it is the pre-session read the login page
	// needs to decide what to offer.
	t.add(groupPublic, "GET /api/auth/setup", s.authH.HandleSetupStatus)
	t.add(groupPublic, "POST /api/auth/setup", s.authH.HandleSetupCreate)
	t.add(groupPublic, "POST /api/auth/login", s.authH.HandleLogin)
	t.add(groupPublic, "POST /api/auth/logout", s.authH.HandleLogout)
	t.add(groupPublic, "GET /api/auth/oidc", s.authH.HandleOIDCRedirect)
	t.add(groupPublic, "GET /api/auth/oidc/callback", s.authH.HandleOIDCCallback)
	t.add(groupPublic, "POST /api/auth/oidc/link", s.authH.HandleOIDCLink)
	t.add(groupPublic, "POST /api/auth/webauthn/login/begin", s.authH.HandleWebAuthnLoginBegin)
	t.add(groupPublic, "POST /api/auth/webauthn/login/finish", s.authH.HandleWebAuthnLoginFinish)
	t.add(groupPublic, "GET /api/auth/webauthn/availability", s.authH.HandleWebAuthnAvailability)

	// --- user: requires a session or valid API key ---

	// Server-sent events (always available, config-independent): the stream,
	// the state digest the client reconciles through on wake, and the
	// keepalive acknowledgement that feeds the presence table.
	t.add(groupUser, "GET /api/events", s.activityH.HandleEvents)
	t.add(groupUser, "POST /api/events/sync", s.activityH.HandleEventsSync)
	t.add(groupUser, "POST /api/events/alive", s.activityH.HandleEventsAlive)

	// Self-service account endpoints. These never delete credentials or
	// mint long-lived tokens, so reauth is not required here. Operations
	// that confirm credentials (changePassword, webauthn register finish)
	// carry their own in-band proof and also live here.
	t.add(groupUser, "GET /api/auth/me", s.authH.HandleAuthMe)
	t.add(groupUser, "PUT /api/auth/password", s.authH.HandleChangePassword)
	t.add(groupUser, "PUT /api/auth/profile", s.authH.HandleUpdateProfile)
	t.add(groupUser, "GET /api/auth/passkeys", s.authH.HandleListPasskeys)
	t.add(groupUser, "GET /api/auth/webauthn/signal-data", s.authH.HandleWebAuthnSignalData)
	t.add(groupUser, "POST /api/auth/webauthn/register/begin", s.authH.HandleWebAuthnRegisterBegin)
	t.add(groupUser, "POST /api/auth/webauthn/register/finish", s.authH.HandleWebAuthnRegisterFinish)
	t.add(groupUser, "PUT /api/auth/passkeys/", s.authH.HandleRenamePasskey)

	// Config schema (read-only; available even when unconfigured).
	t.add(groupUser, "GET /api/config/schema", s.configH.HandleConfigSchema)

	// Alerts (read + dismiss).
	t.add(groupUser, "GET /api/alerts", s.stamp(events.SubjectAlerts, nil)(s.activityH.HandleGetAlerts))
	t.add(groupUser, "DELETE /api/alerts", s.activityH.HandleDismissAlert)

	// Activity feed (user-visible history; config-independent).
	t.add(groupUser, "GET /api/activity", s.stamp(events.SubjectActivity, nil)(s.activityH.HandleGetActivity))
	t.add(groupUser, "DELETE /api/activity", s.activityH.HandleDismissActivity)

	// Credential management on your own account (delete own passkey, unlink
	// own OIDC). Destructive actions are confirmed client-side. API-key
	// management is admin-only (see the admin group).
	t.add(groupUser, "DELETE /api/auth/passkeys/", s.authH.HandleDeletePasskey)
	t.add(groupUser, "DELETE /api/auth/oidc/link", s.authH.HandleOIDCUnlink)

	// --- admin: requires admin role ---

	// User CRUD.
	t.add(groupAdmin, "GET /api/auth/users", s.authH.HandleListUsers)
	t.add(groupAdmin, "POST /api/auth/users", s.authH.HandleCreateUser)
	t.add(groupAdmin, "DELETE /api/auth/users/", s.authH.HandleDeleteUser)

	// API key management (admin-only: keys are bearer credentials that carry
	// the owner's role).
	t.add(groupAdmin, "GET /api/auth/apikeys", s.authH.HandleListAPIKeys)
	t.add(groupAdmin, "POST /api/auth/apikeys", s.authH.HandleGenerateAPIKey)
	t.add(groupAdmin, "DELETE /api/auth/apikeys/", s.authH.HandleRevokeAPIKey)

	// Config CRUD (available even unconfigured; admin is the only party
	// allowed to modify config at runtime).
	t.add(groupAdmin, "GET /api/config", s.configH.HandleGetConfig)
	t.add(groupAdmin, "PUT /api/config", s.configH.HandleSaveConfig)
	t.add(groupAdmin, "POST /api/config", s.configH.HandleSaveConfig)
	// Structured (typed JSON) config surface: the settings UI's read/write
	// path; the raw YAML endpoints above remain for hand editing and the API.
	t.add(groupAdmin, "GET /api/config/structured", s.configH.HandleGetConfigStructured)
	t.add(groupAdmin, "PUT /api/config/structured", s.configH.HandleSaveConfigStructured)
	t.add(groupAdmin, "POST /api/config/reset", s.configH.HandleResetConfig)
	t.add(groupAdmin, "GET /api/config/parsed", s.queryH.HandleConfigParsed)
	// Config-time directory probe. The one deliberate path-accepting
	// endpoint (S7 survivor): it probes candidate media_roots while EDITING
	// config, so it cannot be store-resolved by construction. Admin-only —
	// it reveals filesystem structure and belongs to the config-editing
	// role that consumes it.
	t.add(groupAdmin, "POST /api/config/validate-path", confighandlers.HandleValidatePath)
	// Config-time connection probe: the same ping a save runs, on its own, so
	// the settings UI and the setup wizard can answer "is this URL and key
	// right" at the field. `kind` is the config section, so the surface
	// generalizes by growing an arm rather than an endpoint. Admin for the same
	// reason as the rest of the config surface, and unconfigured-tolerant
	// because the wizard is exactly where it is most useful.
	t.add(groupAdmin, "POST /api/config/test-connection", s.configH.HandleTestConnection)

	// --- userConfigured: requires session + valid config ---

	// Read-only query endpoints.
	t.add(groupUserConfigured, "GET /api/search", s.manualH.HandleManualSearch)
	t.add(groupUserConfigured, "GET /api/search/resolve", s.manualH.HandleSearchResolve)
	t.add(groupUserConfigured, "GET /api/search/targets", s.queryH.HandleSearchTargets)
	t.add(groupUserConfigured, "GET /api/state", s.stamp(events.SubjectHistory, nil)(s.queryH.HandleState))
	t.add(groupUserConfigured, "GET /api/state/stats", s.queryH.HandleStateStats)
	t.add(groupUserConfigured, "GET /api/state/ids", s.fileH.HandleHistoryIDs)
	t.add(groupUserConfigured, "GET /api/backoff", s.queryH.HandleBackoff)
	t.add(groupUserConfigured, "GET /api/backoff/prefix", s.queryH.HandleBackoffByPrefix)
	t.add(groupUserConfigured, "GET /api/locks", s.queryH.HandleLocks)
	t.add(groupUserConfigured, "GET /api/providers", s.queryH.HandleProviders)
	t.add(groupUserConfigured, "GET /api/providers/timeout", s.stamp(events.SubjectProviders, nil)(s.queryH.HandleProviderTimeout))

	// Media browser (proxies Sonarr/Radarr).
	t.add(groupUserConfigured, "GET /api/media/series", s.mediaH.HandleMediaSeries)
	t.add(groupUserConfigured, "GET /api/media/movies", s.mediaH.HandleMediaMovies)
	t.add(groupUserConfigured, "GET /api/media/series/", s.mediaH.HandleMediaEpisodes)

	// Coverage. The per-item summary routes coexist with the legacy
	// trailing-slash detail prefix under ServeMux specificity: {tvdbId}/summary
	// is the more specific pattern, everything else under the prefix still
	// reaches the detail handler.
	t.add(groupUserConfigured, "GET /api/coverage/series", s.stamp(events.SubjectSeries, nil)(s.coverageH.HandleCoverageSeries))
	t.add(groupUserConfigured, "GET /api/coverage/movies", s.stamp(events.SubjectMovies, nil)(s.coverageH.HandleCoverageMovies))
	t.add(groupUserConfigured, "GET /api/coverage/series/", s.stamp(events.SubjectDetail, seriesDetailRef)(s.coverageH.HandleCoverageDetail))
	t.add(groupUserConfigured, "GET /api/coverage/scan-state", s.coverageH.HandleScanStates)
	t.add(groupUserConfigured, "GET /api/coverage/series/{tvdbId}/summary", s.coverageH.HandleCoverageSeriesSummary)
	t.add(groupUserConfigured, "GET /api/coverage/movies/{tmdbId}/summary", s.coverageH.HandleCoverageMovieSummary)
	t.add(groupUserConfigured, "GET /api/coverage/movies/{tmdbId}/subs", s.stamp(events.SubjectDetail, movieDetailRef)(s.coverageH.HandleCoverageMovieSubs))

	// Write endpoints.
	t.add(groupUserConfigured, "POST /api/search/download", s.manualH.HandleManualDownload)
	t.add(groupUserConfigured, "POST /api/search/clear-lock", s.manualH.HandleClearLock)
	t.add(groupUserConfigured, "POST /api/score", s.queryH.HandleScore)

	// File manager: listing is available to any user; deletion is admin-only.
	t.add(groupUserConfigured, "GET /api/files", s.fileH.HandleListFiles)
	t.add(groupAdminConfigured, "DELETE /api/files", s.fileH.HandleDeleteFile)
	t.add(groupAdminConfigured, "DELETE /api/files/bulk", s.fileH.HandleBulkDeleteFiles)

	// Sync endpoints.
	t.add(groupUserConfigured, "POST /api/sync/audio", s.syncH.HandleSyncAudio)
	t.add(groupUserConfigured, "POST /api/sync/season", s.syncH.HandleSyncSeason)
	t.add(groupUserConfigured, "POST /api/sync/offset", s.syncH.HandleSyncOffset)
	t.add(groupUserConfigured, "GET /api/sync/jobs", s.stamp(events.SubjectJobs, nil)(s.syncH.HandleSyncJobs))

	// Preview endpoints.
	t.add(groupUserConfigured, "GET /api/preview/start", s.previewH.HandlePreviewStart)
	t.add(groupUserConfigured, "GET /api/preview/subtitle", s.previewH.HandlePreviewSubtitle)
	t.add(groupUserConfigured, "GET /api/preview/video", s.previewH.HandlePreviewVideo)
	t.add(groupUserConfigured, "GET /api/preview/poster", s.previewH.HandlePreviewPoster)

	// --- adminConfigured: requires admin + valid config ---

	// Full-library scan is an admin/maintenance operation.
	t.add(groupAdminConfigured, "POST /api/scan", s.handleScan)
	// Per-item subtitle search/download is a normal user action.
	t.add(groupUserConfigured, "POST /api/scan/series/", s.scanH.HandleScanSeries)
	t.add(groupUserConfigured, "POST /api/scan/season/", s.scanH.HandleScanSeason)
	t.add(groupUserConfigured, "POST /api/scan/movie/", s.scanH.HandleScanMovie)
	t.add(groupUserConfigured, "POST /api/scan/item", s.scanH.HandleScanItem)

	// Explicit graceful stop for running background scans — the repo's first
	// {id} wildcard route (deliberate; Go 1.22 ServeMux). The group is the
	// per-item scan START group; the handler enforces the object-level role
	// (full scans: admin) against the entry's required_role. Distinct from
	// the dismiss idiom (DELETE /api/activity?id=), which never stops
	// running work.
	t.add(groupUserConfigured, "POST /api/activity/{id}/cancel", s.activityH.HandleCancelActivity)

	// Provider timeout reset.
	t.add(groupAdminConfigured, "POST /api/providers/timeout/reset", s.queryH.HandleProviderTimeoutReset)

	// --- Web UI ---
	//
	// Static assets and the SPA shell live behind the same catch-all.
	// Because we serve both authenticated (index.html) and unauthenticated
	// (login.html) shells from one handler, it runs in the `public` group
	// and uses s.authenticator directly to decide which shell to serve.
	// Static assets (.js, .css, .svg, favicon, icons/) are never sensitive
	// and are always served.
	t.add(groupPublic, "/", s.handleUI)
	return t
}
