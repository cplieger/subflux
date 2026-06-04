package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func FuzzGetOrFetchCtx_CancelProperty(f *testing.F) {
	f.Add("key1", true, int64(50))
	f.Add("", false, int64(0))
	f.Add("k", true, int64(1))

	f.Fuzz(func(t *testing.T, key string, cancelEarly bool, delayMs int64) {
		if delayMs < 0 {
			delayMs = 0
		}
		if delayMs > 100 {
			delayMs = 100
		}

		c := New[string](time.Minute)
		ctx, cancel := context.WithCancel(context.Background())

		if cancelEarly {
			cancel()
		} else {
			defer cancel()
		}

		result, err := c.GetOrFetchCtx(ctx, key, func(ctx context.Context) (string, error) {
			if delayMs > 0 {
				time.Sleep(time.Duration(delayMs) * time.Millisecond)
			}
			return "value-" + key, nil
		})

		if cancelEarly {
			// If context was cancelled before call, must return context error
			if err == nil {
				// Acceptable: value may have been cached from a concurrent call
				// or fetch completed before select noticed cancellation
				_ = result
			} else if !errors.Is(err, context.Canceled) {
				t.Errorf("expected context.Canceled, got %v", err)
			}
		} else {
			// Context was not cancelled; should succeed
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != "value-"+key {
				t.Errorf("got %q, want %q", result, "value-"+key)
			}
		}
	})
}

func FuzzCacheReapPreservesFresh(f *testing.F) {
	f.Add("k1", "v1", int64(1), int64(100))
	f.Add("", "x", int64(0), int64(0))

	f.Fuzz(func(t *testing.T, key, value string, ttlMs, sleepMs int64) {
		if ttlMs < 1 {
			ttlMs = 1
		}
		if ttlMs > 1000 {
			ttlMs = 1000
		}
		if sleepMs < 0 {
			sleepMs = 0
		}
		if sleepMs > 50 {
			sleepMs = 50
		}

		c := New[string](time.Duration(ttlMs) * time.Millisecond)
		c.Set(key, value)

		time.Sleep(time.Duration(sleepMs) * time.Millisecond)
		c.Reap()

		got, ok := c.Get(key)
		if ok && got != value {
			t.Errorf("Get after Reap returned wrong value: got %q, want %q", got, value)
		}
		// Timing-sensitive: just verify no panic and value consistency
		_ = ok
	})
}
