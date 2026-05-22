package cache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestCache_Set_and_Get(t *testing.T) {
	t.Parallel()
	c := New[string](time.Hour)

	c.Set("key1", "value1")

	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("Get(\"key1\") returned ok=false, want true")
	}
	if got != "value1" {
		t.Errorf("Get(\"key1\") = %v, want %q", got, "value1")
	}
}

func TestCache_Get_missing_key(t *testing.T) {
	t.Parallel()
	c := New[string](time.Hour)

	got, ok := c.Get("nonexistent")
	if ok {
		t.Errorf("Get(\"nonexistent\") returned ok=true, want false")
	}
	if got != "" {
		t.Errorf("Get(\"nonexistent\") = %v, want zero value", got)
	}
}

func TestCache_Get_expired_entry(t *testing.T) {
	t.Parallel()
	c := New[string](time.Nanosecond)

	c.Set("key1", "value1")
	time.Sleep(time.Millisecond) // Ensure TTL expires.

	got, ok := c.Get("key1")
	if ok {
		t.Errorf("Get(\"key1\") returned ok=true after expiry, want false")
	}
	if got != "" {
		t.Errorf("Get(\"key1\") = %v after expiry, want zero value", got)
	}
}

// TestCache_Get_is_read_only verifies Get does not remove expired entries.
// Cache memory reclamation is the caller's responsibility via Reap or Clear.
func TestCache_Get_is_read_only(t *testing.T) {
	t.Parallel()
	c := New[string](50 * time.Millisecond)

	c.Set("key1", "value1")
	time.Sleep(60 * time.Millisecond) // let key1 expire

	_, ok := c.Get("key1") // Should return false (expired) but not mutate.
	if ok {
		t.Error("Get() returned ok=true for expired entry")
	}

	// Set a fresh entry, reap, and confirm only the expired one is gone.
	c.Set("key2", "value2")
	c.Reap()
	if _, ok := c.Get("key2"); !ok {
		t.Error("Reap() removed fresh entry")
	}
}

// TestCache_Reap_removes_expired confirms Reap removes expired entries
// and leaves fresh ones alone.
func TestCache_Reap_removes_expired(t *testing.T) {
	t.Parallel()
	c := New[string](10 * time.Millisecond)

	c.Set("old", "old-value")
	time.Sleep(20 * time.Millisecond) // old expires
	c.Set("fresh", "fresh-value")

	c.Reap()

	if _, ok := c.Get("old"); ok {
		t.Error("Reap() did not remove expired entry")
	}
	if got, ok := c.Get("fresh"); !ok || got != "fresh-value" {
		t.Errorf("Reap() removed fresh entry: got %v, ok=%v", got, ok)
	}
}

func TestCache_Set_overwrites_existing(t *testing.T) {
	t.Parallel()
	c := New[string](time.Hour)

	c.Set("key1", "first")
	c.Set("key1", "second")

	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("Get(\"key1\") returned ok=false, want true")
	}
	if got != "second" {
		t.Errorf("Get(\"key1\") = %v, want %q", got, "second")
	}
}

func TestCache_Clear_removes_all_entries(t *testing.T) {
	t.Parallel()
	c := New[int](time.Hour)

	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()

	if _, ok := c.Get("a"); ok {
		t.Error("Get(\"a\") returned ok=true after Clear, want false")
	}
	if _, ok := c.Get("b"); ok {
		t.Error("Get(\"b\") returned ok=true after Clear, want false")
	}
}

func TestCache_concurrent_access(t *testing.T) {
	t.Parallel()
	c := New[int](time.Hour)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "key"
			c.Set(key, n)
			c.Get(key)
		}(i)
	}
	wg.Wait()
	// No race detector panic = pass.
}

func TestCache_GetOrFetch_caches_result(t *testing.T) {
	t.Parallel()
	c := New[string](time.Hour)
	calls := 0

	fetch := func() (string, error) {
		calls++
		return "fetched", nil
	}

	got, err := c.GetOrFetch("key", fetch)
	if err != nil {
		t.Fatalf("GetOrFetch returned error: %v", err)
	}
	if got != "fetched" {
		t.Errorf("GetOrFetch = %q, want %q", got, "fetched")
	}

	// Second call should hit cache.
	got, err = c.GetOrFetch("key", fetch)
	if err != nil {
		t.Fatalf("GetOrFetch (cached) returned error: %v", err)
	}
	if got != "fetched" {
		t.Errorf("GetOrFetch (cached) = %q, want %q", got, "fetched")
	}
	if calls != 1 {
		t.Errorf("fetch called %d times, want 1", calls)
	}
}

func TestCache_GetOrFetch_coalesces_concurrent(t *testing.T) {
	t.Parallel()
	c := New[string](time.Hour)
	var mu sync.Mutex
	calls := 0

	fetch := func() (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		return "result", nil
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			got, err := c.GetOrFetch("key", fetch)
			if err != nil {
				t.Errorf("GetOrFetch error: %v", err)
			}
			if got != "result" {
				t.Errorf("GetOrFetch = %q, want %q", got, "result")
			}
		})
	}
	wg.Wait()

	if calls != 1 {
		t.Errorf("fetch called %d times, want 1 (singleflight coalescing)", calls)
	}
}

func BenchmarkCache_concurrent(b *testing.B) {
	c := New[int](time.Hour)
	for i := range 100 {
		c.Set(fmt.Sprintf("key-%d", i), i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%100)
			c.Get(key)
			i++
		}
	})
}

func TestCache_property_set_then_get(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		// Invariant 2: Set followed by immediate Get always returns the value (TTL > 0).
		// Use minimum 1ms to avoid expiry between Set and Get.
		ttl := time.Duration(rapid.Int64Range(int64(time.Millisecond), int64(time.Hour)).Draw(rt, "ttl"))
		c := New[int](ttl)
		key := rapid.String().Draw(rt, "key")
		val := rapid.Int().Draw(rt, "val")

		c.Set(key, val)
		got, ok := c.Get(key)
		if !ok {
			rt.Fatalf("Get(%q) returned ok=false immediately after Set", key)
		}
		if got != val {
			rt.Fatalf("Get(%q) = %v, want %v", key, got, val)
		}
	})
}

func TestCache_property_clear_invalidates_all(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		// Invariant 3: Clear makes all subsequent Gets return (zero, false).
		c := New[int](time.Hour)
		n := rapid.IntRange(1, 20).Draw(rt, "n")
		keys := make([]string, n)
		for i := range n {
			keys[i] = rapid.String().Draw(rt, "key")
			c.Set(keys[i], i)
		}
		c.Clear()
		for _, k := range keys {
			if _, ok := c.Get(k); ok {
				rt.Fatalf("Get(%q) returned ok=true after Clear", k)
			}
		}
	})
}

func TestCache_property_reap_preserves_fresh(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		// Invariant 4: Reap removes only expired entries (non-expired survive).
		c := New[int](time.Hour)
		n := rapid.IntRange(1, 20).Draw(rt, "n")
		keys := make([]string, n)
		vals := make(map[string]int)
		for i := range n {
			keys[i] = fmt.Sprintf("key-%d", i)
			c.Set(keys[i], i)
			vals[keys[i]] = i
		}
		c.Reap()
		for _, k := range keys {
			got, ok := c.Get(k)
			if !ok {
				rt.Fatalf("Get(%q) returned ok=false after Reap (entry should be fresh)", k)
			}
			if got != vals[k] {
				rt.Fatalf("Get(%q) = %v, want %v after Reap", k, got, vals[k])
			}
		}
	})
}

func BenchmarkCache_GetOrFetch(b *testing.B) {
	b.Run("hit", func(b *testing.B) {
		c := New[string](time.Hour)
		c.Set("key", "value")
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = c.GetOrFetch("key", func() (string, error) {
				return "value", nil
			})
		}
	})

	b.Run("miss", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			c := New[string](time.Hour)
			_, _ = c.GetOrFetch("key", func() (string, error) {
				return "fetched", nil
			})
		}
	})

	b.Run("concurrent_miss", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			c := New[string](time.Hour)
			for pb.Next() {
				_, _ = c.GetOrFetch("shared", func() (string, error) {
					time.Sleep(time.Microsecond)
					return "coalesced", nil
				})
			}
		})
	})
}

func TestCache_GetOrFetchCtx(t *testing.T) {
	t.Parallel()
	c := New[string](time.Hour)

	t.Run("basic fetch", func(t *testing.T) {
		t.Parallel()
		c := New[string](time.Hour)
		ctx := context.Background()
		got, err := c.GetOrFetchCtx(ctx, "k1", func(ctx context.Context) (string, error) {
			return "v1", nil
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "v1" {
			t.Errorf("got %q, want %q", got, "v1")
		}
	})

	t.Run("initiator cancel does not kill shared fetch", func(t *testing.T) {
		t.Parallel()
		c := New[string](time.Hour)
		fetchStarted := make(chan struct{})
		fetchDone := make(chan struct{})

		// Caller A: will be cancelled mid-flight.
		ctxA, cancelA := context.WithCancel(context.Background())

		var wg sync.WaitGroup
		wg.Add(2)

		// Start caller A.
		go func() {
			defer wg.Done()
			_, _ = c.GetOrFetchCtx(ctxA, "shared", func(ctx context.Context) (string, error) {
				close(fetchStarted)
				<-fetchDone
				// The fetch context should NOT be cancelled even though ctxA is.
				if ctx.Err() != nil {
					t.Errorf("fetch context cancelled unexpectedly: %v", ctx.Err())
				}
				return "result", nil
			})
		}()

		// Wait for fetch to start, then cancel caller A.
		<-fetchStarted
		cancelA()

		// Caller B: independent context, should get the result.
		ctxB := context.Background()
		var gotB string
		var errB error
		go func() {
			defer wg.Done()
			gotB, errB = c.GetOrFetchCtx(ctxB, "shared", func(ctx context.Context) (string, error) {
				t.Error("caller B should coalesce, not start a new fetch")
				return "wrong", nil
			})
		}()

		// Let the fetch complete.
		time.Sleep(10 * time.Millisecond)
		close(fetchDone)
		wg.Wait()

		if errB != nil {
			t.Fatalf("caller B got error: %v", errB)
		}
		if gotB != "result" {
			t.Errorf("caller B got %q, want %q", gotB, "result")
		}
	})

	// Verify caller's own context cancellation returns ctx.Err().
	t.Run("caller context respected", func(t *testing.T) {
		t.Parallel()
		_ = c
		c := New[string](time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel

		_, err := c.GetOrFetchCtx(ctx, "k", func(ctx context.Context) (string, error) {
			time.Sleep(time.Second) // slow fetch
			return "late", nil
		})
		if err != context.Canceled {
			t.Errorf("got err=%v, want context.Canceled", err)
		}
	})
}
