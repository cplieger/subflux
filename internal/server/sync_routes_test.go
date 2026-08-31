package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The async sync surface (D1) is user-tier and configured-gated like the
// rest of the sync verbs: without a session, every verb answers 401 before
// any handler logic runs.
func TestSyncRoutes_unauthenticated_401(t *testing.T) {
	t.Parallel()
	s, _ := testAuthServer(t)
	s.metrics = noopMetrics{}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	ts := httptest.NewServer(securityChain(mux))
	defer ts.Close()

	for _, probe := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/sync/jobs", ""},
		{http.MethodPost, "/api/sync/audio", `{"media_type":"movie","media_id":"tmdb-1","language":"en"}`},
	} {
		req, err := http.NewRequest(probe.method, ts.URL+probe.path, strings.NewReader(probe.body))
		if err != nil {
			t.Fatalf("build request %s: %v", probe.path, err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", probe.method, probe.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated %s %s: status = %d, want %d",
				probe.method, probe.path, resp.StatusCode, http.StatusUnauthorized)
		}
	}
}
