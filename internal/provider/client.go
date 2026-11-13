package provider

import (
	"cmp"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/ssrf"
	"github.com/cplieger/subflux/internal/buildinfo"
	"github.com/cplieger/subflux/internal/provider/internal/dialhook"
)

const (
	// fetchAttempts and fetchBaseDelay are the GET and HEAD retry budget.
	fetchAttempts  = 3
	fetchBaseDelay = 2 * time.Second

	// httpClientTimeout is above the longest deadline a caller of
	// HTTPClient sets (the anime map refresh's 2 minutes), so a call under a
	// context with none still ends.
	httpClientTimeout = 3 * time.Minute

	// exceptionRequests caps the requests one Search, Fetch or Test of an
	// Exception entry sends; exceptionPause is the least back-off its 403,
	// 429 or 503 gets.
	exceptionRequests = 4
	exceptionPause    = time.Hour
)

// userAgent is what every entry but an Exception with its own UserAgent
// sends (DECISIONS 41, D-UA).
func userAgent() string {
	return "subflux/" + buildinfo.Version() + " (+https://github.com/cplieger/subflux)"
}

// Client is the only HTTP path an adapter has. Build makes it from the
// entry's Hosts.
type Client struct {
	rt           *pinned
	quirk        func(*http.Response, []byte) error
	credentialed bool
	exception    bool
}

// pinned is the Client's round tripper, which http.Client runs on every
// redirect hop: it refuses a request to a host outside hosts before dialing,
// sets the entry's User-Agent, and for an Exception entry holds each call to
// its request cap and each host to its robots.txt.
type pinned struct {
	base        *http.Transport
	robotsHosts map[string]*robotsHost
	ua          string
	hosts       []string
	mu          sync.Mutex
	exception   bool
}

func newClient(e *Entry, credentialed bool) Client {
	t := ssrf.SafeTransport()
	dial := t.DialContext
	t.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if d := dialhook.From(ctx); d != nil {
			return d(ctx, network, addr)
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		c, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tc := tls.Client(c, &tls.Config{ServerName: host, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = c.Close()
			return nil, err
		}
		return tc, nil
	}
	return Client{
		rt: &pinned{
			base: t, hosts: slices.Clone(e.Hosts), ua: cmp.Or(e.UserAgent, userAgent()),
			exception: e.Exception, robotsHosts: map[string]*robotsHost{},
		},
		quirk:        e.Quirk,
		credentialed: credentialed,
		exception:    e.Exception,
	}
}

func (p *pinned) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if req.URL.Scheme != "https" || !slices.Contains(p.hosts, host) {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &Failure{Kind: Permanent, Host: host, Msg: "not one of the provider's hosts"}
	}
	ctx := req.Context()
	req = req.Clone(ctx)
	req.Header.Set("User-Agent", p.ua)
	sent, _ := ctx.Value(sentKey{}).(*atomic.Int64)
	if p.exception && ctx.Value(robotsKey{}) == nil {
		if err := p.admitException(req, sent); err != nil {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, err
		}
	}
	if sent != nil && ctx.Value(robotsKey{}) == nil {
		sent.Add(1)
	}
	return p.base.RoundTrip(req)
}

// admitException is 5.1's request cap, then the host's robots.txt.
func (p *pinned) admitException(req *http.Request, sent *atomic.Int64) error {
	if sent != nil && sent.Load() >= exceptionRequests {
		return &Failure{Kind: Permanent, Host: req.URL.Hostname(), Msg: "the call already sent its 4 requests"}
	}
	return p.robotsGate(req)
}

// sentKey carries the wrapper's per-call request count, so a Skipped
// returned after a request is told from one returned before any.
type sentKey struct{}

// Detached is ctx for work a call starts but does not own, such as the
// anime map's shared refresh: never cancelled with ctx, and its requests are
// not the call's, so they neither count toward an Exception's cap nor turn
// the call's Skipped into a Failure.
func Detached(ctx context.Context) context.Context {
	return context.WithValue(context.WithoutCancel(ctx), sentKey{}, (*atomic.Int64)(nil))
}

func (c Client) httpClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: c.rt, Timeout: timeout, CheckRedirect: ssrf.SafeRedirectPolicy(nil)}
}

func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// HTTPClient is a new http.Client over the Client's pinned round tripper, for
// a library that takes one. Every hop is pinned whatever CheckRedirect the
// caller sets; its Timeout is 3 minutes.
func (c Client) HTTPClient() *http.Client { return c.httpClient(httpClientTimeout) }

// Do sends req and answers a 2xx, or a 304 to a request carrying
// If-None-Match or If-Modified-Since; any other answer is closed and returned
// as a *Failure. A GET or HEAD is retried on a Transient failure, 3 attempts
// from 2 seconds; any other method is sent once, so a quota-spending POST
// never spends twice. An Exception entry's 403, 429 or 503 is RateLimited
// for at least an hour and never retried.
func (c Client) Do(req *http.Request) (*http.Response, error) { return c.do(req, true) }

// DoNoRedirect is Do for a provider whose answers never redirect: a 3xx is
// a Permanent failure, its Location never requested.
func (c Client) DoNoRedirect(req *http.Request) (*http.Response, error) { return c.do(req, false) }

func (c Client) do(req *http.Request, follow bool) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return c.once(req, follow)
	}
	return httpclient.Do(req.Context(), func(ctx context.Context) (*http.Response, error) {
		return c.once(req.Clone(ctx), follow)
	}, httpclient.WithMaxAttempts(fetchAttempts), httpclient.WithBaseDelay(fetchBaseDelay))
}

func (c Client) once(req *http.Request, follow bool) (*http.Response, error) {
	host := req.URL.Hostname()
	hc := c.httpClient(0)
	if !follow {
		hc.CheckRedirect = refuseRedirect
	}
	resp, err := hc.Do(req) // #nosec G704 -- pinned refuses every host outside the entry's Hosts before dialing
	if err != nil {
		return nil, transportFailure(req.Context(), host, err)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return resp, nil
	case !follow && resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != http.StatusNotModified:
		httpclient.DrainClose(resp.Body)
		return nil, &Failure{Kind: Permanent, Host: host, Status: resp.StatusCode, Msg: "answered a redirect, which this provider never sends"}
	case resp.StatusCode == http.StatusNotModified:
		if req.Header.Get("If-None-Match") != "" || req.Header.Get("If-Modified-Since") != "" {
			return resp, nil
		}
		httpclient.DrainClose(resp.Body)
		return nil, &Failure{Kind: Permanent, Host: resp.Request.URL.Hostname(), Status: resp.StatusCode, Msg: "unsolicited 304"}
	}
	if c.exception {
		switch resp.StatusCode {
		case http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable:
			httpclient.DrainClose(resp.Body)
			return nil, exceptionBackoff(resp, resp.Request.URL.Hostname())
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, quirkBody))
	httpclient.DrainClose(resp.Body)
	return nil, classifyAnswer(resp, body, c.quirk, c.credentialed)
}

// exceptionBackoff is an Exception entry's answer to a 403, 429 or 503, its
// robots.txt's 429 included: RateLimited for an hour, or longer when
// Retry-After says so.
func exceptionBackoff(resp *http.Response, host string) *Failure {
	return &Failure{
		Kind: RateLimited, Host: host, Status: resp.StatusCode, Msg: http.StatusText(resp.StatusCode),
		RetryAfter: max(httpclient.ParseRetryAfterResponse(resp), exceptionPause),
	}
}
