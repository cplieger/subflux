package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/server/authhandlers"
)

// The trusted-proxy set is what makes every client-IP site (audit log, login
// rate limiter, session address) resolve the real client behind a reverse
// proxy, so activation has to push the live config's set into the shared
// resolver. An unconfigured server trusts nothing and reads the socket peer.
func TestApplyTrustedProxies_pushes_the_live_set_into_client_ip_resolution(t *testing.T) {
	// No t.Parallel: the trusted-proxy set is process-global.
	t.Cleanup(func() { authhandlers.SetTrustedProxies(nil) })

	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
		r.RemoteAddr = "10.1.2.3:4711"
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
		return r
	}

	t.Run("configured", func(t *testing.T) {
		s, _ := newActivationTestServer(t)
		s.live.Store(&liveState{
			cfg: testConfig(t, "trusted_proxies:\n  - 10.0.0.0/8\n"),
		})

		s.applyTrustedProxies()

		if got := authhandlers.ClientIP(request()); got != "203.0.113.7" {
			t.Errorf("ClientIP() = %q, want 203.0.113.7 (the forwarded client behind a trusted proxy)", got)
		}
	})

	t.Run("unconfigured", func(t *testing.T) {
		s, _ := newActivationTestServer(t)
		s.live.Store(&liveState{})

		s.applyTrustedProxies()

		if got := authhandlers.ClientIP(request()); got != "10.1.2.3" {
			t.Errorf("ClientIP() = %q, want the socket peer 10.1.2.3 (unconfigured trusts nothing)", got)
		}
	})
}

// A save's first log line is what an operator reads to see WHICH candidate is
// going live; an arr flag that contradicts the config being activated makes a
// dropped arr URL invisible in the record.
func TestHotReload_logs_the_candidate_it_activates(t *testing.T) {
	// No t.Parallel: this test swaps the global slog default logger.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s, _ := newActivationTestServer(t)
	// The base config configures sonarr and never radarr.
	if err := s.hotReload(t.Context(), activationCfg{}.build(t)); err != nil {
		t.Fatalf("hotReload() error = %v, want nil", err)
	}

	// Anchored to the message: activation's own completion line carries
	// same-named attributes derived from the published snapshot instead.
	const want = `msg="hot reload: activating candidate config" providers=1 sonarr=true radarr=false`
	if !bytes.Contains(buf.Bytes(), []byte(want)) {
		t.Errorf("hot reload log missing %q; log was:\n%s", want, buf.String())
	}
}
