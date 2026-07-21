package opensubtitles

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/cplieger/runesafe"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// doPostDownload performs a rate-limited, authenticated POST to the default
// (non-VIP) baseURL + path. The /download endpoint must not use the VIP
// server host returned by login — it returns 503 from Varnish. Returns a
// 10 MB-capped ReadCloser. 401 responses invalidate the cached token so
// the next API call triggers a fresh login.
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
	return httputil.LimitedBody(resp), nil
}

// logQuota records the per-account download quota telemetry returned by
// /download. The API reports `requests` (used today) and `remaining`, which
// sum to the daily cap (10 for free, 1000 for VIP). Logs info on every
// call, warn when ≥70% utilization so operators can retune / upgrade
// before the next 406 wall.
func (p *Provider) logQuota(dl *downloadResponse) {
	total := dl.Requests + dl.Remaining
	if total == 0 {
		// No quota information in the response; do not spam INFO.
		return
	}
	slog.Info("opensubtitles quota",
		"vip", p.vip,
		"requests_used", dl.Requests,
		"remaining", dl.Remaining,
		"reset_utc", dl.ResetTimeUTC)
	// Warn when ≤30% of the daily cap remains (mirrors the docker-cron
	// ≥70% utilization convention — see operations.md).
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
	return httputil.LimitedBody(resp), nil
}

// invalidateTokenOn401 clears the cached token when the server returns 401,
// so the next API call triggers a fresh login instead of repeating the failure.
func (p *Provider) invalidateTokenOn401(err error) {
	authErr, ok := errors.AsType[*api.AuthError](err)
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
	return httputil.LimitedBody(resp), nil
}

// setHeaders adds the required API key, user agent, and optional
// authorization headers to an outgoing request.
func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set(httputil.HeaderContentType, httputil.ContentTypeJSON)
	req.Header.Set("Accept", httputil.ContentTypeJSON)
	req.Header.Set("Api-Key", p.apiKey)

	p.tokenMu.RLock()
	token := p.token
	p.tokenMu.RUnlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// checkStatus maps OpenSubtitles HTTP responses to typed errors. 406 is the
// OpenSubtitles-specific daily download-quota signal, mapped to RateLimitError
// with the quota reset (next UTC midnight) as fallback when no Retry-After
// hint is present. Everything else defers to httputil.CheckHTTPStatus, which
// handles 401/403/429 (also with Retry-After) and returns *HTTPStatusError for
// other 4xx/5xx. 401s surface as *api.AuthError, which the call sites'
// invalidateTokenOn401 hook uses to force a fresh login.
func checkStatus(resp *http.Response) error {
	if resp.StatusCode == http.StatusNotAcceptable {
		retryAfter := httputil.ParseRetryAfter(resp)
		if retryAfter == 0 {
			retryAfter = untilNextUTCMidnight(time.Now())
		}
		return &api.RateLimitError{
			Msg:        "download limit exceeded (406)",
			RetryAfter: retryAfter,
		}
	}
	return httputil.CheckHTTPStatus(resp)
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
