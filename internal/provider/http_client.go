package provider

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cplieger/ssrf/v3"
	"github.com/cplieger/subflux/internal/httputil"
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

// maxResponseBodyBytes is the hard transport-level ceiling on any response
// body read through clients built by this package's factories. It is a
// last-resort guard above every per-site cap, so io.ReadAll callers fail
// loudly instead of slurping an unbounded body if a per-site cap is missed.
// The largest legitimate payloads are season-pack archive downloads capped
// at httputil.MaxDownloadBytes (10 MB); the ceiling is 2x that. Per-site
// caps stay authoritative for normal operation.
const maxResponseBodyBytes = 2 * httputil.MaxDownloadBytes

// errResponseBodyTooLarge reports a response body that exceeded
// maxResponseBodyBytes. Surfaced by reads past the ceiling.
var errResponseBodyTooLarge = errors.New("provider: response body exceeds transport cap")

// bodyCapTransport wraps every response body so reads beyond
// maxResponseBodyBytes return errResponseBodyTooLarge. Applied to all
// requests through the factory; no provider is exempt.
type bodyCapTransport struct {
	base http.RoundTripper
}

func (t *bodyCapTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &cappedBody{rc: resp.Body, remaining: maxResponseBodyBytes}
	return resp, nil
}

// cappedBody is a Read-through wrapper that errors once more than
// `remaining` bytes have been read. A body of exactly the ceiling size
// still reads to EOF cleanly; only data beyond the ceiling triggers the error.
type cappedBody struct {
	rc        io.ReadCloser
	remaining int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		// Probe one byte to distinguish EOF-at-ceiling from oversize.
		var one [1]byte
		n, err := b.rc.Read(one[:])
		if n > 0 {
			return 0, errResponseBodyTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.rc.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }

// NewHTTPClient returns a *http.Client preconfigured with the SSRF-safe
// transport, User-Agent injection, redirect policy, the hard response-body
// ceiling (maxResponseBodyBytes), and the given timeout.
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
		Transport:     &bodyCapTransport{base: &userAgentTransport{base: ssrf.SafeTransport(), ua: httputil.UserAgent}},
		CheckRedirect: ssrf.SafeRedirectPolicy(nil),
	}
}

// NewHTTPClientNoClientTimeout returns an SSRF-safe *http.Client without
// a client-level Timeout. Used by providers (e.g. anidb) that fetch large
// bodies and rely on transport-level dial/response-header timeouts plus
// per-request context.WithTimeout, where a client-level Timeout would
// clip mid-stream.
// NewHTTPClientNoClientTimeout builds the shared SSRF-validated provider client
// without a client-level timeout (phase timeouts come from SafeTransport).
// allowedPorts overrides ssrf's default {443} dial-port allowlist — pass it only
// for a known public endpoint on a non-standard port (e.g. the AniDB HTTP API on
// 9001); omit it for the https/443 default.
func NewHTTPClientNoClientTimeout(allowedPorts ...uint16) *http.Client {
	var opts []ssrf.TransportOption
	if len(allowedPorts) > 0 {
		opts = append(opts, ssrf.WithAllowedPorts(allowedPorts...))
	}
	return &http.Client{
		Transport:     &bodyCapTransport{base: &userAgentTransport{base: ssrf.SafeTransport(opts...), ua: httputil.UserAgent}},
		CheckRedirect: ssrf.SafeRedirectPolicy(nil),
	}
}
