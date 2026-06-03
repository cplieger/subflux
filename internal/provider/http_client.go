package provider

import (
	"net/http"
	"time"

	"subflux/internal/httputil"
	"github.com/cplieger/ssrf"
)

// HTTPTimeoutStandard is the default timeout for lightweight provider APIs
// (hdbits, betaseries, gestdown, animetosho, yifysubtitles).
const HTTPTimeoutStandard = 15 * time.Second

// HTTPTimeoutExtended is the timeout for heavier provider APIs that may
// return larger payloads or have slower backends (opensubtitles, subsource, subdl).
const HTTPTimeoutExtended = 30 * time.Second

// userAgentTransport injects the default User-Agent header on every outgoing
// request unless one is already set (allowing per-provider overrides).
type userAgentTransport struct {
	base http.RoundTripper
	ua   string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", t.ua)
	}
	return t.base.RoundTrip(req)
}

// NewHTTPClient returns a *http.Client preconfigured with the SSRF-safe
// transport, User-Agent injection, redirect policy, and the given timeout.
//
// All providers share these defaults: any redirect to a private IP, link-local
// address, or HTTP-downgrade is rejected; private/link-local DNS resolutions
// are also blocked at the transport layer. Per-provider tuning (auth headers,
// caching, rate limiting) layers on top of the *http.Client returned here.
//
// Use this factory instead of constructing &http.Client{} ad-hoc; consistency
// of SSRF/redirect policy across providers is a security property the factory
// enforces.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     &userAgentTransport{base: ssrf.SafeTransport(), ua: httputil.UserAgent},
		CheckRedirect: ssrf.SafeRedirectPolicy(nil),
	}
}

// NewHTTPClientNoClientTimeout returns an SSRF-safe *http.Client without
// a client-level Timeout. Used by providers (e.g. anidb) that fetch large
// bodies and rely on transport-level dial/response-header timeouts plus
// per-request context.WithTimeout, where a client-level Timeout would
// clip mid-stream.
func NewHTTPClientNoClientTimeout() *http.Client {
	return &http.Client{
		Transport:     &userAgentTransport{base: ssrf.SafeTransport(), ua: httputil.UserAgent},
		CheckRedirect: ssrf.SafeRedirectPolicy(nil),
	}
}
