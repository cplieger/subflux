package httputil

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"subflux/internal/api"
)

func TestIsTransient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		name string
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "AuthError is not transient", err: &api.AuthError{Msg: "bad key"}, want: false},
		{name: "RateLimitError is not transient", err: &api.RateLimitError{Msg: "slow down"}, want: false},
		{name: "context.DeadlineExceeded is not transient", err: context.DeadlineExceeded, want: false},
		{name: "context.Canceled is not transient", err: context.Canceled, want: false},
		{name: "wrapped DeadlineExceeded is not transient", err: errors.Join(errors.New("op"), context.DeadlineExceeded), want: false},
		{name: "io.ErrUnexpectedEOF is transient", err: io.ErrUnexpectedEOF, want: true},
		{name: "ECONNRESET is transient", err: syscall.ECONNRESET, want: true},
		{name: "ECONNREFUSED is transient", err: syscall.ECONNREFUSED, want: true},
		{name: "DNSError is transient", err: &net.DNSError{Err: "lookup failed", Name: "example.com"}, want: true},
		{name: "net timeout error is transient", err: &fakeNetError{timeout: true}, want: true},
		{name: "net non-timeout error is not transient (no Transient iface)", err: &fakeNetError{timeout: false}, want: false},
		{name: "HTTPStatusError 502 is transient", err: &HTTPStatusError{Code: 502}, want: true},
		{name: "HTTPStatusError 503 is transient", err: &HTTPStatusError{Code: 503}, want: true},
		{name: "HTTPStatusError 504 is transient", err: &HTTPStatusError{Code: 504}, want: true},
		{name: "HTTPStatusError 400 is not transient", err: &HTTPStatusError{Code: 400}, want: false},
		{name: "HTTPStatusError 404 is not transient", err: &HTTPStatusError{Code: 404}, want: false},
		{name: "generic error is not transient", err: errors.New("something failed"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsTransient(tt.err)
			if got != tt.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// fakeNetError implements net.Error for testing.
type fakeNetError struct {
	timeout bool
}

func (e *fakeNetError) Error() string   { return "fake net error" }
func (e *fakeNetError) Timeout() bool   { return e.timeout }
func (e *fakeNetError) Temporary() bool { return false }

func TestRetryOnRateLimit(t *testing.T) {
	t.Parallel()

	t.Run("success on first call", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := RetryOnRateLimit(context.Background(), 3, 5*time.Second, func() error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call, got %d", calls)
		}
	})

	t.Run("non-rate-limit error returns immediately", func(t *testing.T) {
		t.Parallel()
		calls := 0
		wantErr := errors.New("permanent failure")
		err := RetryOnRateLimit(context.Background(), 3, 5*time.Second, func() error {
			calls++
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected %v, got %v", wantErr, err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call (no retry for non-rate-limit), got %d", calls)
		}
	})

	t.Run("rate-limit error retries", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := RetryOnRateLimit(context.Background(), 3, 5*time.Second, func() error {
			calls++
			if calls < 3 {
				return &api.RateLimitError{Msg: "slow", RetryAfter: time.Millisecond}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("exhausts attempts returns last error", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := RetryOnRateLimit(context.Background(), 2, 5*time.Second, func() error {
			calls++
			return &api.RateLimitError{Msg: "slow", RetryAfter: time.Millisecond}
		})
		if err == nil {
			t.Fatal("expected error after exhausting attempts")
		}
		var rlErr *api.RateLimitError
		if !errors.As(err, &rlErr) {
			t.Fatalf("expected RateLimitError, got %T: %v", err, err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
	})

	t.Run("context cancellation stops retry", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := RetryOnRateLimit(ctx, 5, 5*time.Second, func() error {
			calls++
			if calls == 1 {
				cancel()
			}
			return &api.RateLimitError{Msg: "slow", RetryAfter: time.Millisecond}
		})
		if err == nil {
			t.Fatal("expected error from cancelled context")
		}
		if calls > 2 {
			t.Fatalf("expected at most 2 calls with cancellation, got %d", calls)
		}
	})
}

func TestRetryWithBackoff(t *testing.T) {
	t.Parallel()

	t.Run("success on first call", func(t *testing.T) {
		t.Parallel()
		calls := 0
		result, err := RetryWithBackoff(context.Background(), 3, time.Millisecond, "test", func(_ context.Context) (string, error) {
			calls++
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "ok" {
			t.Fatalf("expected 'ok', got %q", result)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call, got %d", calls)
		}
	})

	t.Run("retries on error then succeeds", func(t *testing.T) {
		t.Parallel()
		calls := 0
		result, err := RetryWithBackoff(context.Background(), 4, time.Millisecond, "test", func(_ context.Context) (int, error) {
			calls++
			if calls < 3 {
				return 0, &HTTPStatusError{Code: 503} // transient
			}
			return 42, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != 42 {
			t.Fatalf("expected 42, got %d", result)
		}
		if calls != 3 {
			t.Fatalf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("exhausts retries returns last error", func(t *testing.T) {
		t.Parallel()
		calls := 0
		_, err := RetryWithBackoff(context.Background(), 3, time.Millisecond, "test", func(_ context.Context) (string, error) {
			calls++
			return "", &HTTPStatusError{Code: 502} // transient
		})
		if err == nil {
			t.Fatal("expected error after exhausting retries")
		}
		if calls != 3 {
			t.Fatalf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("non-transient error not retried", func(t *testing.T) {
		t.Parallel()
		calls := 0
		_, err := RetryWithBackoff(context.Background(), 3, time.Millisecond, "test", func(_ context.Context) (string, error) {
			calls++
			return "", errors.New("permanent failure")
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if calls != 1 {
			t.Fatalf("expected 1 call (non-transient not retried), got %d", calls)
		}
	})

	t.Run("context cancellation stops retry", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, err := RetryWithBackoff(ctx, 5, time.Millisecond, "test", func(_ context.Context) (string, error) {
			calls++
			if calls == 1 {
				cancel()
			}
			return "", &HTTPStatusError{Code: 503} // transient
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}
