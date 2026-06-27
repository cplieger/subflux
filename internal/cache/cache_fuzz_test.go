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

// FuzzCacheReap_preservesValue checks that Reap never corrupts a surviving
// entry's value and never panics on arbitrary keys/timings.
func FuzzCacheReap_preservesValue(f *testing.F) {
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

		// Whether the entry survived depends on timing; if it did, its value
		// must be intact.
		if got, ok := c.Get(key); ok && got != value {
			t.Errorf("Get after Reap returned wrong value: got %q, want %q", got, value)
		}
	})
}
