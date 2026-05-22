package httputil

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"syscall"
	"time"

	"subflux/internal/api"
)

// JitteredBackoff returns a duration in the range [backoff/2, backoff),
// so that concurrent retries do not stampede a recovering upstream in
// lockstep while preserving the doubling trend between attempts.
// Zero or negative backoff is returned unchanged.
func JitteredBackoff(backoff time.Duration) time.Duration {
	if backoff <= 0 {
		return backoff
	}
	half := int64(backoff) / 2
	jitter := rand.Int64N(half + 1) //nolint:gosec // G404: jitter, not crypto
	return time.Duration(half + jitter)
}

// SafeDouble doubles a duration while guarding against int64 overflow.
// If doubling would overflow or produce a negative value, returns
// math.MaxInt64 as a time.Duration (capped by maxBackoff downstream).
func SafeDouble(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	doubled := d * 2
	if doubled < d {
		// Overflow occurred.
		return time.Duration(1<<63 - 1)
	}
	return doubled
}

// IsTransient returns true for errors likely caused by temporary server
// or network issues worth retrying. Auth and rate-limit errors are never
// transient (the caller owes a fix, not a retry). Context errors are
// never transient (the parent timeout or cancellation has fired).
//
// Matches:
//   - HTTPStatusError with 502/503/504 code
//   - net.Error with Timeout() (includes tls/dial/read timeouts)
//   - io.ErrUnexpectedEOF (mid-body keep-alive close)
//   - net.OpError wrapping syscall.ECONNRESET or syscall.ECONNREFUSED
//   - *net.DNSError (transient DNS resolution failures)
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
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	// Check the shared Transient interface first (covers arrapi.StatusError
	// and any future error types implementing api.Transient).
	var t api.Transient
	if errors.As(err, &t) {
		return t.IsTransient()
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

// RetryOnRateLimit retries fn up to maxAttempts times when it returns a
// *api.RateLimitError. It honors RetryAfter (capped at maxWait) and
// respects context cancellation between attempts. Non-rate-limit errors
// are returned immediately without retry.
func RetryOnRateLimit(ctx context.Context, maxAttempts int, maxWait time.Duration, fn func() error) error {
	var lastErr error
	for attempt := range maxAttempts {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		rlErr, ok := errors.AsType[*api.RateLimitError](lastErr)
		if !ok {
			return lastErr
		}
		if attempt == maxAttempts-1 {
			break
		}
		wait := maxWait
		if rlErr.RetryAfter > 0 {
			wait = min(rlErr.RetryAfter, maxWait)
		}
		if err := SleepCtx(ctx, wait); err != nil {
			return err
		}
	}
	return lastErr
}

// RetryWithBackoff retries fn up to maxRetries times with jittered exponential
// backoff. The delay starts at baseDelay and doubles each attempt. Context
// cancellation is respected between attempts. Permanent failures (auth errors,
// 4xx status codes) are returned immediately without retry. Shared between
// arrapi and provider packages.
func RetryWithBackoff[T any](ctx context.Context, maxRetries int, baseDelay time.Duration,
	label string, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	var lastErr error
	backoff := baseDelay
	for attempt := range maxRetries {
		result, err := fn(ctx)
		if err == nil {
			if attempt > 0 {
				slog.Debug(label+" succeeded after retry", "attempts", attempt+1)
			}
			return result, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		if !IsTransient(err) {
			return zero, err
		}
		if attempt == maxRetries-1 {
			break
		}
		wait := JitteredBackoff(backoff)
		slog.Warn(label+" failed, retrying",
			"attempt", attempt+1, "max", maxRetries,
			"delay", wait.String(), "error", err)
		if err := SleepCtx(ctx, wait); err != nil {
			return zero, err
		}
		backoff = SafeDouble(backoff)
	}
	return zero, lastErr
}
