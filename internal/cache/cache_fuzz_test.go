package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

// FuzzCacheSetGet_roundtrip checks that a value stored under any key is
// returned unchanged by an immediate Get.
func FuzzCacheSetGet_roundtrip(f *testing.F) {
	f.Add("k1", "v1")
	f.Add("", "")
	f.Add("", "x") // promoted from the deleted Reap target: empty key, non-empty value
	f.Add("key", "value with spaces")

	f.Fuzz(func(t *testing.T, key, value string) {
		c := New[string](time.Minute)
		c.Set(key, value)

		got, ok := c.Get(key)
		if !ok {
			t.Errorf("Get(%q) returned not-ok after Set", key)
		}
		if got != value {
			t.Errorf("Get(%q) = %q, want %q", key, got, value)
		}
	})
}

// FuzzGetOrFetch_idempotent checks that once a key is populated, a second
// GetOrFetch returns the cached value and does not call the fetch function.
func FuzzGetOrFetch_idempotent(f *testing.F) {
	f.Add("key1", "value1")
	f.Add("", "x")
	f.Add("k", "")

	f.Fuzz(func(t *testing.T, key, value string) {
		c := New[string](time.Minute)

		got, err := c.GetOrFetch(key, func() (string, error) {
			return value, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != value {
			t.Errorf("first fetch got %q, want %q", got, value)
		}

		// Second fetch must return the cached value; this fn must not run.
		got2, err := c.GetOrFetch(key, func() (string, error) {
			return "SHOULD-NOT-CALL", nil
		})
		if err != nil {
			t.Fatalf("unexpected error on second fetch: %v", err)
		}
		if got2 != value {
			t.Errorf("second fetch got %q, want cached %q", got2, value)
		}
	})
}

// FuzzGetOrFetchCtx_cancellation checks that a pre-cancelled context yields
// context.Canceled (unless the value was already available), and that an
// active context produces the fetched value.
func FuzzGetOrFetchCtx_cancellation(f *testing.F) {
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
		// Parent stays context.Background(): the test decides below whether to cancel early, so it must own the only cancellation.
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
			// A cancelled context must surface context.Canceled, unless the
			// fetch completed before select observed the cancellation.
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("expected context.Canceled, got %v", err)
			}
			_ = result
		} else {
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != "value-"+key {
				t.Errorf("got %q, want %q", result, "value-"+key)
			}
		}
	})
}
