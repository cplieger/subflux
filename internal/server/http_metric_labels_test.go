package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/obs"
)

// http_requests_total's labels are the app's one unauthenticated
// unbounded-cardinality surface: the metric hook fires from the access-log
// defer, OUTSIDE every auth gate, and a series once minted is permanent for
// the process lifetime here and in every scraper storing it. These fixtures pin
// the invariant that keeps that safe — no label value comes from the caller —
// against the concrete shape that broke it: registerRoutes mounts a "/"
// catch-all for the SPA, which matches every origin-form path, so an
// "unmatched" collapse keyed on r.Pattern being empty cannot fire for a hostile
// method on a normal URL. Only a request carrying no path to match (CONNECT's
// authority form, OPTIONS *) still reaches it, and there the METHOD is the real
// one — the path is the only label that collapses.
//
// They drive the REAL chain (buildHandler), not the metric hook in isolation,
// because the bound is a property of the WIRING: what makes the labels safe is
// that buildHandler installs webhttp.WithRecordRouteMetric, so webhttp derives
// them. A fixture that hand-wired the option here would still pass if
// buildHandler swapped back to a hook that hands the app the raw request.

// adversarialMethod is a request-line method built only from RFC 9110 tchar
// characters, so net/http accepts it on the wire. It is the token an attacker
// varies to mint one series per request.
const adversarialMethod = "M!#$%&'*+-.^_|~"

// catchAllMux mirrors the shape of subflux's real route table for label
// purposes: method-bearing API patterns, the two method-AGNOSTIC probe
// patterns, and the SPA catch-all that absorbs every origin-form path.
func catchAllMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	noop := func(http.ResponseWriter, *http.Request) {}
	for _, pattern := range []string{
		"GET /api/state",
		"POST /api/scan",
		"/api/health",
		"/metrics",
		"/",
	} {
		mux.HandleFunc(pattern, noop)
	}
	return mux
}

// recordThrough serves one request through the production middleware chain
// (buildHandler around mux, so the real ServeMux assigns r.Pattern in place and
// the real access logger fires the real metric hook) and returns the scraped
// exposition text.
func recordThrough(t *testing.T, s *Server, mux *http.ServeMux, method, target string) string {
	t.Helper()
	s.buildHandler(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, target, nil))

	scrape := httptest.NewRecorder()
	s.metrics.Handler()(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	if !strings.Contains(body, "http_requests_total{") {
		t.Fatalf("no http_requests_total sample recorded: the chain installed no metric hook\n%s", body)
	}
	return body
}

func TestHTTPMetricLabelsRejectCallerChosenMethod(t *testing.T) {
	s := &Server{metrics: obs.New()}

	// The SPA fallthrough: no API route matches, the catch-all does, so a
	// derivation that trusted r.Method reached the label verbatim.
	body := recordThrough(t, s, catchAllMux(t), adversarialMethod, "/anything")

	if strings.Contains(body, adversarialMethod) {
		t.Errorf("caller-chosen method reached a metric label; exposition contains %q", adversarialMethod)
	}
	if !strings.Contains(body, `method="other"`) {
		t.Errorf("a non-standard method must bucket to method=%q; got:\n%s", "other", httpRequestLines(body))
	}
	if !strings.Contains(body, `path="/"`) {
		t.Errorf("the catch-all must record its own pattern as the path label; got:\n%s", httpRequestLines(body))
	}
}

func TestHTTPMetricLabelVocabulary(t *testing.T) {
	tests := map[string]struct {
		method     string
		target     string
		wantLabels []string
		wantAbsent []string
	}{
		"method-bearing pattern records the real method": {
			method:     http.MethodGet,
			target:     "/api/state",
			wantLabels: []string{`method="GET"`, `path="/api/state"`},
		},
		"HEAD against a GET-only pattern records HEAD": {
			// ServeMux routes HEAD to a GET pattern, but the method label is
			// the REQUEST's, not the pattern's, so the metric agrees with the
			// access line for the commonest non-GET probe there is. The path
			// label is still the matched template.
			method:     http.MethodHead,
			target:     "/api/state",
			wantLabels: []string{`method="HEAD"`, `path="/api/state"`},
			wantAbsent: []string{`method="GET"`},
		},
		"method-agnostic pattern still records the real method": {
			// Nothing about "/api/health" naming no method makes the caller's
			// method unbounded: the closed set bounds it on its own.
			method:     http.MethodGet,
			target:     "/api/health",
			wantLabels: []string{`method="GET"`, `path="/api/health"`},
			wantAbsent: []string{`method="other"`},
		},
		"non-standard method on a method-agnostic pattern buckets": {
			method:     adversarialMethod,
			target:     "/api/health",
			wantLabels: []string{`method="other"`, `path="/api/health"`},
		},
		"a lowercase spelling of a standard method buckets": {
			// Methods are case-sensitive (RFC 9110 §9.1), so folding "get"
			// into GET would hand a caller a second spelling of a real series
			// — and then a third, and a fourth. It buckets instead.
			method:     "get",
			target:     "/anything",
			wantLabels: []string{`method="other"`, `path="/"`},
			wantAbsent: []string{`method="GET"`},
		},
		"a method no route accepts falls to the catch-all, not to 405": {
			// The SPA catch-all is method-agnostic, so a PATCH at an API path
			// reaches it rather than net/http's 405. The path label is still a
			// pattern this server registered.
			method:     http.MethodPatch,
			target:     "/api/state",
			wantLabels: []string{`method="PATCH"`, `path="/"`},
			wantAbsent: []string{`path="unmatched"`},
		},
		"unknown path falls to the catch-all, never to unmatched": {
			method:     http.MethodPost,
			target:     "/../%2e%2e/probe",
			wantLabels: []string{`method="POST"`, `path="/"`},
			wantAbsent: []string{`path="unmatched"`},
		},
		"CONNECT authority-form has no path to match, so only the path collapses": {
			// The catch-all cannot absorb this one: an authority-form target
			// leaves r.URL.Path empty, so no pattern matches. The path label
			// collapses to the fixed marker; the method stays the real one, so
			// a probe flood is still visible per method at zero extra
			// cardinality.
			method:     http.MethodConnect,
			target:     "example.com:443",
			wantLabels: []string{`method="CONNECT"`, `path="unmatched"`},
			wantAbsent: []string{`method="unmatched"`},
		},
		"OPTIONS asterisk-form has no path to match either": {
			method:     http.MethodOptions,
			target:     "*",
			wantLabels: []string{`method="OPTIONS"`, `path="unmatched"`},
			wantAbsent: []string{`method="unmatched"`},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := &Server{metrics: obs.New()}
			body := recordThrough(t, s, catchAllMux(t), tc.method, tc.target)
			for _, want := range tc.wantLabels {
				if !strings.Contains(body, want) {
					t.Errorf("missing label %s in exposition:\n%s", want, httpRequestLines(body))
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(body, absent) {
					t.Errorf("unexpected label %s in exposition:\n%s", absent, httpRequestLines(body))
				}
			}
		})
	}
}

// httpRequestLines narrows a full exposition dump to the http_requests_total
// samples, so a failure message shows the labels that were actually recorded.
// Matched as a substring rather than a line prefix: the recorded metric carries
// the registry's "subflux_" namespace, and a prefix match on the bare name
// silently reports nothing.
func httpRequestLines(body string) string {
	var kept []string
	for line := range strings.SplitSeq(body, "\n") {
		if strings.Contains(line, "http_requests_total{") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}
