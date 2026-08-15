package showskip

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestCache_Get_miss(t *testing.T) {
	t.Parallel()
	c := New(time.Minute)
	skip, ok := c.Get("nonexistent")
	if ok || skip {
		t.Errorf("Get(miss) = (%v, %v), want (false, false)", skip, ok)
	}
}

func TestCache_Get_hit(t *testing.T) {
	t.Parallel()
	c := New(time.Minute)
	c.Set("key1", true)
	skip, ok := c.Get("key1")
	if !ok || !skip {
		t.Errorf("Get(hit) = (%v, %v), want (true, true)", skip, ok)
	}
}

func TestCache_Get_expired(t *testing.T) {
	t.Parallel()
	// Fake clock: the TTL can be a realistic hour instead of a 1ms hack, the
	// advance is exact rather than "5ms should be enough", and it costs no
	// wall-clock time.
	synctest.Test(t, func(t *testing.T) {
		c := New(time.Hour)
		c.Set("key1", true)
		time.Sleep(time.Hour + time.Second)
		skip, ok := c.Get("key1")
		if ok || skip {
			t.Errorf("Get(expired) = (%v, %v), want (false, false)", skip, ok)
		}
	})
}

func TestCache_Set_overwrites(t *testing.T) {
	t.Parallel()
	c := New(time.Minute)
	c.Set("key1", true)
	c.Set("key1", false)
	skip, ok := c.Get("key1")
	if !ok || skip {
		t.Errorf("Get(overwritten) = (%v, %v), want (false, true)", skip, ok)
	}
}

func TestCache_concurrent(t *testing.T) {
	t.Parallel()
	c := New(time.Minute)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			key := "k"
			if i%2 == 0 {
				c.Set(key, true)
			} else {
				c.Get(key)
			}
		})
	}
	wg.Wait()
}

func TestCache_Prune_removes_expired(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New(time.Hour)
		c.Set("a", true)
		c.Set("b", false)
		time.Sleep(time.Hour + time.Second)

		c.Prune()

		if len(c.entries) != 0 {
			t.Errorf("after Prune of expired entries, len(entries) = %d, want 0", len(c.entries))
		}
	})
}

func TestCache_Prune_keeps_live(t *testing.T) {
	t.Parallel()
	c := New(time.Hour)
	c.Set("a", true)
	c.Set("b", false)

	c.Prune()

	if len(c.entries) != 2 {
		t.Errorf("after Prune of live entries, len(entries) = %d, want 2", len(c.entries))
	}
	if skip, ok := c.Get("a"); !ok || !skip {
		t.Errorf("Get(a) after Prune = (%v, %v), want (true, true)", skip, ok)
	}
}
