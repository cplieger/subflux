package arrapi

import (
	"context"

	"github.com/cplieger/subflux/internal/httputil"
)

// fetchAllAvgItemSize is a conservative lower-bound estimate of the average
// JSON object size (in bytes) used as a heuristic for pre-allocating the
// result slice from Content-Length.
//
// Actual sizes vary by endpoint: episodes ~500 B, history ~300 B,
// movies ~1.5 KB, series ~2 KB. The value 200 is intentionally low to
// avoid reallocation on the episode-list path (most items, smallest
// objects). Over-allocation for larger-object endpoints (series, movies)
// is bounded by fetchAllMaxBytes/200 ≈ 1 M entries and is preferred over
// under-allocation which would cause repeated slice growth.
const fetchAllAvgItemSize = 200

// fetchAll performs an authenticated GET and decodes the JSON array response.
// The caller provides the target type via the type parameter.
//
// Wraps the underlying request in retryWithBackoff (3 attempts by default,
// 5s base delay, 25% jitter) so transient 5xx / 429 responses survive
// brief arr restarts and overload windows. GETs are idempotent so this
// is safe; POST commands intentionally bypass retry via postCommand.
func fetchAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	return retryWithBackoff(ctx, c.maxRetries, c.retryDelay, "fetchAll "+path,
		func(ctx context.Context) ([]T, error) {
			resp, err := c.get(ctx, path) //nolint:bodyclose // closed by decodeJSONSlice
			if err != nil {
				return nil, err
			}
			return decodeJSONSlice[T](resp, httputil.MaxBulkListBytes, path)
		})
}
