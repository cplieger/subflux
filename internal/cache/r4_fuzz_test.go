package cache

import (
	"testing"
	"time"
)

func FuzzGetOrFetch_Idempotence(f *testing.F) {
	f.Add("key1", "value1")
	f.Add("", "x")
	f.Add("k", "")

	f.Fuzz(func(t *testing.T, key, value string) {
		c := New[string](time.Minute)

		// First fetch populates
		got, err := c.GetOrFetch(key, func() (string, error) {
			return value, nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != value {
			t.Errorf("first fetch got %q, want %q", got, value)
		}

		// Second fetch must return cached value (fetch not called)
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

func FuzzCacheSetGet_Consistency(f *testing.F) {
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
