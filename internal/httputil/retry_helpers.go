package httputil

import (
	"context"
	"time"

	"github.com/cplieger/httpx"
)

// JitteredBackoff returns a duration in [backoff/2, backoff).
var JitteredBackoff = httpx.JitteredBackoff

// SafeDouble doubles a duration while guarding against int64 overflow.
var SafeDouble = httpx.SafeDouble

// IsTransient returns true for errors likely caused by temporary server
// or network issues worth retrying.
var IsTransient = httpx.IsTransient

// RetryOnRateLimit retries fn up to maxAttempts times when it returns a
// *RateLimitError. It honors RetryAfter (capped at maxWait) and
// respects context cancellation between attempts.
func RetryOnRateLimit(ctx context.Context, maxAttempts int, maxWait time.Duration, fn func() error) error {
	return httpx.RetryOnRateLimit(ctx, maxAttempts, maxWait, func(_ context.Context) error {
		return fn()
	})
}

// RetryWithBackoff retries fn up to maxRetries times with jittered exponential
// backoff. Non-transient errors are returned immediately.
func RetryWithBackoff[T any](ctx context.Context, maxRetries int, baseDelay time.Duration,
	label string, fn func(ctx context.Context) (T, error)) (T, error) {
	return httpx.RetryWithBackoff(ctx, maxRetries, baseDelay, label, fn)
}
