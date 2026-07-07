package authhandlers

import (
	"net"
	"net/http"
	"sync/atomic"

	"github.com/cplieger/webhttp"
)

// trustedProxies holds the parsed reverse-proxy CIDR set consulted by ClientIP.
// It is updated at server construction and on every config hot-reload via
// SetTrustedProxies, and read locklessly per request. The zero value (nil)
// means "trust nothing": ClientIP then returns the unspoofable socket peer and
// ignores X-Forwarded-For — the spoof-safe default for a directly-exposed
// deployment.
var trustedProxies atomic.Pointer[[]*net.IPNet]

// SetTrustedProxies replaces the trusted reverse-proxy CIDR set used by
// ClientIP. Passing nil or an empty slice restores the trust-nothing default.
// It is safe for concurrent use and the new set takes effect on the next
// request, so a config hot-reload updates client-IP resolution without a
// restart.
func SetTrustedProxies(nets []*net.IPNet) {
	trustedProxies.Store(&nets)
}

// ClientIP extracts the client IP address from the request using the current
// trusted-proxy set. With no trusted proxies configured it returns the
// unspoofable socket-peer host and ignores X-Forwarded-For; when the direct
// peer is a trusted proxy it resolves the real client from a trusted
// X-Forwarded-For chain (spoof-safe, walked right-to-left). It delegates to
// webhttp.ClientIP, the shared spoof-aware resolver — passing no trusted
// ranges is byte-identical to the previous unconfigured behavior.
func ClientIP(r *http.Request) string {
	var trusted []*net.IPNet
	if p := trustedProxies.Load(); p != nil {
		trusted = *p
	}
	return webhttp.ClientIP(r, trusted...)
}
