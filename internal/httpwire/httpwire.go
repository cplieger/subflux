// Package httpwire holds subflux's own HTTP wire policy: the byte bounds it
// will read from a peer, and the vocabulary it classifies a peer's response
// with. Behavioural logic is delegated to github.com/cplieger/go/httpclient; what
// stays here is application-specific — the caps, the User-Agent, and the thin
// adapters bridging httpclient error types to the internal subflux.* errors.
//
// Named for the wire rather than as an httputil: the bounds and the
// classification are one concern (every export is about reading a response
// safely — the caps bound it, CheckHTTPStatus/IsTransient/ParseRetryAfter/
// LimitedBody judge it), so splitting them would put a cap in one package and
// the reader that applies it in another. Most consumers are providers, but
// search, manualops and synchandlers read the caps too, which is why the name
// is not provider-specific.
package httpwire

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/subflux"
)

// UserAgent is the default User-Agent header sent by all providers.
const UserAgent = "Subflux/1.0"

// HeaderContentType is the canonical HTTP header name for content type.
const HeaderContentType = "Content-Type"

// ContentTypeJSON is the MIME type for JSON request/response bodies.
const ContentTypeJSON = "application/json"

// MaxDownloadBytes is the maximum response body size for subtitle/archive
// downloads (10 MB).
const MaxDownloadBytes = 10 << 20

// MaxSingleSubtitleBytes is the maximum size for a single subtitle file
// download (5 MB).
const MaxSingleSubtitleBytes = 5 << 20

// MaxJSONResponseBytes is the maximum size for a JSON API response body (5 MB).
const MaxJSONResponseBytes = 5 << 20

// MaxSearchResponseBytes is the maximum size for a search/lookup JSON response (1 MB).
const MaxSearchResponseBytes = 1 << 20

// MaxListResponseBytes is the maximum size for a list/detail JSON response (2 MB).
const MaxListResponseBytes = 2 << 20

// MaxErrorBodyBytes is the maximum size for reading error response bodies (1 MB).
const MaxErrorBodyBytes = 1 << 20

// HTTPStatusError represents a non-2xx HTTP response that is not already
// covered by a more specific typed error. Delegates to httpclient.StatusError.
type HTTPStatusError = httpclient.StatusError

// ParseRetryAfter parses the Retry-After header from a response.
// Delegates to httpclient.ParseRetryAfterResponse (uncapped, matching original behavior).
func ParseRetryAfter(resp *http.Response) time.Duration {
	return httpclient.ParseRetryAfterResponse(resp)
}

// CheckHTTPStatus returns nil for a 2xx, *subflux.AuthError for a 401 or 403,
// *subflux.RateLimitError for a 429 and *HTTPStatusError for any other code.
func CheckHTTPStatus(resp *http.Response) error {
	err := httpclient.CheckStatus(resp)
	se, ok := errors.AsType[*httpclient.StatusError](err)
	if !ok {
		return err
	}
	switch se.Code {
	case http.StatusUnauthorized:
		return &subflux.AuthError{Msg: "invalid API key (401)"}
	case http.StatusForbidden:
		return &subflux.AuthError{Msg: "access denied (403)"}
	case http.StatusTooManyRequests:
		return &subflux.RateLimitError{Msg: "rate limited (429)", RetryAfter: se.RetryAfter}
	}
	return err
}

// IsTransient returns true for errors likely caused by temporary server
// or network issues worth retrying. Bridges subflux.AuthError and
// subflux.RateLimitError exclusion with httpclient.IsTransient for network checks.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[*subflux.AuthError](err); ok {
		return false
	}
	if _, ok := errors.AsType[*subflux.RateLimitError](err); ok {
		return false
	}
	return httpclient.IsTransient(err)
}

// LimitedBody wraps resp.Body with a read cap while preserving Close.
func LimitedBody(resp *http.Response) io.ReadCloser {
	return httpclient.LimitedBody(resp, MaxDownloadBytes)
}

// RetryOnRateLimit retries fn up to maxAttempts times when it returns a
// *subflux.RateLimitError. It hands that error to httpclient's rate-limit-only
// retry mode (httpclient's Do + WithRateLimitOnly) as the 429
// *httpclient.StatusError the mode matches: only rate limits are retried,
// waiting min(hint, maxWait), and every other error, transient included,
// returns immediately (transient retry is the caller's outer wrapper's job).
// Once the attempts run out, the caller gets fn's own *subflux.RateLimitError.
func RetryOnRateLimit(ctx context.Context, maxAttempts int, maxWait time.Duration, fn func() error) error {
	_, err := httpclient.Do(ctx, func(context.Context) (struct{}, error) {
		err := fn()
		if err == nil {
			return struct{}{}, nil
		}
		if rl, ok := errors.AsType[*subflux.RateLimitError](err); ok {
			return struct{}{}, &rateLimitedError{
				err:    rl,
				status: &httpclient.StatusError{Code: http.StatusTooManyRequests, RetryAfter: rl.RetryAfter},
			}
		}
		return struct{}{}, err
	}, httpclient.WithRateLimitOnly(maxWait), httpclient.WithMaxAttempts(maxAttempts))
	if rl, ok := errors.AsType[*rateLimitedError](err); ok {
		return rl.err
	}
	return err
}

// rateLimitedError is a *subflux.RateLimitError dressed as the 429 status error
// httpclient's rate-limit mode retries. It reads as the subflux error and
// unwraps to both.
type rateLimitedError struct {
	err    *subflux.RateLimitError
	status *httpclient.StatusError
}

func (e *rateLimitedError) Error() string { return e.err.Error() }

func (e *rateLimitedError) Unwrap() []error { return []error{e.err, e.status} }
