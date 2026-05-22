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
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"AuthError is not transient", &api.AuthError{Msg: "bad key"}, false},
		{"RateLimitError is not transient", &api.RateLimitError{Msg: "slow down"}, false},
		{"context.DeadlineExceeded is not transient", context.DeadlineExceeded, false},
		{"context.Canceled is not transient", context.Canceled, false},
		{"wrapped DeadlineExceeded is not transient", errors.Join(errors.New("op"), context.DeadlineExceeded), false},
		{"io.ErrUnexpectedEOF is transient", io.ErrUnexpectedEOF, true},
		{"ECONNRESET is transient", syscall.ECONNRESET, true},
		{"ECONNREFUSED is transient", syscall.ECONNREFUSED, true},
		{"DNSError is transient", &net.DNSError{Err: "lookup failed", Name: "example.com"}, true},
		{"net timeout error is transient", &fakeNetError{timeout: true}, true},
		{"net non-timeout error is not transient (no Transient iface)", &fakeNetError{timeout: false}, false},
		{"HTTPStatusError 502 is transient", &HTTPStatusError{Code: 502}, true},
		{"HTTPStatusError 503 is transient", &HTTPStatusError{Code: 503}, true},
		{"HTTPStatusError 504 is transient", &HTTPStatusError{Code: 504}, true},
		{"HTTPStatusError 400 is not transient", &HTTPStatusError{Code: 400}, false},
		{"HTTPStatusError 404 is not transient", &HTTPStatusError{Code: 404}, false},
		{"generic error is not transient", errors.New("something failed"), false},
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
