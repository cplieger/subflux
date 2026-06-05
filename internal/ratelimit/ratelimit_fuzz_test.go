package ratelimit

import (
	"context"
	"testing"
	"time"
)

func FuzzRateLimiterAllowRecord(f *testing.F) {
	f.Add("192.168.1.1", "alice", 5)
	f.Add("10.0.0.1", "", 0)
	f.Add("", "bob", 3)
	f.Add("", "", 10)
	f.Add("::1", "user@example.com", 1)
	f.Add("very-long-string-that-is-not-an-ip", "very-long-username-string", 15)

	f.Fuzz(func(t *testing.T, ip, username string, attempts int) {
		if attempts < 0 || attempts > 20 {
			return
		}

		cfg := Config{
			IPLimit:       5,
			IPWindow:      time.Minute,
			AcctLimit:     10,
			AcctWindow:    5 * time.Minute,
			PruneInterval: time.Hour, // won't fire during test
			MaxEntries:    100,
		}
		rl := NewRateLimiter(context.Background(), cfg)
		defer rl.Stop()

		now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
		rl.nowFunc = func() time.Time { return now }

		for range attempts {
			rl.Record(ip, username)
		}

		allowed, retryAfter := rl.Allow(ip, username)

		// Invariant: if allowed, retryAfter must be zero.
		if allowed && retryAfter != 0 {
			t.Fatalf("Allow()=true but retryAfter=%v, want 0", retryAfter)
		}
		// Invariant: if not allowed, retryAfter must be positive.
		if !allowed && retryAfter <= 0 {
			t.Fatalf("Allow()=false but retryAfter=%v, want >0", retryAfter)
		}
		// Invariant: if attempts < IP limit, should be allowed (unless account limit exceeded).
		if attempts < cfg.IPLimit && (username == "" || attempts < cfg.AcctLimit) && !allowed {
			t.Fatalf("Allow()=false with %d attempts (ip_limit=%d, acct_limit=%d)", attempts, cfg.IPLimit, cfg.AcctLimit)
		}
	})
}
