package arrapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// Compile-time assertion: *StatusError satisfies api.Transient.
var _ api.Transient = (*StatusError)(nil)

// StatusError represents a non-OK HTTP response from an arr API.
// Callers can use errors.As to inspect the status code and distinguish
// transient (5xx) from permanent (4xx) failures.
type StatusError struct {
	msg  string
	Body string
	Path string
	Code int
}

// newStatusError creates a StatusError with a pre-formatted message.
func newStatusError(code int, path, body string) *StatusError {
	var msg string
	if body != "" {
		msg = "request " + path + ": HTTP " + strconv.Itoa(code) + ": " + body
	} else {
		msg = "request " + path + ": HTTP " + strconv.Itoa(code)
	}
	return &StatusError{msg: msg, Body: body, Path: path, Code: code}
}

func (e *StatusError) Error() string {
	return e.msg
}

// IsTransient returns true for HTTP status codes that indicate a retryable
// failure (5xx server errors and 429 Too Many Requests).
func (e *StatusError) IsTransient() bool {
	return e.Code >= 500 || e.Code == 429
}

// fetchByID fetches a single resource by path and decodes it into dst.
//
// Wraps the underlying request in retryWithBackoff (3 attempts by default,
// 5s base delay, 25% jitter) so transient 5xx / 429 responses survive
// brief arr restarts and overload windows. Single-resource GETs are
// idempotent so retry is safe.
func (c *Client) fetchByID(ctx context.Context, path, label string, id int, dst any) error {
	_, err := retryWithBackoff(ctx, c.maxAttempts, c.retryDelay,
		fmt.Sprintf("%s %d", label, id),
		func(ctx context.Context) (struct{}, error) {
			resp, err := c.get(ctx, path) //nolint:bodyclose // closed by decodeJSON
			if err != nil {
				return struct{}{}, fmt.Errorf("get %s %d: %w", label, id, err)
			}
			if err := decodeJSON(resp.Body, httputil.MaxJSONResponseBytes, dst, fmt.Sprintf("%s %d", label, id)); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, nil
		})
	return err
}

// idPath builds an API path by appending the integer ID using strconv.AppendInt
// to avoid fmt.Sprintf overhead on the hot fetch path.
func idPath(base string, id int, suffix string) string {
	buf := make([]byte, 0, len(base)+20+len(suffix))
	buf = append(buf, base...)
	buf = strconv.AppendInt(buf, int64(id), 10)
	buf = append(buf, suffix...)
	return string(buf)
}

// get performs an authenticated GET request and returns the response.
// The caller must close the response body on success.
// If the context has no deadline and requestTimeout is set, it is applied.
func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	if _, ok := ctx.Deadline(); !ok && c.defaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.defaultTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", path, err)
	}
	req.Header.Set(api.HeaderXAPIKey, c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", path, err)
	}
	if err := classifyResponse(resp, path); err != nil {
		return nil, err
	}
	return resp, nil
}

// retryWithBackoff delegates to httputil.RetryWithBackoff for shared retry logic.
func retryWithBackoff[T any](ctx context.Context, maxAttempts int, baseDelay time.Duration,
	label string, fn func(ctx context.Context) (T, error),
) (T, error) {
	return httputil.RetryWithBackoff(ctx, maxAttempts, baseDelay, label, fn)
}
