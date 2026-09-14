//go:build functional

package functional

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This file ports run.sh's first eight sections: health, auth, config,
// providers, media_browser, coverage, state, files. Assertion messages are
// verbatim from the bash suite; helper semantics live in suite_test.go. The
// sse section, added after the port, sits between health and auth.

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

// sseReadWindow is how long each stream read lasts: the handshake (retry,
// hello, the legacy epoch frame) is flushed at connect and the first
// keepalive is 15s out, so two seconds sees everything a connect writes.
const sseReadWindow = 2 * time.Second

var hexEpoch = regexp.MustCompile(`^[0-9a-f]{16}$`)

// frameNamed returns the first frame carrying the event name and whether one
// exists.
func frameNamed(frames []sseFrame, event string) (sseFrame, bool) {
	for _, f := range frames {
		if f.event == event {
			return f, true
		}
	}
	return sseFrame{}, false
}

// sectionSSE pins the server side of the SSE contract: the v3 handshake,
// the header-gated legacy epoch frame, the state digest with a stamped GET
// feeding it, and the keepalive acknowledgement. Arr-free.
func (s *suite) sectionSSE() {
	s.log("=== Server-Sent Events ===")

	// (a) A v3 connect: retry first, then the hello, and no epoch frame.
	status, frames := s.sseRead(sseReadWindow, s.baseURL+"/api/events", map[string]string{"SSE-Wire": "1"})
	if status != "200" {
		s.fail(fmt.Sprintf("SSE v3 connect: expected HTTP 200, got %s", status))
		return
	}
	s.pass("SSE v3 connect (HTTP 200)")
	if len(frames) > 0 && frames[0].retry == "1500" && frames[0].event == "" {
		s.pass("SSE v3: retry: 1500 precedes every frame")
	} else {
		s.fail(fmt.Sprintf("SSE v3: expected a leading retry: 1500 frame, got %+v", frames))
	}
	hello, ok := frameNamed(frames, "sse:hello")
	if !ok {
		s.fail("SSE v3: no sse:hello frame within the read window")
		return
	}
	s.pass("SSE v3: sse:hello received")
	s.assertJSON(hello.data, ".wire", "1", "SSE v3: hello wire == 1")
	s.assertJSON(hello.data, ".verdict", "fresh", "SSE v3: hello verdict == fresh (no cursor presented)")
	s.assertJSON(hello.data, ".keepalive_ms", "15000", "SSE v3: hello keepalive_ms == 15000")
	epoch := fieldRaw(hello.data, ".epoch")
	if hexEpoch.MatchString(epoch) {
		s.pass("SSE v3: hello epoch is 16 hex")
	} else {
		s.fail(fmt.Sprintf("SSE v3: hello epoch %q, want 16 hex", epoch))
	}
	if _, legacyFrame := frameNamed(frames, "epoch"); legacyFrame {
		s.fail("SSE v3: an epoch frame reached a client that sent SSE-Wire")
	} else {
		s.pass("SSE v3: no epoch frame (header-gated overlap)")
	}

	// (b) A legacy connect (no SSE-Wire): exactly one epoch frame after the
	// hello, with no id, boot_id == the hub epoch, and gap following the
	// verdict: true on a cursor-less connect, false over a covered cursor.
	status, frames = s.sseRead(sseReadWindow, s.baseURL+"/api/events", nil)
	if status != "200" {
		s.fail(fmt.Sprintf("SSE legacy connect: expected HTTP 200, got %s", status))
		return
	}
	epochFrames := 0
	for _, f := range frames {
		if f.event == "epoch" {
			epochFrames++
		}
	}
	if epochFrames == 1 {
		s.pass("SSE legacy: exactly one epoch frame")
	} else {
		s.fail(fmt.Sprintf("SSE legacy: %d epoch frames, want 1 (frames: %+v)", epochFrames, frames))
	}
	legacyHello, _ := frameNamed(frames, "sse:hello")
	legacyEpoch, _ := frameNamed(frames, "epoch")
	if legacyEpoch.id == "" {
		s.pass("SSE legacy: epoch frame carries no id")
	} else {
		s.fail(fmt.Sprintf("SSE legacy: epoch frame has id %q, must never become a cursor", legacyEpoch.id))
	}
	s.assertJSON(legacyEpoch.data, ".type", "epoch", "SSE legacy: epoch frame type")
	s.assertJSON(legacyEpoch.data, ".data.boot_id", epoch, "SSE legacy: epoch boot_id == the v3 hello epoch")
	s.assertJSON(legacyEpoch.data, ".data.gap", "true", "SSE legacy: gap == true on a cursor-less connect")
	head := fieldRaw(legacyEpoch.data, ".data.head")
	if _, err := strconv.ParseUint(head, 10, 64); err == nil {
		s.pass("SSE legacy: epoch head is a JSON number")
	} else {
		s.fail(fmt.Sprintf("SSE legacy: epoch head %q is not numeric", head))
	}
	s.assertJSON(legacyEpoch.data, ".data.head", fieldRaw(legacyHello.data, ".head"), "SSE legacy: epoch head == hello head")

	// A legacy reconnect presenting the hub's own head resumes, so gap is
	// false: the frame follows the verdict, not the header.
	_, frames = s.sseRead(sseReadWindow, s.baseURL+"/api/events", map[string]string{"Last-Event-ID": epoch + ":" + head})
	resumedEpoch, ok := frameNamed(frames, "epoch")
	if ok {
		s.assertJSON(resumedEpoch.data, ".data.gap", "false", "SSE legacy: gap == false over a covered cursor")
	} else {
		s.fail("SSE legacy reconnect: no epoch frame within the read window")
	}

	// (c) The digest, fed by a stamped GET. The stamp on GET /api/activity
	// names the version the server holds; a digest presenting it answers
	// unchanged, and once an activity entry moves it answers changed.
	s.apiGet("/api/activity")
	s.assertStatus("200", "GET /api/activity")
	stamp := s.lastHeader.Get("Subject-Stamp")
	s.assertJSON(stamp, ".kind", "activity", "Subject-Stamp kind == activity on GET /api/activity")
	s.assertJSON(stamp, ".epoch", epoch, "Subject-Stamp epoch == the hub epoch")
	version := fieldRaw(stamp, ".version")
	if _, err := strconv.ParseUint(version, 10, 64); err == nil {
		s.pass("Subject-Stamp version is a decimal string")
	} else {
		s.fail(fmt.Sprintf("Subject-Stamp version %q is not a decimal string", version))
	}
	digestBody := fmt.Sprintf(`{"epoch":%q,"subjects":[{"kind":"activity","ref":"","version":%q}]}`, epoch, version)
	r := s.apiPost("/api/events/sync", digestBody)
	s.assertStatus("200", "POST /api/events/sync")
	s.assertJSON(r, ".checked", "1", "Digest: checked == 1")
	s.assertJSON(r, ".must_refetch", "false", "Digest: must_refetch == false for the current epoch")
	s.assertJSON(r, ".epoch", epoch, "Digest: epoch == the hub epoch")
	s.assertJSONLen(r, ".changed", "eq", 0, "Digest: unchanged against the stamped version")

	// Any activity-producing call moves the counter; a manual search is the
	// cheapest arr-free one (its entry starts and ends). The publish is
	// coalesced, so poll the digest until it names activity.
	s.apiGet("/api/search?title=Test+Movie&year=2024&lang=en&type=movie")
	s.assertStatus("200", "GET /api/search (activity producer)")
	deadline := time.Now().Add(15 * time.Second)
	for {
		r = s.apiPost("/api/events/sync", digestBody)
		if fieldRaw(r, ".changed[0].kind") == "activity" || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.assertJSON(r, ".changed[0].kind", "activity", "Digest: changed names activity after an activity entry")
	s.assertJSONNotEmpty(r, ".changed[0].version", "Digest: changed carries the new version")

	r = s.apiPost("/api/events/sync", fmt.Sprintf(`{"epoch":"0000000000000000","subjects":[{"kind":"activity","ref":"","version":%q}]}`, version))
	s.assertStatus("200", "POST /api/events/sync (foreign epoch)")
	s.assertJSON(r, ".must_refetch", "true", "Digest: must_refetch == true for a foreign epoch")

	// (d) The keepalive acknowledgement.
	s.apiPost("/api/events/alive", "")
	s.assertStatus("400", "POST /api/events/alive without SSE-Client")
	s.doRequestHeaders(defaultTimeout, http.MethodPost, s.baseURL+"/api/events/alive", "", "", map[string]string{"SSE-Client": "functional-1"})
	s.assertStatus("204", "POST /api/events/alive with SSE-Client")
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
	// (gestdown) alongside synthetic. The visible list must then be EXACTLY that
	// provider — synthetic is hidden test infrastructure, and embedded is no
	// longer an acquisition provider (detector separation).
	s.applySyntheticConfig("static", `  gestdown:
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

	// Synthetic is internal test infrastructure: functional when enabled, but
	// deliberately hidden from the settings schema and the provider list.
	schemaProvs := s.apiGet("/api/config/schema")
	hasSynthetic := schemaProviderCount(schemaProvs, "synthetic")
	if n, ok := shellInt(hasSynthetic); ok && n == 0 {
		s.pass("Synthetic hidden from schema")
	} else {
		s.fail("Synthetic leaked into schema")
	}
	hasSynthetic = countFieldEq(provs, "name", "synthetic")
	if n, ok := shellInt(hasSynthetic); ok && n == 0 {
		s.pass("Synthetic hidden from provider list")
	} else {
		s.fail("Synthetic leaked into provider list")
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
