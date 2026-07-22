//go:build functional

package functional

import (
	"fmt"
	"strings"
	"time"
)

// This file ports run.sh's first eight sections: health, auth, config,
// providers, media_browser, coverage, state, files. Assertion messages are
// verbatim from the bash suite; helper semantics live in suite_test.go.

func (s *suite) sectionHealth() {
	s.log("=== Health & Basics ===")
	s.apiGet("/health")
	s.assertStatus("200", "GET /health")
	s.apiGet("/metrics")
	s.assertStatus("200", "GET /metrics")

	// SSE requires auth; without credentials expect 401.
	sseStatus := s.sseProbe(2*time.Second, s.baseURL+"/api/events")
	switch sseStatus {
	case "200":
		s.pass("SSE connects (auth disabled)")
	case "401":
		s.pass("SSE requires auth (HTTP 401)")
	default:
		s.fail(fmt.Sprintf("SSE: HTTP %s", sseStatus))
	}

	ui, _ := s.curlSF(5*time.Second, s.baseURL+"/")
	if strings.Contains(ui, "html") {
		s.pass("GET / serves HTML")
	} else {
		s.fail("GET / no HTML")
	}

	ui, _ = s.curlSF(5*time.Second, s.baseURL+"/library/series")
	if strings.Contains(ui, "html") {
		s.pass("SPA routing works")
	} else {
		s.fail("SPA routing broken")
	}

	// Login page is a separate HTML entry point.
	ui, _ = s.curlSF(5*time.Second, s.baseURL+"/login")
	if strings.Contains(ui, "html") {
		s.pass("GET /login serves HTML")
	} else {
		s.fail("GET /login no HTML")
	}

	// Method not allowed on wrong methods.
	s.apiDelete("/api/health")
	if s.lastStatus == "200" {
		s.pass("Health accepts any method")
	} else {
		s.logf("Health DELETE: HTTP %s", s.lastStatus)
	}
}

func (s *suite) sectionAuth() {
	s.log("=== Authentication Endpoints ===")

	// Setup status.
	setup := s.apiGet("/api/auth/setup")
	s.assertStatus("200", "GET /api/auth/setup")
	s.assertJSONNotEmpty(setup, ".setup_required", "Setup has setup_required field")
	s.assertJSONNotEmpty(setup, ".config_valid", "Setup has config_valid field")

	// Login with wrong credentials (should fail gracefully).
	s.apiPost("/api/auth/login", `{"username":"nonexistent","password":"wrong"}`)
	s.assertStatus("401", "Login: invalid credentials")

	// Login with empty body.
	s.apiPost("/api/auth/login", "{}")
	s.assertStatus("401", "Login: empty body")

	// Logout without session.
	s.apiPost("/api/auth/logout", "")
	s.assertStatus("200", "Logout: no session")

	// Auth me without session (should 401 or return synthetic user if auth
	// disabled).
	me := s.apiGet("/api/auth/me")
	switch s.lastStatus {
	case "200":
		s.assertJSONNotEmpty(me, ".username", "Auth me: has username")
		s.assertJSONNotEmpty(me, ".role", "Auth me: has role")
		s.pass("Auth me: returns user (auth disabled or session valid)")
	case "401":
		s.pass("Auth me: requires auth (HTTP 401)")
	default:
		s.fail(fmt.Sprintf("Auth me: unexpected HTTP %s", s.lastStatus))
	}

	// Passkeys list (requires auth).
	s.apiGet("/api/auth/passkeys")
	if s.lastStatus == "200" || s.lastStatus == "401" {
		s.pass(fmt.Sprintf("List passkeys (HTTP %s)", s.lastStatus))
	} else {
		s.fail(fmt.Sprintf("List passkeys: HTTP %s", s.lastStatus))
	}

	// API keys list (requires auth).
	s.apiGet("/api/auth/apikeys")
	if s.lastStatus == "200" || s.lastStatus == "401" {
		s.pass(fmt.Sprintf("List API keys (HTTP %s)", s.lastStatus))
	} else {
		s.fail(fmt.Sprintf("List API keys: HTTP %s", s.lastStatus))
	}

	// Users list (requires admin).
	s.apiGet("/api/auth/users")
	if s.lastStatus == "200" || s.lastStatus == "401" || s.lastStatus == "403" {
		s.pass(fmt.Sprintf("List users (HTTP %s)", s.lastStatus))
	} else {
		s.fail(fmt.Sprintf("List users: HTTP %s", s.lastStatus))
	}

	// WebAuthn login begin (requires WebAuthn configured).
	s.apiPost("/api/auth/webauthn/login/begin", "")
	if s.lastStatus == "200" || s.lastStatus == "400" || s.lastStatus == "401" {
		s.pass(fmt.Sprintf("WebAuthn login begin (HTTP %s)", s.lastStatus))
	} else {
		s.fail(fmt.Sprintf("WebAuthn login begin: HTTP %s", s.lastStatus))
	}

	// WebAuthn signal data (requires auth).
	s.apiGet("/api/auth/webauthn/signal-data")
	if s.lastStatus == "200" || s.lastStatus == "400" || s.lastStatus == "401" {
		s.pass(fmt.Sprintf("WebAuthn signal data (HTTP %s)", s.lastStatus))
	} else {
		s.fail(fmt.Sprintf("WebAuthn signal data: HTTP %s", s.lastStatus))
	}

	// OIDC redirect (302 when configured, 400 when not).
	s.apiGet("/api/auth/oidc")
	if s.lastStatus == "302" || s.lastStatus == "400" || s.lastStatus == "401" {
		s.pass(fmt.Sprintf("OIDC redirect (HTTP %s)", s.lastStatus))
	} else {
		s.fail(fmt.Sprintf("OIDC redirect: HTTP %s", s.lastStatus))
	}
}

func (s *suite) sectionConfig() {
	s.log("=== Configuration ===")

	cfg := s.apiGet("/api/config")
	s.assertStatus("200", "GET /api/config")
	if cfg != "" {
		s.pass("Config body non-empty")
	} else {
		s.fail("Config body empty")
	}

	schema := s.apiGet("/api/config/schema")
	s.assertStatus("200", "GET /api/config/schema")
	s.assertJSONLen(schema, ".", "gt", 0, "Schema has sections")

	parsed := s.apiGet("/api/config/parsed")
	s.assertStatus("200", "GET /api/config/parsed")
	s.assertJSONNotEmpty(parsed, ".search", "Parsed has search config")
	s.assertJSONNotEmpty(parsed, ".providers", "Parsed has providers map")

	// Auth config in parsed response. (`// empty` also swallows a literal
	// false, matching bash: enabled=false takes the log branch.)
	authEnabled := fieldJSONOrEmpty(parsed, ".auth.enabled")
	if authEnabled != "" {
		s.pass("Parsed has auth config")
	} else {
		s.log("Parsed: no auth section (may be disabled)")
	}

	s.apiPut("/api/config", cfg)
	s.assertStatus("200", "PUT /api/config (unchanged)")

	// POST also accepted for config save.
	s.apiPostText("/api/config", cfg)
	s.assertStatus("200", "POST /api/config (alternate method)")

	s.apiPost("/api/config/reset", "{}")
	if s.lastStatus == "200" || s.lastStatus == "409" {
		s.pass(fmt.Sprintf("POST /api/config/reset (HTTP %s)", s.lastStatus))
	} else {
		s.fail(fmt.Sprintf("POST /api/config/reset: HTTP %s", s.lastStatus))
	}
	resetCfg := s.apiGet("/api/config")
	if resetCfg != cfg {
		s.pass("Reset changed config")
	} else {
		s.skip("Reset same (already default)")
	}

	s.apiPut("/api/config", cfg)
	time.Sleep(time.Second)
}

func (s *suite) sectionProviders() {
	s.log("=== Providers ===")

	// Deterministic list content: enable one credential-free real provider
	// (gestdown) alongside mock. The visible list must then be EXACTLY that
	// provider — mock is hidden test infrastructure, and embedded is no
	// longer an acquisition provider (detector separation).
	s.applyMockConfig("static", `  gestdown:
    enabled: true
    priority: 2`, "", "")

	provs := s.apiGet("/api/providers")
	s.assertStatus("200", "GET /api/providers")
	s.assertJSONLen(provs, ".", "eq", 1, "Visible provider list is exactly the enabled real provider")
	hasGestdown := countFieldEq(provs, "name", "gestdown")
	if n, ok := shellInt(hasGestdown); ok && n == 1 {
		s.pass("Gestdown present in provider list")
	} else {
		s.fail("Gestdown missing from provider list")
	}

	// Mock is internal test infrastructure: functional when enabled, but
	// deliberately hidden from the settings schema and the provider list.
	schemaProvs := s.apiGet("/api/config/schema")
	hasMock := schemaProviderCount(schemaProvs, "mock")
	if n, ok := shellInt(hasMock); ok && n == 0 {
		s.pass("Mock hidden from schema")
	} else {
		s.fail("Mock leaked into schema")
	}
	hasMock = countFieldEq(provs, "name", "mock")
	if n, ok := shellInt(hasMock); ok && n == 0 {
		s.pass("Mock hidden from provider list")
	} else {
		s.fail("Mock leaked into provider list")
	}

	// Embedded is not an acquisition provider (detector separation): absent
	// by construction from the provider list, the provider schema, and the
	// timeout status; the dedicated embedded_subtitles schema section exists.
	hasEmbedded := countFieldEq(provs, "name", "embedded")
	if n, ok := shellInt(hasEmbedded); ok && n == 0 {
		s.pass("Embedded absent from provider list")
	} else {
		s.fail("Embedded leaked into provider list")
	}
	hasEmbedded = schemaProviderCount(schemaProvs, "embedded")
	if n, ok := shellInt(hasEmbedded); ok && n == 0 {
		s.pass("Embedded absent from provider schema")
	} else {
		s.fail("Embedded leaked into provider schema")
	}
	hasEmbedded = countFieldEq(schemaProvs, "key", "embedded_subtitles")
	if n, ok := shellInt(hasEmbedded); ok && n == 1 {
		s.pass("embedded_subtitles schema section present")
	} else {
		s.fail("embedded_subtitles schema section missing")
	}

	timeouts := s.apiGet("/api/providers/timeout")
	s.assertStatus("200", "GET /api/providers/timeout")
	if fieldNotNull(timeouts, ".providers.embedded") != "true" {
		s.pass("Embedded absent from timeout status")
	} else {
		s.fail("Embedded leaked into timeout status")
	}
	s.apiPost("/api/providers/timeout/reset", "")
	s.assertStatus("200", "POST /api/providers/timeout/reset")
}

func (s *suite) sectionMediaBrowser() {
	s.log("=== Media Browser ===")

	series := s.apiGet("/api/media/series")
	s.assertStatus("200", "GET /api/media/series")
	s.assertJSONLen(series, ".", "gt", 0, "Series list non-empty")

	firstID := fieldJSON(series, ".[0].id")
	if firstID != "" && firstID != "null" {
		episodes := s.apiGet(fmt.Sprintf("/api/media/series/%s/episodes", firstID))
		s.assertStatus("200", fmt.Sprintf("GET episodes for series %s", firstID))
		s.assertJSONLen(episodes, ".", "gt", 0, "Episode list non-empty")
	} else {
		s.skip("No series for episode test")
	}

	movies := s.apiGet("/api/media/movies")
	s.assertStatus("200", "GET /api/media/movies")
	s.assertJSONLen(movies, ".", "gt", 0, "Movie list non-empty")
}

func (s *suite) sectionCoverage() {
	s.log("=== Coverage ===")
	s.apiGet("/api/coverage/series")
	s.assertStatus("200", "GET /api/coverage/series")
	s.apiGet("/api/coverage/movies")
	s.assertStatus("200", "GET /api/coverage/movies")
	s.apiGet("/api/coverage/scan-state")
	s.assertStatus("200", "GET /api/coverage/scan-state")

	sid := fieldRawOrEmpty(s.apiGet("/api/media/series"), ".[0].id")
	if sid != "" {
		s.apiGet("/api/coverage/series/" + sid)
		s.assertStatus("200", fmt.Sprintf("Coverage detail series/%s", sid))
	} else {
		s.skip("No series for coverage detail")
	}

	mid := fieldRawOrEmpty(s.apiGet("/api/media/movies"), ".[0].id")
	if mid != "" {
		// Movie coverage is via the movies list endpoint, not a detail
		// endpoint.
		s.log("Movie coverage included in /api/coverage/movies")
	}
}

func (s *suite) sectionState() {
	s.log("=== State & History ===")
	stats := s.apiGet("/api/state/stats")
	s.assertStatus("200", "GET /api/state/stats")
	s.assertJSONNotEmpty(stats, ".total_series", "Stats has total_series")
	s.assertJSONNotEmpty(stats, ".total_movies", "Stats has total_movies")

	s.apiGet("/api/state")
	s.assertStatus("200", "GET /api/state")
	s.apiGet("/api/state?type=episode&limit=5")
	s.assertStatus("200", "State filter: episode")
	s.apiGet("/api/state?type=movie&limit=5")
	s.assertStatus("200", "State filter: movie")
	s.apiGet("/api/state?lang=fr&limit=5")
	s.assertStatus("200", "State filter: lang=fr")
	s.apiGet("/api/state?search=test&limit=5")
	s.assertStatus("200", "State filter: search")
	s.apiGet("/api/state?limit=5&offset=0")
	s.assertStatus("200", "State: pagination")

	s.apiGet("/api/state/ids?type=episode")
	s.assertStatus("200", "History IDs: episode")
	s.apiGet("/api/state/ids?type=movie")
	s.assertStatus("200", "History IDs: movie")

	// Invalid type for history IDs.
	s.apiGet("/api/state/ids?type=invalid")
	if s.lastStatus == "400" {
		s.pass("History IDs: invalid type rejected")
	} else {
		s.logf("History IDs invalid type: HTTP %s", s.lastStatus)
	}

	s.apiGet("/api/activity")
	s.assertStatus("200", "GET /api/activity")

	// Dismiss activity (no-op if nothing to dismiss).
	s.apiDelete("/api/activity")
	if s.lastStatus == "200" || s.lastStatus == "204" {
		s.pass(fmt.Sprintf("DELETE /api/activity (HTTP %s)", s.lastStatus))
	} else {
		s.logf("Dismiss activity: HTTP %s", s.lastStatus)
	}

	s.apiGet("/api/backoff")
	s.assertStatus("200", "GET /api/backoff")
	s.apiGet("/api/backoff/prefix?type=episode&prefix=tvdb-81189-")
	s.assertStatus("200", "Backoff prefix: episode")
	s.apiGet("/api/backoff/prefix?type=movie&prefix=tmdb-27205")
	s.assertStatus("200", "Backoff prefix: movie")
	s.apiGet("/api/locks")
	s.assertStatus("200", "GET /api/locks")
	s.apiGet("/api/alerts")
	s.assertStatus("200", "GET /api/alerts")

	// Dismiss alerts.
	s.apiDelete("/api/alerts")
	s.logf("Dismiss alerts: HTTP %s", s.lastStatus)
}

func (s *suite) sectionFiles() {
	s.log("=== File Management ===")
	s.apiGet("/api/files?media_type=episode&media_id=tvdb-")
	s.assertStatus("200", "Files: episode prefix")
	s.apiGet("/api/files?media_type=movie&media_id=tmdb-")
	s.assertStatus("200", "Files: movie prefix")

	// Bulk delete with empty body.
	s.apiDeleteJSON("/api/files/bulk", "{}")
	s.logf("Bulk delete empty: HTTP %s", s.lastStatus)

	// Single delete with nonexistent path.
	s.apiDeleteJSON("/api/files", `{"path":"/nonexistent/sub.srt","media_type":"movie","media_id":"tmdb-0"}`)
	s.logf("Delete nonexistent: HTTP %s", s.lastStatus)
}
