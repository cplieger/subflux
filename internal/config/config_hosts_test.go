package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadFromBytes_allowed_hosts_unset_is_inactive(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromBytes(context.Background(), []byte(minimalValidYAML()))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	policy := cfg.HostAllowlist()
	if policy.Active() {
		t.Error("HostAllowlist().Active() = true for unset allowed_hosts, want inactive pass-through")
	}
	r := httptest.NewRequest(http.MethodGet, "http://anything.example/", nil)
	if !policy.Allows(r) {
		t.Error("inactive policy must allow any Host")
	}
}

func TestLoadFromBytes_allowed_hosts_gates_exact_hosts(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
allowed_hosts:
  - subflux.example.com
  - 192.168.1.5
`
	cfg, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err != nil {
		t.Fatalf("LoadFromBytes() unexpected error: %v", err)
	}
	policy := cfg.HostAllowlist()
	if !policy.Active() {
		t.Fatal("HostAllowlist().Active() = false, want active")
	}
	if policy.Size() != 2 {
		t.Errorf("HostAllowlist().Size() = %d, want 2", policy.Size())
	}

	cases := []struct {
		host  string
		allow bool
	}{
		{"subflux.example.com", true},
		{"SUBFLUX.EXAMPLE.COM:443", true}, // canonicalized: lowercase, portless
		{"192.168.1.5:8374", true},
		{"evil.example", false},
		{"[subflux.example.com]", false}, // malformed authority is rejected, never repaired
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "http://placeholder/", nil)
		r.Host = tc.host
		r.RemoteAddr = "192.0.2.1:4711" // non-loopback peer: no carve-out
		if got := policy.Allows(r); got != tc.allow {
			t.Errorf("Allows(Host=%q) = %v, want %v", tc.host, got, tc.allow)
		}
	}
}

func TestLoadFromBytes_allowed_hosts_rejects_malformed_entries(t *testing.T) {
	t.Parallel()
	yaml := minimalValidYAML() + `
allowed_hosts:
  - "http://subflux.example.com"
`
	_, err := LoadFromBytes(context.Background(), []byte(yaml))
	if err == nil {
		t.Fatal("LoadFromBytes() = nil error for a URL in allowed_hosts, want validation error")
	}
	if !strings.Contains(err.Error(), "allowed_hosts") {
		t.Errorf("validation error %q does not name allowed_hosts", err)
	}
}
