package showskip

import (
	"fmt"
	"testing"
	"time"
)

func TestCache_expiry_scenarios(t *testing.T) {
	t.Parallel()
	tests := []struct {
		setup   func(c *Cache)
		name    string
		key     string
		ttl     time.Duration
		wantVal bool
		wantOK  bool
	}{
		{name: "fresh_within_ttl", ttl: time.Minute, setup: func(c *Cache) { c.Set("k", true) }, key: "k", wantVal: true, wantOK: true},
		{name: "expired_past_ttl", ttl: time.Millisecond, setup: func(c *Cache) {
			c.Set("k", true)
			time.Sleep(5 * time.Millisecond)
		}, key: "k", wantVal: false, wantOK: false},
		{name: "miss_no_entry", ttl: time.Minute, setup: func(_ *Cache) {}, key: "k", wantVal: false, wantOK: false},
		{name: "overwrite_extends", ttl: time.Minute, setup: func(c *Cache) {
			c.Set("k", true)
			c.Set("k", false)
		}, key: "k", wantVal: false, wantOK: true},
		{name: "clear_removes_all", ttl: time.Minute, setup: func(c *Cache) {
			c.Set("k", true)
			c.Clear()
		}, key: "k", wantVal: false, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := New(tt.ttl)
			tt.setup(c)
			val, ok := c.Get(tt.key)
			if ok != tt.wantOK || val != tt.wantVal {
				t.Errorf("Get(%q) = (%v, %v), want (%v, %v)",
					tt.key, val, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

func BenchmarkCache_Lookup(b *testing.B) {
	c := New(time.Minute)
	for i := range 100 {
		c.Set(fmt.Sprintf("key-%d", i), i%2 == 0)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		c.Get("key-50")
	}
}
