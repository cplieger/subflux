// Package httputil provides shared HTTP utilities for subtitle providers:
// status checking, retry-after parsing, context-aware sleep, and common constants.
package httputil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"subflux/internal/api"
)

// UserAgent is the default User-Agent header sent by all providers.
const UserAgent = "Subflux/1.0"

// HeaderContentType is the canonical HTTP header name for content type.
const HeaderContentType = "Content-Type"

// ContentTypeJSON is the MIME type for JSON request/response bodies.
const ContentTypeJSON = "application/json"

// MaxDownloadBytes is the maximum response body size for subtitle/archive
// downloads (10 MB). All provider Download methods use this via
// io.LimitReader to prevent OOM from oversized responses.
const MaxDownloadBytes = 10 << 20

// MaxSingleSubtitleBytes is the maximum size for a single subtitle file
// download (5 MB). Used by providers that never return archives (e.g.
// gestdown) where the tighter cap is appropriate.
const MaxSingleSubtitleBytes = 5 << 20

// MaxJSONResponseBytes is the maximum size for a JSON API response body
// from a provider (5 MB). Prevents OOM from oversized metadata responses.
const MaxJSONResponseBytes = 5 << 20

// MaxSearchResponseBytes is the maximum size for a search/lookup JSON response
// (1 MB). Used by providers with tighter per-endpoint caps for search queries.
const MaxSearchResponseBytes = 1 << 20

// MaxListResponseBytes is the maximum size for a list/detail JSON response
// (2 MB). Used by providers returning subtitle lists or detailed metadata.
const MaxListResponseBytes = 2 << 20

// MaxErrorBodyBytes is the maximum size for reading error response bodies (1 MB).
// Used by arrapi and other clients to cap error body reads.
const MaxErrorBodyBytes = 1 << 20

// MaxBulkListBytes is the maximum size for bulk list responses (200 MB).
// Used by arrapi for full series/movie/episode list fetches.
const MaxBulkListBytes = 200 << 20

// HTTPStatusError represents a non-2xx HTTP response that is not already
// covered by a more specific typed error (AuthError for 401/403,
// RateLimitError for 429). Retry logic uses errors.As with this type to
// classify transient 5xx responses without string matching.
type HTTPStatusError struct {
	Code int
}

var _ api.Transient = (*HTTPStatusError)(nil)

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// IsTransient reports whether the HTTP status code indicates a retryable
// server-side failure (502 Bad Gateway, 503 Service Unavailable, 504
// Gateway Timeout). Auth errors (401/403) and rate limits (429) are handled
// by dedicated error types and are NOT transient from this type's perspective.
func (e *HTTPStatusError) IsTransient() bool {
	return e.Code == 502 || e.Code == 503 || e.Code == 504
}

// IsServerError reports whether the status code is a 5xx server error.
func (e *HTTPStatusError) IsServerError() bool { return e.Code >= 500 }

// IsClientError reports whether the status code is a 4xx client error.
func (e *HTTPStatusError) IsClientError() bool { return e.Code >= 400 && e.Code < 500 }

// ParseRetryAfter parses the Retry-After header per RFC 7231 (delta-seconds
// or HTTP-date). Returns zero if absent or unparseable. Negative durations
// (past HTTP-date) are clamped to zero. Exported so providers with custom
// status handling (opensubtitles' 406 in particular) can reuse the same
// parser their shared CheckHTTPStatus uses.
func ParseRetryAfter(resp *http.Response) time.Duration {
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return 0
	}
	if secs, err := strconv.Atoi(ra); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(ra); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// statusErrors maps HTTP status codes to error constructors. CheckHTTPStatus
// looks up the response code in this table before falling through to the
// generic >=400 handler. The table is package-visible for test inspection.
var statusErrors = map[int]func(*http.Response) error{
	http.StatusBadRequest: func(_ *http.Response) error {
		return &HTTPStatusError{Code: http.StatusBadRequest}
	},
	http.StatusUnauthorized: func(_ *http.Response) error {
		return &api.AuthError{Msg: "invalid API key (401)"}
	},
	http.StatusForbidden: func(_ *http.Response) error {
		return &api.AuthError{Msg: "access denied (403)"}
	},
	http.StatusTooManyRequests: func(r *http.Response) error {
		return &api.RateLimitError{Msg: "rate limited (429)", RetryAfter: ParseRetryAfter(r)}
	},
}

// CheckHTTPStatus maps common HTTP error status codes to typed errors.
// Returns nil for 2xx responses. Providers with custom status handling
// (e.g. gestdown's 423, opensubtitles' 406) should call this first and
// handle their custom codes separately.
//
// 429 responses parse the Retry-After header into RateLimitError.RetryAfter
// so the scan engine's provider timeout manager can honor the hint.
func CheckHTTPStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}
	if fn, ok := statusErrors[resp.StatusCode]; ok {
		return fn(resp)
	}
	if resp.StatusCode >= 400 {
		return &HTTPStatusError{Code: resp.StatusCode}
	}
	return nil
}

// RedactTransportError unwraps *url.Error (which embeds the full request URL
// including secrets) and redacts the secret from the resulting error message.
// Use this at every provider HTTP call site where the URL contains an API key.
//
// Pattern replaced:
//
//	if urlErr, ok := errors.AsType[*url.Error](err); ok {
//	    return nil, provider.RedactSecret(fmt.Errorf("prefix: %w", urlErr.Err), secret)
//	}
//	return nil, provider.RedactSecret(fmt.Errorf("prefix: %w", err), secret)
//
// Becomes:
//
//	return nil, httputil.RedactTransportError(err, "prefix", secret)
func RedactTransportError(err error, prefix, secret string) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var wrapped error
	if prefix == "" {
		wrapped = err
	} else {
		wrapped = fmt.Errorf("%s: %w", prefix, err)
	}
	if secret == "" {
		return wrapped
	}
	msg := wrapped.Error()
	if !strings.Contains(msg, secret) {
		return wrapped
	}
	return errors.New(strings.ReplaceAll(msg, secret, "REDACTED"))
}

// SleepCtx sleeps for the given duration or returns early if the context
// is cancelled. Returns nil on successful sleep, ctx.Err() on cancellation.
// Safe to call with d <= 0 (returns immediately).
func SleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	select {
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// drainBytes is the maximum number of bytes to read from a response body
// before closing, allowing HTTP/1.1 connection reuse by the transport pool.
const drainBytes = 4096

// DrainClose reads remaining bytes (up to drainBytes) from rc before closing it.
// This allows HTTP/1.1 connection reuse by the transport pool.
func DrainClose(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, drainBytes))
	rc.Close()
}

// LimitedBody wraps resp.Body with a read cap (MaxDownloadBytes) while
// preserving Close on the original body so the transport pool can drain
// and recycle the connection.
func LimitedBody(resp *http.Response) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, MaxDownloadBytes), resp.Body}
}

// RedactSecret replaces occurrences of secret in err's message with "REDACTED".
// Returns err unchanged if err is nil, secret is empty, or the secret doesn't
// appear in the error message.
//
// This is a convenience wrapper around RedactTransportError with an empty prefix.
func RedactSecret(err error, secret string) error {
	return RedactTransportError(err, "", secret)
}
