// Package httputil provides shared HTTP utilities for subtitle providers.
// Behavioral logic is delegated to github.com/cplieger/httpx/v2; this package
// retains application-specific constants and thin adapters that bridge httpx
// error types to the internal api.* error types used across the codebase.
package httputil

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cplieger/httpx/v2"
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

// MaxBulkListBytes is the maximum size for bulk list responses (200 MB).
const MaxBulkListBytes = 200 << 20

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

// RedactTransportError unwraps *url.Error and redacts the secret.
func RedactTransportError(err error, prefix, secret string) error {
	return httpx.RedactTransportError(err, prefix, secret)
}

// RedactSecret replaces occurrences of secret in err's message with "REDACTED".
func RedactSecret(err error, secret string) error {
	return httpx.RedactSecret(err, secret)
}

// SleepCtx sleeps for the given duration or returns early if the context is cancelled.
func SleepCtx(ctx context.Context, d time.Duration) error {
	return httpx.SleepCtx(ctx, d)
}

// DrainClose reads remaining bytes from rc before closing it.
func DrainClose(rc io.ReadCloser) {
	httpx.DrainClose(rc)
}

// LimitedBody wraps resp.Body with a read cap while preserving Close.
func LimitedBody(resp *http.Response) io.ReadCloser {
	return httpx.LimitedBody(resp, MaxDownloadBytes)
}

// JitteredBackoff returns a jittered duration in [backoff/2, backoff].
func JitteredBackoff(backoff time.Duration) time.Duration {
	return httpx.JitteredBackoff(backoff)
}

// SafeDouble doubles a duration while guarding against int64 overflow.
func SafeDouble(d time.Duration) time.Duration {
	return httpx.SafeDouble(d)
}

// RetryOnRateLimit retries fn up to maxAttempts times when it returns a
// *api.RateLimitError. Bridges the api.RateLimitError type to httpx.RateLimitError.
func RetryOnRateLimit(ctx context.Context, maxAttempts int, maxWait time.Duration, fn func() error) error {
	return httpx.RetryOnRateLimit(ctx, maxAttempts, maxWait, func(_ context.Context) error {
		err := fn()
		if err == nil {
			return nil
		}
		var rl *api.RateLimitError
		if errors.As(err, &rl) {
			return &httpx.RateLimitError{Msg: rl.Msg, RetryAfter: rl.RetryAfter}
		}
		return err
	})
}

// RetryWithBackoff retries fn with jittered exponential backoff.
func RetryWithBackoff[T any](ctx context.Context, maxAttempts int, baseDelay time.Duration,
	label string, fn func(ctx context.Context) (T, error),
) (T, error) {
	return httpx.RetryWithBackoff(ctx, maxAttempts, baseDelay, label, func(ctx context.Context) (T, error) {
		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		// Bridge api.* errors to httpx.* so httpx.IsTransient classifies correctly.
		var authErr *api.AuthError
		if errors.As(err, &authErr) {
			return result, httpx.Permanent(err)
		}
		var rlErr *api.RateLimitError
		if errors.As(err, &rlErr) {
			return result, httpx.Permanent(err)
		}
		return result, err
	})
}
