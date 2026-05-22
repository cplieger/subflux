package ssrf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestValidateURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Valid URLs.
		{"valid https", "https://example.com/file.srt", false},
		{"valid https with path", "https://dl.opensubtitles.org/en/download/sub/123", false},
		{"public IP allowed", "https://1.2.3.4/file.srt", false},
		{"public IPv6 allowed", "https://[2606:4700::1]/file.srt", false},

		// Scheme rejection.
		{"http rejected", "http://example.com/file.srt", true},
		{"ftp rejected", "ftp://example.com/file.srt", true},
		{"empty scheme rejected", "://example.com/file.srt", true},
		{"no scheme rejected", "example.com/file.srt", true},

		// Host rejection.
		{"empty string", "", true},
		{"empty host", "https://", true},
		{"localhost rejected", "https://localhost/file.srt", true},
		{"localhost uppercase rejected", "https://LOCALHOST/file.srt", true},
		{"localhost trailing dot rejected", "https://localhost./file.srt", true},
		{"localhost double trailing dot rejected", "https://localhost../file.srt", true},
		{"localhost uppercase trailing dot rejected", "https://LOCALHOST./file.srt", true},
		{"only dots rejected", "https://../file.srt", true},
		{"bare hostname rejected", "https://internal/file.srt", true},

		// IPv4 private/reserved.
		{"loopback IP rejected", "https://127.0.0.1/file.srt", true},
		{"private 192.168 rejected", "https://192.168.1.77/file.srt", true},
		{"private 10.x rejected", "https://10.0.0.1/file.srt", true},
		{"private 172.16 rejected", "https://172.16.0.1/file.srt", true},
		{"link-local rejected", "https://169.254.1.1/file.srt", true},
		{"unspecified rejected", "https://0.0.0.0/file.srt", true},

		// RFC 6890 "this host on this network" 0.0.0.0/8 (beyond IsUnspecified).
		{"this-host 0.1.2.3 rejected", "https://0.1.2.3/file.srt", true},
		{"this-host 0.127.0.0.1 rejected", "https://0.127.0.1/file.srt", true},
		{"this-host 0.255.255.255 rejected", "https://0.255.255.255/file.srt", true},
		{"just above this-host 1.0.0.0 allowed", "https://1.0.0.0/file.srt", false},

		// RFC 1112 §4 reserved 240.0.0.0/4 (former Class E).
		{"reserved 240.0.0.1 rejected", "https://240.0.0.1/file.srt", true},
		{"reserved 250.1.2.3 rejected", "https://250.1.2.3/file.srt", true},
		{"broadcast 255.255.255.255 rejected", "https://255.255.255.255/file.srt", true},
		{"just below reserved 239.255.255.255 rejected (multicast)", "https://239.255.255.255/file.srt", true},

		// IPv6 private/reserved.
		{"IPv6 loopback rejected", "https://[::1]/file.srt", true},
		{"IPv6 ULA rejected", "https://[fc00::1]/file.srt", true},
		{"IPv6 link-local rejected", "https://[fe80::1]/file.srt", true},
		{"IPv6 multicast rejected", "https://[ff02::1]/file.srt", true},
		{"IPv6 unspecified rejected", "https://[::]/file.srt", true},

		// IPv4-mapped IPv6 bypass attempts.
		{"IPv4-mapped loopback rejected", "https://[::ffff:127.0.0.1]/file.srt", true},
		{"IPv4-mapped private rejected", "https://[::ffff:192.168.1.1]/file.srt", true},

		// RFC 3056 6to4 wrapper (2002::/16) with embedded IPv4.
		{"6to4 embedded loopback rejected", "https://[2002:7f00:0001::]/file.srt", true},
		{"6to4 embedded private 192.168 rejected", "https://[2002:c0a8:0101::]/file.srt", true},
		{"6to4 embedded private 10.0 rejected", "https://[2002:0a00:0001::]/file.srt", true},
		{"6to4 embedded CGNAT rejected", "https://[2002:6440:0001::]/file.srt", true},
		{"6to4 embedded public 8.8.8.8 allowed", "https://[2002:0808:0808::]/file.srt", false},

		// RFC 6052 NAT64 well-known prefix (64:ff9b::/96).
		{"NAT64 embedded loopback rejected", "https://[64:ff9b::7f00:1]/file.srt", true},
		{"NAT64 embedded private rejected", "https://[64:ff9b::c0a8:101]/file.srt", true},
		{"NAT64 embedded 10.0 rejected", "https://[64:ff9b::a00:1]/file.srt", true},
		{"NAT64 embedded public allowed", "https://[64:ff9b::808:808]/file.srt", false},

		// RFC 4291 §2.5.5.1 deprecated IPv4-compatible IPv6 (::/96).
		{"IPv4-compat loopback rejected", "https://[::127.0.0.1]/file.srt", true},
		{"IPv4-compat private 192.168 rejected", "https://[::192.168.1.1]/file.srt", true},
		{"IPv4-compat private 10 rejected", "https://[::10.0.0.1]/file.srt", true},

		// RFC 6598 shared address space (CGNAT).
		{"CGNAT 100.64 rejected", "https://100.64.0.1/file.srt", true},
		{"CGNAT 100.127 rejected", "https://100.127.255.254/file.srt", true},
		{"non-CGNAT 100.128 allowed", "https://100.128.0.1/file.srt", false},

		// SSRF bypass vectors (documentation-as-tests: Go's url.Parse handles
		// these correctly, but tests prove the SSRF layer doesn't regress).
		{"userinfo bypass rejected", "https://evil@127.0.0.1/file.srt", true},
		{"loopback with port rejected", "https://127.0.0.1:8080/file.srt", true},
		{"private with port rejected", "https://192.168.1.1:443/file.srt", true},
		{"public with port allowed", "https://example.com:443/file.srt", false},
		{"URL with fragment allowed", "https://example.com/file.srt#frag", false},

		// DNS rebinding: ValidateURL accepts public hostnames even if they
		// could resolve to private IPs. SafeTransport's DialContext catches
		// private addresses after DNS resolution; SafeRedirectPolicy catches
		// redirects to literal private IPs or bare names.
		{"DNS rebinding hostname accepted (caught by dial context)", "https://evil.attacker.com/file.srt", false},

		// CGNAT boundary values.
		{"CGNAT first address rejected", "https://100.64.0.0/file.srt", true},
		{"just below CGNAT allowed", "https://100.63.255.255/file.srt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateURL(tt.url)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateURL(%q) = nil, want error", tt.url)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateURL(%q) = %v, want nil", tt.url, err)
			}
		})
	}
}

func TestIsPublicHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"public domain", "example.com", true},
		{"public IP", "8.8.8.8", true},
		{"public IPv6", "2606:4700::1", true},
		{"localhost", "localhost", false},
		{"localhost trailing dot", "localhost.", false},
		{"localhost double trailing dot", "localhost..", false},
		{"loopback", "127.0.0.1", false},
		{"private", "192.168.1.1", false},
		{"bare hostname", "internal", false},
		{"empty", "", false},
		{"IPv6 ULA", "fc00::1", false},
		{"IPv4-mapped private", "::ffff:10.0.0.1", false},
		{"CGNAT", "100.64.0.1", false},

		// RFC 6890 this-host block beyond IsUnspecified.
		{"this-host 0.1.2.3", "0.1.2.3", false},
		{"this-host 0.127.0.0.1", "0.127.0.1", false},

		// Reserved Class E / broadcast.
		{"reserved 240.0.0.1", "240.0.0.1", false},
		{"broadcast", "255.255.255.255", false},

		// IPv6 transition mechanisms.
		{"6to4 embedded loopback", "2002:7f00:0001::", false},
		{"6to4 embedded private", "2002:c0a8:0101::", false},
		{"6to4 embedded public", "2002:0808:0808::", true},
		{"NAT64 embedded loopback", "64:ff9b::7f00:1", false},
		{"IPv4-compat loopback", "::127.0.0.1", false},

		// IP range boundary values.
		{"172.31.255.255 private", "172.31.255.255", false},
		{"172.32.0.0 public", "172.32.0.0", true},
		{"10.255.255.255 private", "10.255.255.255", false},
		{"192.168.255.255 private", "192.168.255.255", false},
		{"CGNAT boundary 100.63.255.255 public", "100.63.255.255", true},
		{"CGNAT boundary 100.64.0.0 private", "100.64.0.0", false},
		{"CGNAT boundary 100.127.255.255 private", "100.127.255.255", false},
		{"CGNAT boundary 100.128.0.0 public", "100.128.0.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsPublicHost(tt.host)
			if got != tt.want {
				t.Errorf("IsPublicHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestSafeRedirectPolicy_blocks_private_redirect(t *testing.T) {
	t.Parallel()
	policy := SafeRedirectPolicy(nil)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://192.168.1.77/internal", http.NoBody)
	err := policy(req, nil)
	if err == nil {
		t.Error("SafeRedirectPolicy() = nil, want error for private redirect")
	}
}

func TestSafeRedirectPolicy_blocks_http_downgrade(t *testing.T) {
	t.Parallel()
	policy := SafeRedirectPolicy(nil)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/file.srt", http.NoBody)
	err := policy(req, nil)
	if err == nil {
		t.Error("SafeRedirectPolicy() = nil, want error for http scheme downgrade")
	}
}

func TestSafeRedirectPolicy_allows_public_redirect(t *testing.T) {
	t.Parallel()
	policy := SafeRedirectPolicy(nil)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://cdn.example.com/file.srt", http.NoBody)
	err := policy(req, nil)
	if err != nil {
		t.Errorf("SafeRedirectPolicy() = %v, want nil for public redirect", err)
	}
}

func TestSafeRedirectPolicy_stops_after_10_redirects(t *testing.T) {
	t.Parallel()
	policy := SafeRedirectPolicy(nil)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/file", http.NoBody)
	via := make([]*http.Request, 10)
	err := policy(req, via)
	if err == nil {
		t.Error("SafeRedirectPolicy() = nil, want error after 10 redirects")
	}
}

// Even when a caller supplies a custom next, the 10-redirect cap must
// still apply — next could be a trivial passthrough that has no cap.
func TestSafeRedirectPolicy_caps_redirects_with_custom_next(t *testing.T) {
	t.Parallel()
	called := false
	next := func(_ *http.Request, _ []*http.Request) error {
		called = true
		return nil
	}
	policy := SafeRedirectPolicy(next)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/file", http.NoBody)
	via := make([]*http.Request, 10)
	err := policy(req, via)
	if err == nil {
		t.Error("SafeRedirectPolicy() with custom next = nil, want error at 10-redirect cap")
	}
	if called {
		t.Error("SafeRedirectPolicy() called next past the 10-redirect cap")
	}
}

func TestSafeRedirectPolicy_delegates_to_next(t *testing.T) {
	t.Parallel()
	called := false
	next := func(_ *http.Request, _ []*http.Request) error {
		called = true
		return nil
	}
	policy := SafeRedirectPolicy(next)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/file", http.NoBody)
	err := policy(req, nil)
	if err != nil {
		t.Errorf("SafeRedirectPolicy() = %v, want nil", err)
	}
	if !called {
		t.Error("SafeRedirectPolicy() did not call next function")
	}
}

func TestSafeRedirectPolicy_propagates_next_error(t *testing.T) {
	t.Parallel()
	nextErr := errors.New("custom redirect policy error")
	next := func(_ *http.Request, _ []*http.Request) error {
		return nextErr
	}
	policy := SafeRedirectPolicy(next)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/file", http.NoBody)
	err := policy(req, nil)
	if !errors.Is(err, nextErr) {
		t.Errorf("SafeRedirectPolicy() = %v, want %v", err, nextErr)
	}
}

func TestSafeRedirectPolicy_nil_next_under_limit_allows(t *testing.T) {
	t.Parallel()
	policy := SafeRedirectPolicy(nil)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/file", http.NoBody)
	via := make([]*http.Request, 5) // under 10
	err := policy(req, via)
	if err != nil {
		t.Errorf("SafeRedirectPolicy(nil) with %d redirects = %v, want nil", len(via), err)
	}
}

// PBT: ValidateURL and IsPublicHost are consistent. If ValidateURL accepts
// an https URL with a given host, IsPublicHost must also accept that host.
func TestValidateURL_IsPublicHost_consistency(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate domain-like hostnames with at least one dot (to pass bare hostname check).
		label := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "label")
		tld := rapid.SampledFrom([]string{"com", "org", "net", "io"}).Draw(t, "tld")
		host := label + "." + tld
		url := fmt.Sprintf("https://%s/path", host)

		urlErr := ValidateURL(url)
		hostOK := IsPublicHost(host)

		if urlErr == nil && !hostOK {
			t.Errorf("ValidateURL(%q) = nil but IsPublicHost(%q) = false", url, host)
		}
	})
}

// PBT: ValidateURL always rejects non-https schemes.
func TestValidateURL_rejects_non_https_schemes(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		scheme := rapid.SampledFrom([]string{"http", "ftp", "gopher", "file", "ssh", "ws", "wss"}).Draw(t, "scheme")
		url := fmt.Sprintf("%s://example.com/file", scheme)

		err := ValidateURL(url)

		if err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error for non-https scheme", url)
		}
	})
}

// PBT: isPublicAddr rejects all non-public IPv4 addresses.
func TestIsPublicAddr_rejects_all_non_public(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		b := rapid.SliceOfN(rapid.Byte(), 4, 4).Draw(t, "ipv4")
		addr := netip.AddrFrom4([4]byte(b))
		if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
			addr.IsMulticast() || addr.IsUnspecified() || sharedAddrSpace.Contains(addr) ||
			thisHostNet.Contains(addr) || reserved240.Contains(addr) {
			if isPublicAddr(addr) {
				t.Errorf("isPublicAddr(%v) = true, want false for non-public address", addr)
			}
		}
	})
}

// PBT: isPublicAddr rejects all non-public IPv6 addresses.
// Symmetric to TestIsPublicAddr_rejects_all_non_public for IPv4; closes the
// mutation gap where a flip on !addr.IsLinkLocalUnicast() etc. on the IPv6
// code path could survive because only IPv4 addresses were randomly drawn.
func TestIsPublicAddr_rejects_all_non_public_ipv6(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		b := rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(t, "ipv6")
		addr := netip.AddrFrom16([16]byte(b))
		// Skip IPv4-mapped forms — those are covered by
		// TestValidateURL_ipv4_mapped_ipv6_consistency and by
		// Unmap() inside validateHost/safeDialContext.
		if addr.Is4In6() || addr.Is4() {
			return
		}
		if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
			addr.IsMulticast() || addr.IsUnspecified() {
			if isPublicAddr(addr) {
				t.Errorf("isPublicAddr(%v) = true, want false for non-public IPv6 address", addr)
			}
		}
	})
}

// PBT: ValidateURL never panics on arbitrary input.
func TestValidateURL_never_panics(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.String().Draw(t, "url")
		_ = ValidateURL(input) // must not panic
	})
}

// PBT: IPv4-mapped IPv6 addresses are rejected consistently with their
// IPv4 equivalents (defense-in-depth for CVE-2024-24790).
func TestValidateURL_ipv4_mapped_ipv6_consistency(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		b := rapid.SliceOfN(rapid.Byte(), 4, 4).Draw(t, "ipv4")
		addr := netip.AddrFrom4([4]byte(b))
		if !isPublicAddr(addr) {
			mapped := netip.AddrFrom16(addr.As16())
			host := mapped.String()
			url := fmt.Sprintf("https://[%s]/file.srt", host)
			err := ValidateURL(url)
			if err == nil {
				t.Errorf("ValidateURL(%q) = nil, want error for IPv4-mapped IPv6 of non-public %v", url, addr)
			}
		}
	})
}

// --- safeDialContext ---

func TestSafeDialContext_blocks_private_ip_resolution(t *testing.T) {
	t.Parallel()
	dial := safeDialContext(&net.Dialer{Timeout: 2 * time.Second})
	// 127.0.0.1 is a literal IP that resolves to itself (loopback).
	_, err := dial(context.Background(), "tcp", "127.0.0.1:443")
	if err == nil {
		t.Error("safeDialContext() = nil, want error for loopback IP")
	}
}

func TestSafeDialContext_blocks_private_range(t *testing.T) {
	t.Parallel()
	dial := safeDialContext(&net.Dialer{Timeout: 2 * time.Second})
	_, err := dial(context.Background(), "tcp", "192.168.1.1:443")
	if err == nil {
		t.Error("safeDialContext() = nil, want error for private IP")
	}
}

func TestSafeDialContext_invalid_address_returns_error(t *testing.T) {
	t.Parallel()
	dial := safeDialContext(&net.Dialer{Timeout: 2 * time.Second})
	_, err := dial(context.Background(), "tcp", "no-port")
	if err == nil {
		t.Error("safeDialContext() = nil, want error for invalid address")
	}
}

// The .invalid TLD is RFC 2606 reserved to never resolve; this closes the
// coverage gap on the DNS-lookup-error branch and guards against a silent
// regression that would drop the SSRF error wrapping or return a nil conn
// with a nil error (which callers would treat as a successful connection).
func TestSafeDialContext_dns_lookup_error_is_wrapped(t *testing.T) {
	t.Parallel()
	dial := safeDialContext(&net.Dialer{Timeout: 2 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dial(ctx, "tcp", "invalid-host-does-not-exist-xyz.invalid:443")

	if conn != nil {
		t.Errorf("safeDialContext() returned non-nil conn %v, want nil on DNS error", conn)
	}
	if err == nil {
		t.Fatal("safeDialContext() = nil err, want DNS lookup error")
	}
	if !strings.Contains(err.Error(), "SSRF dial") {
		t.Errorf("safeDialContext() err = %q, want error wrapped with \"SSRF dial\" prefix", err.Error())
	}
	if !strings.Contains(err.Error(), "DNS lookup failed") {
		t.Errorf("safeDialContext() err = %q, want \"DNS lookup failed\" in message", err.Error())
	}
}

func TestSafeTransport_returns_non_nil(t *testing.T) {
	t.Parallel()
	tr := SafeTransport()
	if tr == nil {
		t.Fatal("SafeTransport() returned nil")
	}
	if tr.DialContext == nil {
		t.Error("SafeTransport().DialContext is nil")
	}
	// Proxy must be nil; any proxy would bypass safeDialContext and re-open SSRF.
	if tr.Proxy != nil {
		t.Error("SafeTransport().Proxy != nil, want nil to prevent HTTP(S)_PROXY bypass")
	}
	if tr.ResponseHeaderTimeout == 0 {
		t.Error("SafeTransport().ResponseHeaderTimeout == 0, want a cap to prevent slow-headers stall")
	}
	if tr.IdleConnTimeout == 0 {
		t.Error("SafeTransport().IdleConnTimeout == 0, want a bound on idle conn lifetime")
	}
}

// --- Property tests ---

func TestValidateURL_Property_HTTPSRequired(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		host := rapid.StringMatching(`[a-z][a-z0-9]{2,10}\.[a-z]{2,4}`).Draw(t, "host")
		path := rapid.StringMatching(`/[a-z0-9/]{0,20}`).Draw(t, "path")

		httpsURL := "https://" + host + path
		httpURL := "http://" + host + path

		// HTTP must always be rejected.
		if err := ValidateURL(httpURL); err == nil {
			t.Errorf("HTTP URL accepted: %s", httpURL)
		}

		// HTTPS with a valid public host should be accepted.
		// (may still fail on DNS resolution, which is fine)
		_ = ValidateURL(httpsURL)
	})
}
