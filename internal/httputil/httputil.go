// Package httputil provides shared HTTP utilities for subtitle providers.
// Functions are delegated to github.com/cplieger/httpx; this package retains
// only application-specific constants and thin adapters where signatures differ.
package httputil

import (
	"io"
	"net/http"
	"time"

	"github.com/cplieger/httpx"
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

// MaxJSONResponseBytes is the maximum size for a JSON API response body
// from a provider (5 MB).
const MaxJSONResponseBytes = 5 << 20

// MaxSearchResponseBytes is the maximum size for a search/lookup JSON response
// (1 MB).
const MaxSearchResponseBytes = 1 << 20

// MaxListResponseBytes is the maximum size for a list/detail JSON response
// (2 MB).
const MaxListResponseBytes = 2 << 20

// MaxErrorBodyBytes is the maximum size for reading error response bodies (1 MB).
const MaxErrorBodyBytes = 1 << 20

// MaxBulkListBytes is the maximum size for bulk list responses (200 MB).
const MaxBulkListBytes = 200 << 20

// HTTPStatusError represents a non-2xx HTTP response.
type HTTPStatusError = httpx.HTTPStatusError

// ParseRetryAfter parses the Retry-After header from an *http.Response.
// Returns zero if absent or unparseable.
func ParseRetryAfter(resp *http.Response) time.Duration {
	return httpx.ParseRetryAfterResponse(resp)
}

// CheckHTTPStatus maps common HTTP error status codes to typed errors.
// Returns nil for 2xx/3xx responses.
func CheckHTTPStatus(resp *http.Response) error {
	return httpx.CheckHTTPStatus(resp)
}

// RedactTransportError unwraps *url.Error and redacts secrets.
func RedactTransportError(err error, prefix, secret string) error {
	return httpx.RedactTransportError(err, prefix, secret)
}

// RedactSecret replaces occurrences of secret in err's message with "REDACTED".
func RedactSecret(err error, secret string) error {
	return httpx.RedactSecret(err, secret)
}

// SleepCtx sleeps for the given duration or returns early if the context
// is cancelled.
var SleepCtx = httpx.SleepCtx

// DrainClose reads remaining bytes from rc before closing it.
func DrainClose(rc io.ReadCloser) {
	httpx.DrainClose(rc)
}

// LimitedBody wraps resp.Body with a read cap (MaxDownloadBytes) while
// preserving Close on the original body.
func LimitedBody(resp *http.Response) io.ReadCloser {
	return httpx.LimitedBody(resp, MaxDownloadBytes)
}
