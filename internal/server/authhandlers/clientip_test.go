package authhandlers

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return n
}

func reqWith(remoteAddr, xff string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	return req
}

// TestClientIP_trusted_proxy_resolution proves the deployment-agnostic behavior.
// It is NOT parallel: it mutates the package-level trusted set, so it runs in
// the sequential phase (before any t.Parallel() test resumes) and restores the
// trust-nothing default via Cleanup.
func TestClientIP_trusted_proxy_resolution(t *testing.T) {
	t.Cleanup(func() { SetTrustedProxies(nil) })

	// Unconfigured (trust nothing): X-Forwarded-For is ignored and the
	// unspoofable socket peer wins — byte-identical to the pre-change behavior.
	SetTrustedProxies(nil)
	if got := ClientIP(reqWith("10.0.0.9:5555", "203.0.113.7")); got != "10.0.0.9" {
		t.Errorf("unconfigured ClientIP = %q, want 10.0.0.9 (XFF must be ignored)", got)
	}

	// Peer is a trusted proxy: the real client is resolved from a trusted XFF.
	SetTrustedProxies([]*net.IPNet{mustCIDR(t, "10.0.0.0/8")})
	if got := ClientIP(reqWith("10.0.0.9:5555", "203.0.113.7")); got != "203.0.113.7" {
		t.Errorf("trusted-proxy ClientIP = %q, want 203.0.113.7 (real client from XFF)", got)
	}

	// Peer NOT in the trusted set: XFF is still ignored (spoof-safe) even though
	// a trusted set is configured, because the direct peer is untrusted.
	if got := ClientIP(reqWith("198.51.100.4:1234", "203.0.113.7")); got != "198.51.100.4" {
		t.Errorf("untrusted-peer ClientIP = %q, want 198.51.100.4 (XFF ignored)", got)
	}
}

// TestClientIP_reset_restores_default confirms resetting to nil restores the
// trust-nothing default so hot-reload back to no proxies is spoof-safe.
func TestClientIP_reset_restores_default(t *testing.T) {
	t.Cleanup(func() { SetTrustedProxies(nil) })

	SetTrustedProxies([]*net.IPNet{mustCIDR(t, "10.0.0.0/8")})
	SetTrustedProxies(nil)
	if got := ClientIP(reqWith("10.0.0.9:5555", "203.0.113.7")); got != "10.0.0.9" {
		t.Errorf("after reset ClientIP = %q, want 10.0.0.9 (default trust-nothing)", got)
	}
}
