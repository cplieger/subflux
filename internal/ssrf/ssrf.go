// Package ssrf provides URL validation to prevent server-side request forgery
// via malicious API responses. All subtitle providers that follow URLs from
// upstream API responses must validate them through this package.
//
// Trust model: DNS resolution is delegated to net.DefaultResolver, which on
// Linux consults /etc/nsswitch.conf → /etc/hosts → the configured resolver
// (Technitium in this deployment). The subflux container runs with
// read_only: true (root-init-proxy-strict) so /etc/hosts cannot be tampered
// with at runtime. safeDialContext resolves a hostname once and hands the
// resolved literal IPs to the dialer; a malicious authoritative resolver
// cannot rebind mid-connection because no second lookup occurs.
package ssrf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const schemeHTTPS = "https"

// ValidateURL checks that a URL uses HTTPS and points to a public host.
// Rejects HTTP (cleartext), non-HTTP schemes, loopback, private, and
// link-local addresses. Hostnames without dots (bare names like
// "localhost" or "internal") are also rejected.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != schemeHTTPS {
		slog.Warn("ssrf blocked", "reason", "scheme", "scheme", u.Scheme)
		return fmt.Errorf("URL scheme must be https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		slog.Warn("ssrf blocked", "reason", "empty_host")
		return errors.New("URL has empty host")
	}
	return validateHost(host)
}

// IsPublicHost checks that a hostname is not a private/loopback/CGNAT address.
// Returns false for localhost, bare hostnames, RFC 1918/link-local IPs,
// and RFC 6598 shared address space.
func IsPublicHost(host string) bool {
	return validateHost(host) == nil
}

// validateHost rejects hostnames that resolve to non-public addresses.
// Checks localhost, bare hostnames (no dots), and all non-public IP ranges
// including IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1).
func validateHost(host string) error {
	if host == "" {
		return errors.New("empty host")
	}

	// Strip all trailing dots (FQDN notation). "localhost." and "localhost.."
	// both resolve identically in DNS, so collapse before comparison to prevent
	// bypasses like "localhost." or "localhost..".
	for strings.HasSuffix(host, ".") {
		host = host[:len(host)-1]
	}
	if host == "" {
		slog.Warn("ssrf blocked", "reason", "empty_host")
		return errors.New("empty host after trimming trailing dots")
	}

	if strings.EqualFold(host, "localhost") {
		slog.Warn("ssrf blocked", "host", host, "reason", "localhost")
		return errors.New("URL points to localhost")
	}

	// Parse as IP first. Handles both IPv4 and IPv6.
	// Unmap converts IPv4-mapped IPv6 (::ffff:x.x.x.x) to plain IPv4
	// so the Is* checks work correctly (defense-in-depth for CVE-2024-24790).
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if !isPublicAddr(addr) {
			slog.Warn("ssrf blocked", "host", host, "reason", "non_public_ip")
			return fmt.Errorf("URL points to non-public IP: %s", host)
		}
		return nil
	}

	// Not an IP; must be a hostname with at least one dot.
	if !strings.Contains(host, ".") {
		slog.Warn("ssrf blocked", "host", host, "reason", "bare_hostname")
		return fmt.Errorf("URL points to bare hostname: %s", host)
	}
	return nil
}

// sharedAddrSpace is the RFC 6598 shared address space (100.64.0.0/10)
// used by carrier-grade NAT. Go's netip.Addr.IsPrivate does not cover
// this range, but it is not globally routable and must be blocked.
var sharedAddrSpace = netip.MustParsePrefix("100.64.0.0/10")

// thisHostNet is RFC 6890 §2.2.2 "this host on this network" (0.0.0.0/8).
// On Linux, connect(2) to any address in 0.0.0.0/8 with no route is
// interpreted as the local host (INADDR_ANY special case), so the entire
// /8 must be blocked, not just the unspecified 0.0.0.0 that IsUnspecified
// catches.
var thisHostNet = netip.MustParsePrefix("0.0.0.0/8")

// reserved240 covers RFC 1112 §4 former Class E (240.0.0.0/4), which is
// reserved for future use and not globally routable. Includes the limited
// broadcast address 255.255.255.255 (RFC 919).
var reserved240 = netip.MustParsePrefix("240.0.0.0/4")

// sixToFour is the IPv6 6to4 well-known prefix (RFC 3056). 2002:xxyy:zzww::
// embeds the IPv4 address xx.yy.zz.ww in the next 32 bits; kernels with a
// 6to4 gateway (sit0) translate these to the embedded IPv4, so the embedded
// address must be re-validated.
var sixToFour = netip.MustParsePrefix("2002::/16")

// nat64Wellknown is the IPv6/IPv4 translation well-known prefix (RFC 6052
// §2.1). 64:ff9b::x.y.z.w routes to the embedded IPv4 via a NAT64 gateway.
var nat64Wellknown = netip.MustParsePrefix("64:ff9b::/96")

// ipv4Compat is the deprecated RFC 4291 §2.5.5.1 IPv4-compatible IPv6
// address format (::a.b.c.d, excluding ::). Unmap does not strip this
// form, so the embedded IPv4 must be re-validated.
var ipv4Compat = netip.MustParsePrefix("::/96")

// isPublicAddr returns true only for globally routable unicast addresses.
// Rejects loopback, private (RFC 1918/RFC 4193), link-local, multicast,
// unspecified, shared (RFC 6598 CGNAT), "this host" (0.0.0.0/8), former
// Class E (240.0.0.0/4 including 255.255.255.255 broadcast), and embedded
// IPv4 inside 6to4 (2002::/16), NAT64 (64:ff9b::/96), or IPv4-compatible
// IPv6 (::/96) wrappers.
func isPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	if addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() ||
		sharedAddrSpace.Contains(addr) ||
		thisHostNet.Contains(addr.Unmap()) ||
		reserved240.Contains(addr.Unmap()) {
		return false
	}

	// IPv6 transition mechanisms embed an IPv4 address in the low 32 bits.
	// Decode and recurse so an IPv4 in one of the blocked ranges is rejected
	// regardless of which IPv6 wrapper is used to express it.
	if sixToFour.Contains(addr) {
		b := addr.As16()
		embedded := netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
		if !isPublicAddr(embedded) {
			return false
		}
	}
	if nat64Wellknown.Contains(addr) {
		b := addr.As16()
		embedded := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		if !isPublicAddr(embedded) {
			return false
		}
	}
	// ::/96 catches the deprecated IPv4-compatible form. Skip the unspecified
	// address (already caught above) so the zero embedded IPv4 doesn't trigger
	// a spurious recursion.
	if ipv4Compat.Contains(addr) && !addr.IsUnspecified() {
		b := addr.As16()
		embedded := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		if !isPublicAddr(embedded) {
			return false
		}
	}
	return true
}

// SafeRedirectPolicy returns an http.Client CheckRedirect function that
// validates each redirect target URL against SSRF rules (scheme, literal
// private IPs, bare hostnames). It does not protect against DNS rebinding
// on its own; DNS-level protection comes from SafeTransport's DialContext
// which re-resolves every redirect host and rejects private IPs before
// connecting. The 10-redirect cap is applied unconditionally, matching
// net/http's default for nil CheckRedirect.
func SafeRedirectPolicy(
	next func(req *http.Request, via []*http.Request) error,
) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if err := ValidateURL(req.URL.String()); err != nil {
			slog.Warn("ssrf redirect blocked", "url", req.URL.Redacted(), "reason", err.Error())
			return fmt.Errorf("redirect blocked (SSRF): %w", err)
		}
		if next != nil {
			return next(req, via)
		}
		return nil
	}
}

// safeDialContext returns a DialContext function that resolves DNS and
// validates all resolved IPs against SSRF rules before connecting.
// This prevents DNS rebinding attacks where a hostname like
// 127.0.0.1.nip.io passes string-based validation but resolves to
// a private address.
//
// Tries resolved IPs sequentially (IPv4/IPv6 order as returned by the
// resolver). If a dial fails, the next IP is attempted. This is simple
// serial fallback, not Happy Eyeballs (RFC 8305) which races families
// concurrently. The outer ctx deadline is checked between attempts so
// a host with many unreachable IPs cannot consume the full cumulative
// per-IP budget.
func safeDialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("SSRF dial: invalid address %q: %w", addr, err)
		}

		// Independent DNS timeout (defense-in-depth if caller passes a
		// background context without a deadline). If ctx already has a
		// tighter deadline, the child inherits it.
		dnsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err := net.DefaultResolver.LookupNetIP(dnsCtx, "ip", host)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("SSRF dial: DNS lookup failed for %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("SSRF dial: no IPs resolved for %q", host)
		}

		// Unmap once up front so validation and dial agree on the literal
		// address. Go's resolver today never returns IPv4-mapped IPv6 for
		// network=="ip", but normalize defensively.
		for i := range ips {
			ips[i] = ips[i].Unmap()
			if !isPublicAddr(ips[i]) {
				slog.Warn("ssrf dial blocked",
					"host", host, "resolved_ip", ips[i].String(), "reason", "non_public_ip")
				return nil, fmt.Errorf("SSRF dial: resolved IP %s for %q is not public", ips[i], host)
			}
		}

		// Try each resolved IP in order until one connects. Short-circuit
		// on ctx cancellation so many unreachable IPs don't consume the
		// full cumulative per-IP timeout budget.
		var lastErr error
		for _, ip := range ips {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("SSRF dial: context cancelled: %w", ctx.Err())
			}
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, fmt.Errorf("SSRF dial: all %d IPs for %q failed: %w", len(ips), host, lastErr)
	}
}

// SafeTransport returns an *http.Transport hardened against SSRF and
// DNS rebinding. Proxy is deliberately nil so configured HTTP(S)_PROXY
// env vars cannot bypass safeDialContext and re-open the SSRF hole.
// Idle/response timeouts prevent a server that TLS-handshakes and then
// stalls from consuming the full outer http.Client.Timeout budget.
func SafeTransport() *http.Transport {
	return &http.Transport{
		// Proxy: nil — never enable; a proxy would bypass safeDialContext.
		DialContext: safeDialContext(&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   4,
		ForceAttemptHTTP2:     true,
	}
}
