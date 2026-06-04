package showskip

import (
	"testing"
	"time"
)

// FuzzCacheRoundtrip verifies the roundtrip invariant: Set(k, v) followed
// by Get(k) returns (v, true) while the TTL has not expired.
func FuzzCacheRoundtrip(f *testing.F) {
	f.Add("show-123", true)
	f.Add("", false)
	f.Add("abc/def", true)

	f.Fuzz(func(t *testing.T, key string, skip bool) {
		c := New(1 * time.Hour)
		c.Set(key, skip)
		got, ok := c.Get(key)
		if !ok {
			t.Fatalf("Get(%q) returned ok=false after Set", key)
		}
		if got != skip {
			t.Fatalf("Get(%q) = %v, want %v", key, got, skip)
		}
	})
}

// FuzzCacheSetIdempotent verifies that setting the same key twice with the
// same value is idempotent.
func FuzzCacheSetIdempotent(f *testing.F) {
	f.Add("key1", true)
	f.Add("key2", false)

	f.Fuzz(func(t *testing.T, key string, skip bool) {
		c := New(1 * time.Hour)
		c.Set(key, skip)
		c.Set(key, skip)
		got, ok := c.Get(key)
		if !ok {
			t.Fatalf("Get(%q) returned ok=false", key)
		}
		if got != skip {
			t.Fatalf("Get(%q) = %v after double-set, want %v", key, got, skip)
		}
	})
}
