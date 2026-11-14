// Package clientaddr resolves a request's client address against
// trusted_proxies, decides whether that client is local (DECISIONS 42)
// and watches for public exposure (DECISIONS 18). It imports no subflux
// package.
package clientaddr

import (
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/cplieger/access"
	"github.com/cplieger/go/httpserver"
)

// Resolver is one trusted-proxy list; its zero value trusts no proxy.
type Resolver struct{ trusted []netip.Prefix }

// New takes settings.Server's trusted-proxy prefixes; nil trusts no proxy.
func New(trusted []netip.Prefix) Resolver {
	return Resolver{trusted: slices.Clone(trusted)}
}

// Client is the one client resolver access is given: the
// httpserver.ResolveClient walk over the trusted list, with its provenance.
func (r Resolver) Client(req *http.Request) access.ClientAddr {
	return addrOf(r.resolve(req))
}

// Local reports whether c is a local client (DECISIONS 42): it reached
// subflux directly (ViaDirect), from a loopback or private peer, an
// IPv4-mapped one read as IPv4, that forwarder does not name as one of the
// host's own forwarders. A forwarded address is never local, whatever
// trusted_proxies lists. A nil forwarder makes no client local.
func Local(c access.ClientAddr, forwarder func(netip.Addr) bool) bool {
	if c.Via != access.ViaDirect || forwarder == nil {
		return false
	}
	return (c.Peer.IsLoopback() || c.Peer.IsPrivate()) && !forwarder(c.Peer)
}

func (r Resolver) resolve(req *http.Request) httpserver.ClientResolution {
	return httpserver.ResolveClient(req, r.trusted...)
}

// https reports r.TLS, or a first X-Forwarded-Proto of https from a peer res
// trusted. Only the exposure detector reads it: the session cookie takes
// access.RequestIsHTTPS, since this is false behind an unlisted TLS proxy.
func https(req *http.Request, res httpserver.ClientResolution) bool {
	return req.TLS != nil || (res.PeerTrusted && forwardedHTTPS(req))
}

func forwardedHTTPS(req *http.Request) bool {
	first, _, _ := strings.Cut(req.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(first), "https")
}

// addrOf maps res onto access's provenance: a trusted peer either forwarded
// a client or none, and an untrusted one either sent forwarding headers or
// is the client.
func addrOf(res httpserver.ClientResolution) access.ClientAddr {
	c := access.ClientAddr{Peer: res.Peer}
	switch {
	case res.Forwarded:
		c.Via, c.Forwarded = access.ViaProxy, res.IP
	case res.PeerTrusted:
		c.Via = access.ViaProxyNoClient
	case res.UntrustedForwarding:
		c.Via = access.ViaUntrustedForwarding
	default:
		c.Via = access.ViaDirect
	}
	return c
}
