// Package httputil provides shared HTTP utilities for subtitle providers.
// Behavioral logic is delegated to github.com/cplieger/httpx/v3; this package
// retains application-specific constants and thin adapters that bridge httpx
// error types to the internal api.* error types used across the codebase.
package httputil

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cplieger/httpx/v3"
	"github.com/cplieger/subflux/internal/api"
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
// covered by a more specific typed error. Delegates to httpx.HTTPStatusError.
type HTTPStatusError = httpx.HTTPStatusError

// ParseRetryAfter parses the Retry-After header from a response.
// Delegates to httpx.ParseRetryAfterResponse (uncapped, matching original behavior).
func ParseRetryAfter(resp *http.Response) time.Duration {
	return httpx.ParseRetryAfterResponse(resp)
}

// CheckHTTPStatus maps HTTP error status codes to typed errors.
// Returns nil for 2xx/3xx. Bridges httpx error types to api.* types
// so callers can continue using errors.As with *api.AuthError and
// *api.RateLimitError.
func CheckHTTPStatus(resp *http.Response) error {
	err := httpx.CheckHTTPStatus(resp)
	if err == nil {
		return nil
	}
	var hAuth *httpx.AuthError
	if errors.As(err, &hAuth) {
		return &api.AuthError{Msg: hAuth.Msg}
	}
	var hRL *httpx.RateLimitError
	if errors.As(err, &hRL) {
		return &api.RateLimitError{Msg: hRL.Msg, RetryAfter: hRL.RetryAfter}
	}
	return err
}

// IsTransient returns true for errors likely caused by temporary server
// or network issues worth retrying. Bridges api.AuthError and
// api.RateLimitError exclusion with httpx.IsTransient for network checks.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var authErr *api.AuthError
	if errors.As(err, &authErr) {
		return false
	}
	var rlErr *api.RateLimitError
	if errors.As(err, &rlErr) {
		return false
	}
	return httpx.IsTransient(err)
}

// LimitedBody wraps resp.Body with a read cap while preserving Close.
func LimitedBody(resp *http.Response) io.ReadCloser {
	return httpx.LimitedBody(resp, MaxDownloadBytes)
}

// RetryOnRateLimit retries fn up to maxAttempts times when it returns a
// *api.RateLimitError. Bridges the api.RateLimitError type to
// httpx.RateLimitError and runs httpx's rate-limit-only retry mode (v3's
// Do + WithRateLimitOnly, which absorbed the v2 RetryOnRateLimit helper):
// only rate limits are retried — waiting min(hint, maxWait) — and every
// other error, transient included, returns immediately (transient retry is
// the caller's outer wrapper's job).
func RetryOnRateLimit(ctx context.Context, maxAttempts int, maxWait time.Duration, fn func() error) error {
	_, err := httpx.Do(ctx, func(context.Context) (struct{}, error) {
		err := fn()
		if err == nil {
			return struct{}{}, nil
		}
		var rl *api.RateLimitError
		if errors.As(err, &rl) {
			return struct{}{}, &httpx.RateLimitError{Msg: rl.Msg, RetryAfter: rl.RetryAfter}
		}
		return struct{}{}, err
	}, httpx.WithRateLimitOnly(maxWait), httpx.WithMaxAttempts(maxAttempts))
	return err
}
