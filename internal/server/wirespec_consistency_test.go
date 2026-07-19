package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/wirespec"
)

// wirespecPattern returns the routes.go registration pattern an endpoint is
// expected to appear under: the explicit override for prefix-style and
// method-less routes, else "METHOD path".
func wirespecPattern(name, method, path string) string {
	if p, ok := wirespec.RoutePatterns()[name]; ok {
		return p
	}
	return method + " " + path
}

// TestWirespec_matches_registerRoutes is the endpoint-table consistency gate:
// every wirespec endpoint must correspond to a route registration with the
// same auth group, and every registration must be described by the table.
// routes.go stays authoritative for permissions — a mismatch is fixed by
// correcting the TABLE unless the route change itself was intended.
func TestWirespec_matches_registerRoutes(t *testing.T) {
	t.Parallel()
	s := newTestServer(&qhMockStore{}, &qhMockConfig{})
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	if len(s.routeRegs) == 0 {
		t.Fatal("registerRoutes recorded no registrations")
	}

	regGroup := make(map[string]string, len(s.routeRegs))
	for _, reg := range s.routeRegs {
		if prev, dup := regGroup[reg.Pattern]; dup {
			t.Errorf("pattern %q registered twice (groups %s and %s)", reg.Pattern, prev, reg.Group)
		}
		regGroup[reg.Pattern] = reg.Group
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
	for _, reg := range s.routeRegs {
		if skip[reg.Pattern] || matched[reg.Pattern] {
			continue
		}
		t.Errorf("route %q (group %s) has no wirespec endpoint entry", reg.Pattern, reg.Group)
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
	for name, pattern := range wirespec.RoutePatterns() {
		ep, ok := byName[name]
		if !ok {
			t.Errorf("RoutePatterns has entry %q with no matching endpoint", name)
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
