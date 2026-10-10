package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/wirespec"
)

// routePatterns maps endpoint names to their routes.go registration pattern
// when it differs from the default "METHOD path": the prefix-style
// registrations whose handlers parse the suffix themselves, and the
// method-less routes.
func routePatterns() map[string]string {
	return map[string]string{
		"health":               "/api/health", // method-less: probes may use any method
		"metrics":              "/metrics",
		"renamePasskey":        "PUT /api/auth/passkeys/",
		"deletePasskey":        "DELETE /api/auth/passkeys/",
		"deleteUser":           "DELETE /api/auth/users/",
		"revokeAPIKey":         "DELETE /api/auth/apikeys/",
		"mediaEpisodes":        "GET /api/media/series/",
		"coverageSeriesDetail": "GET /api/coverage/series/",
		"scanSeries":           "POST /api/scan/series/",
		"scanSeason":           "POST /api/scan/season/",
		"scanMovie":            "POST /api/scan/movie/",
	}
}

// wirespecPattern returns the routes.go registration pattern an endpoint is
// expected to appear under: the explicit override for prefix-style and
// method-less routes, else "METHOD path".
func wirespecPattern(name, method, path string) string {
	if p, ok := routePatterns()[name]; ok {
		return p
	}
	return method + " " + path
}

// TestWirespec_matches_routes is the endpoint-table consistency gate:
// every wirespec endpoint must correspond to a route registration with the
// same auth group, and every registration must be described by the table.
// routes.go stays authoritative for permissions — a mismatch is fixed by
// correcting the TABLE unless the route change itself was intended.
func TestWirespec_matches_routes(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})
	routes := s.routes()

	if len(routes) == 0 {
		t.Fatal("the route table is empty")
	}

	regGroup := make(map[string]string, len(routes))
	for _, r := range routes {
		if prev, dup := regGroup[r.pattern]; dup {
			t.Errorf("pattern %q registered twice (groups %s and %s)", r.pattern, prev, r.group)
		}
		regGroup[r.pattern] = string(r.group)
	}

	eps := wirespec.Endpoints()

	// Table self-consistency: no duplicate method+path.
	seen := map[string]string{}
	for _, e := range eps {
		key := e.Method + " " + e.Path
		if prev, dup := seen[key]; dup {
			t.Errorf("endpoints %s and %s share method+path %q", prev, e.Name, key)
		}
		seen[key] = e.Name
	}

	// Table → routes: every endpoint's pattern is registered in its group.
	matched := map[string]bool{}
	for _, e := range eps {
		pattern := wirespecPattern(e.Name, e.Method, e.Path)
		group, ok := regGroup[pattern]
		if !ok {
			t.Errorf("endpoint %s: no route registration for pattern %q", e.Name, pattern)
			continue
		}
		matched[pattern] = true
		if group != e.AuthGroup {
			t.Errorf("endpoint %s: table auth group %q, but routes.go registers %q in group %q",
				e.Name, e.AuthGroup, pattern, group)
		}
	}

	// Routes → table: every registration is described by an endpoint. The
	// SPA catch-all is the only registration outside the API contract.
	skip := map[string]bool{"/": true}
	for _, r := range routes {
		if skip[r.pattern] || matched[r.pattern] {
			continue
		}
		t.Errorf("route %q (group %s) has no wirespec endpoint entry", r.pattern, r.group)
	}
}

// TestWirespec_query_flags pins each endpoint's Query flag against a full
// snapshot, split by why it is set: the FIVE ?recovery=1-honoring endpoints
// (the two coverage collections, episodes-by-series, and the two per-item
// summaries — A3's transport precondition) and the thirteen shipped endpoints
// that carry it for unrelated query strings. Every other endpoint is
// flag-free — a new Query: true is a deliberate wire-contract change that
// updates this snapshot. The HONORING pin itself is handler-level (the
// coveragehandlers and mediahandlers recovery tests); this is the transport
// half of the split.
func TestWirespec_query_flags(t *testing.T) {
	t.Parallel()
	honoring := map[string]bool{
		"coverageSeries":        true,
		"coverageMovies":        true,
		"mediaEpisodes":         true,
		"coverageSeriesSummary": true,
		"coverageMovieSummary":  true,
	}
	unrelatedQuery := map[string]bool{
		"webauthnLoginBegin":   true,
		"webauthnAvailability": true,
		"dismissAlert":         true,
		"dismissActivity":      true,
		"manualSearch":         true,
		"searchResolve":        true,
		"searchTargets":        true,
		"listState":            true,
		"stateIDs":             true,
		"backoffPrefix":        true,
		"listFiles":            true,
		"previewStart":         true,
		"syncJobs":             true,
	}
	seen := 0
	for _, e := range wirespec.Endpoints() {
		want := honoring[e.Name] || unrelatedQuery[e.Name]
		if e.Query != want {
			t.Errorf("endpoint %s: Query = %v, want %v", e.Name, e.Query, want)
		}
		if want {
			seen++
		}
	}
	if want := len(honoring) + len(unrelatedQuery); seen != want {
		t.Errorf("snapshot names %d Query endpoints, table matched %d — a renamed or removed entry must update this snapshot",
			want, seen)
	}
}

// TestWirespec_routePatterns_are_prefix_consistent pins the override map's
// shape: every override must either be method-less (the deliberately
// any-method routes) or a "METHOD /prefix/" trailing-slash pattern whose
// prefix is a prefix of the endpoint's path with placeholders stripped.
func TestWirespec_routePatterns_are_prefix_consistent(t *testing.T) {
	t.Parallel()
	byName := map[string]struct{ method, path string }{}
	for _, e := range wirespec.Endpoints() {
		byName[e.Name] = struct{ method, path string }{e.Method, e.Path}
	}
	for name, pattern := range routePatterns() {
		ep, ok := byName[name]
		if !ok {
			t.Errorf("routePatterns has entry %q with no matching endpoint", name)
			continue
		}
		if !strings.Contains(pattern, " ") {
			// Method-less pattern: must equal the endpoint path.
			if pattern != ep.path {
				t.Errorf("%s: method-less pattern %q != endpoint path %q", name, pattern, ep.path)
			}
			continue
		}
		var method, prefix string
		if _, err := fmt.Sscanf(pattern, "%s %s", &method, &prefix); err != nil {
			t.Errorf("%s: unparsable pattern %q", name, pattern)
			continue
		}
		if method != ep.method {
			t.Errorf("%s: pattern method %q != endpoint method %q", name, method, ep.method)
		}
		if !strings.HasSuffix(prefix, "/") {
			t.Errorf("%s: override pattern %q is not a trailing-slash prefix", name, pattern)
		}
		if !strings.HasPrefix(ep.path, prefix) {
			t.Errorf("%s: endpoint path %q does not start with pattern prefix %q", name, ep.path, prefix)
		}
	}
}
