package showskip

import (
	"sync"
	"testing"
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
	c := New(time.Millisecond)
	c.Set("key1", true)
	time.Sleep(5 * time.Millisecond)
	skip, ok := c.Get("key1")
	if ok || skip {
		t.Errorf("Get(expired) = (%v, %v), want (false, false)", skip, ok)
	}
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

func TestCache_Clear(t *testing.T) {
	t.Parallel()
	c := New(time.Minute)
	c.Set("a", true)
	c.Set("b", false)
	c.Clear()
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) after Clear should miss")
	}
	if _, ok := c.Get("b"); ok {
		t.Error("Get(b) after Clear should miss")
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
