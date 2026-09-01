package opensubtitles

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/cplieger/runesafe/v2"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/subflux"
)

// doPostDownload performs a rate-limited, authenticated POST to the default
// (non-VIP) baseURL + path. /download must not use the VIP host from
// login — it returns 503 from Varnish. 401 invalidates the cached token.
func (p *Provider) doPostDownload(ctx context.Context, path string,
	body io.Reader,
) (io.ReadCloser, error) {
	if err := p.rateLimit(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+path, body)
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if err := checkStatus(resp); err != nil {
		resp.Body.Close()
		p.invalidateTokenOn401(err)
		return nil, err
	}
	return httpwire.LimitedBody(resp), nil
}

// logQuota records the per-account download quota telemetry returned by
// /download: `requests` (used) and `remaining` sum to the daily cap (10
// free, 1000 VIP). Warns at ≥70% utilization.
func (p *Provider) logQuota(dl *downloadResponse) {
	total := dl.Requests + dl.Remaining
	if total == 0 {
		return
	}
	slog.Info("opensubtitles quota",
		"vip", p.vip,
		"requests_used", dl.Requests,
		"remaining", dl.Remaining,
		"reset_utc", dl.ResetTimeUTC)
	if dl.Remaining*10 < total*3 {
		slog.Warn("opensubtitles quota low",
			"vip", p.vip,
			"remaining", dl.Remaining,
			"total", total,
			"reset_utc", dl.ResetTimeUTC)
	}
}

// --- HTTP Helpers ---

// doGet performs a rate-limited, authenticated GET request to the
// OpenSubtitles API. Returns a 10 MB-capped ReadCloser.
func (p *Provider) doGet(ctx context.Context, path string, params url.Values) (io.ReadCloser, error) {
	if err := p.rateLimit(ctx); err != nil {
		return nil, err
	}
	u := p.serverURL() + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if err := checkStatus(resp); err != nil {
		resp.Body.Close()
		p.invalidateTokenOn401(err)
		return nil, err
	}
	return httpwire.LimitedBody(resp), nil
}

// invalidateTokenOn401 clears the cached token when the server returns 401,
// so the next API call triggers a fresh login instead of repeating the failure.
func (p *Provider) invalidateTokenOn401(err error) {
	authErr, ok := errors.AsType[*subflux.AuthError](err)
	if !ok {
		return
	}
	p.tokenMu.Lock()
	p.token = ""
	p.tokenMu.Unlock()
	slog.Warn("opensubtitles: token invalidated after 401",
		"reason", runesafe.SanitizeSingleLineBounded(authErr.Error(), 256))
}

// doPostUnauthed performs a rate-limited POST request to the default
// OpenSubtitles base URL without requiring authentication.
func (p *Provider) doPostUnauthed(ctx context.Context, path string, body io.Reader) (io.ReadCloser, error) {
	if err := p.rateLimit(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, body)
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if err := checkStatus(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return httpwire.LimitedBody(resp), nil
}

// setHeaders adds the required API key, user agent, and optional
// authorization headers to an outgoing request.
func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set(httpwire.HeaderContentType, httpwire.ContentTypeJSON)
	req.Header.Set("Accept", httpwire.ContentTypeJSON)
	req.Header.Set("Api-Key", p.apiKey)

	p.tokenMu.RLock()
	token := p.token
	p.tokenMu.RUnlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// checkStatus maps OpenSubtitles responses to typed errors. 406 is the
// daily download-quota signal (mapped to RateLimitError, falling back to
// next UTC midnight when no Retry-After is present); everything else
// defers to httpwire.CheckHTTPStatus. 401 surfaces as *subflux.AuthError
// for invalidateTokenOn401 to act on.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode == http.StatusNotAcceptable {
		retryAfter := httpwire.ParseRetryAfter(resp)
		if retryAfter == 0 {
			retryAfter = untilNextUTCMidnight(time.Now())
		}
		return &subflux.RateLimitError{
			Msg:        "download limit exceeded (406)",
			RetryAfter: retryAfter,
		}
	}
	return httpwire.CheckHTTPStatus(resp)
}

// untilNextUTCMidnight returns the duration from now until the next UTC midnight.
func untilNextUTCMidnight(now time.Time) time.Duration {
	n := now.UTC()
	next := time.Date(n.Year(), n.Month(), n.Day()+1, 0, 0, 0, 0, time.UTC)
	d := next.Sub(n)
	if d < time.Second {
		return time.Second
	}
	return d
}
