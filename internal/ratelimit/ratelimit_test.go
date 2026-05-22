package ratelimit

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Feature: subflux-authentication, Property 19: Rate limiter sliding window
// **Validates: Requirements 15.1, 15.5**
func TestProperty_RateLimiterSlidingWindow(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		ip := fmt.Sprintf("%d.%d.%d.%d",
			rapid.IntRange(1, 255).Draw(t, "ip1"),
			rapid.IntRange(0, 255).Draw(t, "ip2"),
			rapid.IntRange(0, 255).Draw(t, "ip3"),
			rapid.IntRange(1, 254).Draw(t, "ip4"),
		)
		username := rapid.StringN(3, 32, -1).Draw(t, "username")

		now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

		rl := NewRateLimiter(context.Background(), DefaultConfig())
		defer rl.Stop()
		rl.nowFunc = func() time.Time { return now }

		for i := range rl.ipLimit {
			allowed, _ := rl.Allow(ip, username)
			if !allowed {
				t.Fatalf("attempt %d: should be allowed (under IP limit)", i+1)
			}
			rl.Record(ip, username)
		}

		allowed, retryAfter := rl.Allow(ip, username)
		if allowed {
			t.Fatal("attempt 11: should be blocked (IP limit exceeded)")
		}
		if retryAfter <= 0 {
			t.Fatalf("retryAfter should be positive, got %v", retryAfter)
		}

		now = now.Add(rl.ipWindow + time.Second)
		allowed, _ = rl.Allow(ip, username)
		if !allowed {
			t.Fatal("after IP window elapsed: should be allowed again")
		}

		now = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		rl2 := NewRateLimiter(context.Background(), DefaultConfig())
		defer rl2.Stop()
		rl2.nowFunc = func() time.Time { return now }

		for i := range rl2.acctLimit {
			diffIP := fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256+1)
			allowed, _ := rl2.Allow(diffIP, username)
			if !allowed {
				t.Fatalf("account attempt %d: should be allowed (under account limit)", i+1)
			}
			rl2.Record(diffIP, username)
		}

		freshIP := "172.16.0.1"
		allowed, retryAfter = rl2.Allow(freshIP, username)
		if allowed {
			t.Fatal("account attempt 101: should be blocked (account limit exceeded)")
		}
		if retryAfter <= 0 {
			t.Fatalf("account retryAfter should be positive, got %v", retryAfter)
		}

		now = now.Add(rl2.acctWindow + time.Second)
		allowed, _ = rl2.Allow(freshIP, username)
		if !allowed {
			t.Fatal("after account window elapsed: should be allowed again")
		}

		now = time.Date(2025, 9, 1, 12, 0, 0, 0, time.UTC)
		rl3 := NewRateLimiter(context.Background(), DefaultConfig())
		defer rl3.Stop()
		rl3.nowFunc = func() time.Time { return now }

		for range rl3.ipLimit {
			rl3.Record(ip, username)
		}

		allowed, _ = rl3.Allow(ip, username)
		if allowed {
			t.Fatal("after ipLimit failures without Record: should still be blocked")
		}
	})
}

func TestRateLimiter_prune_removes_stale_entries(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	rl := NewRateLimiter(context.Background(), DefaultConfig())
	defer rl.Stop()
	rl.nowFunc = func() time.Time { return now }

	rl.Record("10.0.0.1", "alice")
	rl.Record("10.0.0.2", "bob")

	now = now.Add(2 * time.Hour)
	rl.prune()

	rl.muIP.Lock()
	ipCount := len(rl.ipWindows)
	rl.muIP.Unlock()

	rl.muAcct.Lock()
	acctCount := len(rl.acctWindows)
	rl.muAcct.Unlock()

	if ipCount != 0 {
		t.Errorf("prune() left %d IP windows, want 0", ipCount)
	}
	if acctCount != 0 {
		t.Errorf("prune() left %d account windows, want 0", acctCount)
	}
}

func TestRateLimiter_prune_keeps_active_entries(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	rl := NewRateLimiter(context.Background(), DefaultConfig())
	defer rl.Stop()
	rl.nowFunc = func() time.Time { return now }

	rl.Record("10.0.0.1", "alice")

	now = now.Add(10 * time.Minute)
	rl.prune()

	rl.muIP.Lock()
	ipCount := len(rl.ipWindows)
	rl.muIP.Unlock()

	if ipCount != 1 {
		t.Errorf("prune() removed active IP window: got %d, want 1", ipCount)
	}
}

func TestRateLimiter_empty_username_skips_account_tracking(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	rl := NewRateLimiter(context.Background(), DefaultConfig())
	defer rl.Stop()
	rl.nowFunc = func() time.Time { return now }

	rl.Record("10.0.0.1", "")

	rl.muIP.Lock()
	ipCount := len(rl.ipWindows)
	rl.muIP.Unlock()

	rl.muAcct.Lock()
	acctCount := len(rl.acctWindows)
	rl.muAcct.Unlock()

	if ipCount != 1 {
		t.Errorf("Record(ip, \"\") IP windows = %d, want 1", ipCount)
	}
	if acctCount != 0 {
		t.Errorf("Record(ip, \"\") account windows = %d, want 0", acctCount)
	}

	allowed, _ := rl.Allow("10.0.0.1", "")
	if !allowed {
		t.Error("Allow(ip, \"\") = false after 1 attempt, want true (under IP limit)")
	}
}

func TestRateLimiter_Record_caps_ip_entries(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	rl := NewRateLimiter(context.Background(), DefaultConfig())
	defer rl.Stop()
	rl.nowFunc = func() time.Time { return now }

	for i := range rl.maxEntries {
		ip := fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256+1)
		rl.Record(ip, "")
	}

	rl.muIP.Lock()
	ipCount := len(rl.ipWindows)
	rl.muIP.Unlock()

	if ipCount != rl.maxEntries {
		t.Fatalf("Record() IP windows = %d, want %d", ipCount, rl.maxEntries)
	}

	rl.Record("192.168.99.99", "")

	rl.muIP.Lock()
	ipCountAfter := len(rl.ipWindows)
	rl.muIP.Unlock()

	if ipCountAfter != rl.maxEntries {
		t.Fatalf("Record() IP windows after cap = %d, want %d (should not grow)", ipCountAfter, rl.maxEntries)
	}

	allowed, _ := rl.Allow("10.0.0.1", "")
	if !allowed {
		t.Fatal("Allow() for existing IP after cap = false, want true (only 1 attempt)")
	}
}

func TestRateLimiter_Record_caps_account_entries(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	rl := NewRateLimiter(context.Background(), DefaultConfig())
	defer rl.Stop()
	rl.nowFunc = func() time.Time { return now }

	for i := range rl.maxEntries {
		username := fmt.Sprintf("user%d", i)
		rl.Record("10.0.0.1", username)
	}

	rl.muAcct.Lock()
	acctCount := len(rl.acctWindows)
	rl.muAcct.Unlock()

	if acctCount != rl.maxEntries {
		t.Fatalf("Record() account windows = %d, want %d", acctCount, rl.maxEntries)
	}

	rl.Record("10.0.0.2", "overflow-user")

	rl.muAcct.Lock()
	acctCountAfter := len(rl.acctWindows)
	rl.muAcct.Unlock()

	if acctCountAfter != rl.maxEntries {
		t.Fatalf("Record() account windows after cap = %d, want %d (should not grow)", acctCountAfter, rl.maxEntries)
	}

	allowed, _ := rl.Allow("172.16.0.1", "user0")
	if !allowed {
		t.Fatal("Allow() for existing account after cap = false, want true (only 1 attempt)")
	}
}

func TestRateLimiter_retryAfter_returns_correct_duration(t *testing.T) {
	t.Parallel()

	start := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	now := start

	rl := NewRateLimiter(context.Background(), DefaultConfig())
	defer rl.Stop()
	rl.nowFunc = func() time.Time { return now }

	ip := "10.0.0.1"

	for range rl.ipLimit {
		rl.Record(ip, "")
	}

	now = start.Add(5 * time.Minute)

	allowed, retryAfter := rl.Allow(ip, "")
	if allowed {
		t.Fatal("Allow() = true, want false (IP limit exceeded)")
	}

	want := start.Add(rl.ipWindow).Sub(now)
	if retryAfter != want {
		t.Errorf("Allow() retryAfter = %v, want %v", retryAfter, want)
	}
}

func TestProperty_SlidingWindowCountPrunesCorrectly(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		window := time.Duration(rapid.IntRange(1, 3600).Draw(t, "windowSec")) * time.Second
		now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).Add(
			time.Duration(rapid.IntRange(0, 86400).Draw(t, "offsetSec")) * time.Second)

		n := rapid.IntRange(0, 50).Draw(t, "numEntries")
		offsets := make([]int, n)
		for i := range n {
			offsets[i] = rapid.IntRange(0, int(2*window/time.Second)).Draw(t, fmt.Sprintf("offset%d", i))
		}
		slices.Sort(offsets)

		w := &slidingWindow{}
		for i := range n {
			w.add(now.Add(-time.Duration(offsets[n-1-i]) * time.Second))
		}

		count := w.count(now, window)
		cutoff := now.Add(-window)

		if count != len(w.timestamps) {
			t.Fatalf("count() = %d but len(timestamps) = %d", count, len(w.timestamps))
		}

		for i, ts := range w.timestamps {
			if ts.Before(cutoff) {
				t.Fatalf("timestamps[%d] = %v is before cutoff %v", i, ts, cutoff)
			}
		}
	})
}

func TestAllow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(rl *RateLimiter, now *time.Time)
		ip      string
		user    string
		allowed bool
	}{
		{
			name:    "first attempt always allowed",
			setup:   func(_ *RateLimiter, _ *time.Time) {},
			ip:      "10.0.0.1",
			user:    "alice",
			allowed: true,
		},
		{
			name: "under limit all allowed",
			setup: func(rl *RateLimiter, _ *time.Time) {
				for range rl.ipLimit - 1 {
					rl.Record("10.0.0.1", "alice")
				}
			},
			ip:      "10.0.0.1",
			user:    "alice",
			allowed: true,
		},
		{
			name: "at limit blocked",
			setup: func(rl *RateLimiter, _ *time.Time) {
				for range rl.ipLimit {
					rl.Record("10.0.0.1", "alice")
				}
			},
			ip:      "10.0.0.1",
			user:    "alice",
			allowed: false,
		},
		{
			name: "different IPs independent counters",
			setup: func(rl *RateLimiter, _ *time.Time) {
				for range rl.ipLimit {
					rl.Record("10.0.0.1", "alice")
				}
			},
			ip:      "10.0.0.2",
			user:    "alice",
			allowed: true,
		},
		{
			name: "after cooldown reset and allowed",
			setup: func(rl *RateLimiter, now *time.Time) {
				for range rl.ipLimit {
					rl.Record("10.0.0.1", "alice")
				}
				*now = now.Add(rl.ipWindow + time.Second)
			},
			ip:      "10.0.0.1",
			user:    "alice",
			allowed: true,
		},
		{
			name: "account limit reached all IPs blocked",
			setup: func(rl *RateLimiter, _ *time.Time) {
				for i := range rl.acctLimit {
					ip := fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256+1)
					rl.Record(ip, "alice")
				}
			},
			ip:      "172.16.0.1",
			user:    "alice",
			allowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
			rl := NewRateLimiter(context.Background(), DefaultConfig())
			defer rl.Stop()
			rl.nowFunc = func() time.Time { return now }

			tt.setup(rl, &now)

			allowed, retryAfter := rl.Allow(tt.ip, tt.user)
			if allowed != tt.allowed {
				t.Errorf("Allow(%q, %q) = %v, want %v", tt.ip, tt.user, allowed, tt.allowed)
			}
			if !allowed && retryAfter <= 0 {
				t.Errorf("Allow() blocked but retryAfter = %v, want > 0", retryAfter)
			}
			if allowed && retryAfter != 0 {
				t.Errorf("Allow() allowed but retryAfter = %v, want 0", retryAfter)
			}
		})
	}
}

func TestRateLimiter_prune_concurrent(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex

	rl := NewRateLimiter(context.Background(), DefaultConfig())
	defer rl.Stop()
	rl.nowFunc = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	var wg sync.WaitGroup

	for i := range 4 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ip := fmt.Sprintf("10.0.%d.1", id)
			user := fmt.Sprintf("user-%d", id)
			for range 200 {
				rl.Record(ip, user)
			}
		}(i)
	}

	for i := range 2 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ip := fmt.Sprintf("10.0.%d.1", id)
			user := fmt.Sprintf("user-%d", id)
			for range 200 {
				allowed, retryAfter := rl.Allow(ip, user)
				if !allowed && retryAfter <= 0 {
					t.Errorf("Allow returned blocked with non-positive retryAfter")
				}
			}
		}(i)
	}

	wg.Go(func() {
		for range 10 {
			mu.Lock()
			now = now.Add(20 * time.Minute)
			mu.Unlock()
			rl.prune()
		}
	})

	wg.Wait()
}

func BenchmarkRateLimiter_parallel(b *testing.B) {
	rl := NewRateLimiter(context.Background(), DefaultConfig())
	defer rl.Stop()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ip := fmt.Sprintf("10.0.%d.%d", (i/256)%256, i%256+1)
			user := fmt.Sprintf("user-%d", i%100)
			rl.Allow(ip, user)
			rl.Record(ip, user)
			i++
		}
	})
}
