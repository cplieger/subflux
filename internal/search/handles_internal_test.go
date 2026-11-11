package search

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fill holds n handles for owner in searches of size each.
func fill(t *testing.T, s *handleStore, owner int64, n, size int, now time.Time) []Handle {
	t.Helper()
	var out []Handle
	for n > 0 {
		k := min(size, n)
		hs := make([]*held, k)
		for i := range hs {
			hs[i] = &held{}
		}
		ids, err := s.insert(owner, hs, now)
		if err != nil {
			t.Fatalf("fill: %v", err)
		}
		out = append(out, ids...)
		n -= k
	}
	return out
}

func TestSearchCapacityKeepsOldHandles(t *testing.T) {
	s := newHandleStore()
	now := time.Now()
	for o := range int64(19) {
		fill(t, &s, 100+o, 250, 50, now)
	}
	fill(t, &s, 200, 10, 10, now)
	old := fill(t, &s, 1, 240, 10, now)
	if len(s.byHandle) != maxHandles {
		t.Fatalf("%d handles held, want %d", len(s.byHandle), maxHandles)
	}
	hs := make([]*held, 50)
	for i := range hs {
		hs[i] = &held{}
	}
	if _, err := s.insert(1, hs, now); !errors.Is(err, ErrSearchCapacity) {
		t.Fatalf("insert = %v, want ErrSearchCapacity", err)
	}
	for _, h := range old {
		if _, ok := s.take(1, h, now); !ok {
			t.Fatalf("old handle %s gone after a refused search", h)
		}
	}
}

func TestPerOwnerHandleBound(t *testing.T) {
	s := newHandleStore()
	now := time.Now()
	first := fill(t, &s, 1, 50, 50, now)
	fill(t, &s, 1, 200, 50, now)
	last := fill(t, &s, 1, 50, 50, now)
	if s.owned[1] != maxHandlesPerOwner || len(s.byHandle) != maxHandlesPerOwner {
		t.Errorf("owner holds %d of %d, want %d", s.owned[1], len(s.byHandle), maxHandlesPerOwner)
	}
	if _, ok := s.take(1, first[0], now); ok {
		t.Error("the oldest search's handle survived the owner's bound")
	}
	if _, ok := s.take(1, last[0], now); !ok {
		t.Error("the new search's handle is missing")
	}
}

func TestSearchCapacity(t *testing.T) {
	t.Run("sequential", func(t *testing.T) {
		s := newHandleStore()
		now := time.Now()
		for o := range int64(20) {
			fill(t, &s, o, 249, 50, now)
		}
		refused := 0
		for o := range int64(30) {
			if _, err := s.insert(100+o, []*held{{}}, now); errors.Is(err, ErrSearchCapacity) {
				refused++
			}
		}
		if len(s.byHandle) != maxHandles || refused != 10 {
			t.Errorf("%d held, %d refused; want %d and 10", len(s.byHandle), refused, maxHandles)
		}
	})
	t.Run("concurrent", func(t *testing.T) {
		s := newHandleStore()
		now := time.Now()
		for o := range int64(20) {
			fill(t, &s, o, 249, 50, now)
		}
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			refused int
		)
		for o := range int64(30) {
			wg.Go(func() {
				if _, err := s.insert(100+o, []*held{{}, {}}, now); errors.Is(err, ErrSearchCapacity) {
					mu.Lock()
					refused++
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if len(s.byHandle) != maxHandles || refused != 20 {
			t.Errorf("%d held, %d refused; want %d and 20", len(s.byHandle), refused, maxHandles)
		}
	})
	t.Run("expired handles make room", func(t *testing.T) {
		s := newHandleStore()
		now := time.Now()
		for o := range int64(20) {
			fill(t, &s, o, 250, 50, now)
		}
		if !s.room(99, 1, now.Add(handleTTL)) {
			t.Error("no room once every handle expired")
		}
		if len(s.byHandle) != 0 || len(s.owned) != 0 {
			t.Errorf("%d handles and %d owners after the sweep", len(s.byHandle), len(s.owned))
		}
	})
}

func TestHandleTakeAndGiveBack(t *testing.T) {
	s := newHandleStore()
	now := time.Now()
	h := fill(t, &s, 1, 1, 1, now)[0]
	if len(h) != 22 {
		t.Errorf("handle %q, want 22 base64url characters", h)
	}
	c, ok := s.take(1, h, now)
	if !ok {
		t.Fatal("take failed")
	}
	if _, ok := s.take(1, h, now); ok {
		t.Error("a taken handle was taken twice")
	}
	s.giveBack(c)
	if _, ok := s.take(1, h, now); !ok {
		t.Error("a handle given back did not take")
	}
	s.consume(h)
	if _, ok := s.take(1, h, now); ok || s.owned[1] != 0 {
		t.Errorf("a consumed handle is held (owner count %d)", s.owned[1])
	}
}
