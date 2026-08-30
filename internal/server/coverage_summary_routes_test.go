package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/server/coveragehandlers"
)

// coverageSummaryPaths are the three per-item coverage routes task A2 adds.
var coverageSummaryPaths = []string{
	"/api/coverage/series/81189/summary",
	"/api/coverage/movies/12345/summary",
	"/api/coverage/movies/12345/subs",
}

func TestCoverageSummaryRoutes_unauthenticated_401(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)
	s.metrics = noopMetrics{}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	ts := httptest.NewServer(securityChain(mux))
	defer ts.Close()

	for _, path := range coverageSummaryPaths {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, http.NoBody)
		if err != nil {
			t.Fatalf("build request %s: %v", path, err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated GET %s: status = %d, want %d",
				path, resp.StatusCode, http.StatusUnauthorized)
		}
	}
}

// TestCoverageSummaryRoutes_coexist_with_detail_prefix drives the REAL route
// table: the {tvdbId}/summary pattern must win ServeMux specificity over the
// legacy trailing-slash detail prefix, while everything else under the prefix
// still reaches the detail handler.
func TestCoverageSummaryRoutes_coexist_with_detail_prefix(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &qhMockStore{})
	s.coverageH = coveragehandlers.NewHandler(coveragehandlers.Deps{
		Store: s.db,
		StateFunc: func() *coveragehandlers.LiveState {
			// No arr clients: the summary handlers answer 404 (nothing
			// exists), which is what distinguishes them from the legacy
			// detail handler's 200 below.
			return &coveragehandlers.LiveState{Cfg: s.state().cfg}
		},
	})
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	serve := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	if rec := serve("/api/coverage/series/81189/summary"); rec.Code != http.StatusNotFound {
		t.Errorf("GET series summary via mux: status = %d, want 404 from the summary handler (nil sonarr)", rec.Code)
	}
	if rec := serve("/api/coverage/series/81189"); rec.Code != http.StatusOK {
		t.Errorf("GET legacy series detail via mux: status = %d, want 200 (prefix route still serves)", rec.Code)
	}
	if rec := serve("/api/coverage/series/abc/summary"); rec.Code != http.StatusBadRequest {
		t.Errorf("GET malformed summary id via mux: status = %d, want 400 (wildcard reaches the handler)", rec.Code)
	}
	if rec := serve("/api/coverage/movies/12345/subs"); rec.Code != http.StatusOK {
		t.Errorf("GET movie subs via mux: status = %d, want 200 store-only", rec.Code)
	} else if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("GET movie subs via mux: body = %q, want empty list", body)
	}
	if rec := serve("/api/coverage/movies/12345/summary"); rec.Code != http.StatusNotFound {
		t.Errorf("GET movie summary via mux: status = %d, want 404 (nil radarr)", rec.Code)
	}
}
