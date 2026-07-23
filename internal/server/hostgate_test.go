package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/metrics"
)

// hostGateYAML is a minimal valid config; extra holds appended YAML sections
// (e.g. allowed_hosts).
func hostGateConfig(t *testing.T, extra string) *config.Config {
	t.Helper()
	yaml := `
sonarr:
  url: "http://sonarr:8989"
  api_key: "test"
languages:
  rules:
    - audio: en
      subtitles:
        - code: fr
  default:
    - code: en
providers:
  opensubtitles:
    enabled: true
    settings:
      api_key: "test"
` + extra
	cfg, err := config.LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	return cfg
}

// TestBuildHandler_host_allowlist_gate pins the allowed_hosts DNS-rebinding
// gate end-to-end through the real middleware chain: an active policy 403s a
// disallowed Host with the standard JSON envelope (code host_not_allowed),
// admits a listed Host, and a config hot-reload (applyHostAllowlist) swaps
// the gate without rebuilding the chain.
func TestBuildHandler_host_allowlist_gate(t *testing.T) {
	t.Parallel()
	s := &Server{metrics: metrics.New()}
	s.live.Store(&liveState{cfg: hostGateConfig(t, "allowed_hosts:\n  - subflux.example.com\n")})

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := s.buildHandler(mux)

	do := func(host string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://placeholder/ping", nil)
		r.Host = host
		r.RemoteAddr = "192.0.2.1:4711" // non-loopback peer: no carve-out
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	if rec := do("subflux.example.com"); rec.Code != http.StatusOK {
		t.Errorf("allowed Host status = %d, want 200", rec.Code)
	}

	rec := do("evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disallowed Host status = %d, want 403", rec.Code)
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("403 body %q is not the JSON error envelope: %v", rec.Body.String(), err)
	}
	if envelope.Code != "host_not_allowed" {
		t.Errorf("403 envelope code = %q, want host_not_allowed", envelope.Code)
	}

	// Hot reload: a new live config without allowed_hosts deactivates the
	// gate on the already-built handler.
	s.live.Store(&liveState{cfg: hostGateConfig(t, "")})
	s.applyHostAllowlist()
	if rec := do("evil.example"); rec.Code != http.StatusOK {
		t.Errorf("Host after deactivating reload: status = %d, want 200", rec.Code)
	}

	// And back: re-engaging the gate on reload rejects again.
	s.live.Store(&liveState{cfg: hostGateConfig(t, "allowed_hosts:\n  - other.example.com\n")})
	s.applyHostAllowlist()
	if rec := do("subflux.example.com"); rec.Code != http.StatusForbidden {
		t.Errorf("Host dropped by reload: status = %d, want 403", rec.Code)
	}
}
