package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// fetch is GetOrFetch of v for key, reporting whether fn ran.
func fetch(t *testing.T, c *Cache[[]int], key string, v []int) (got []int, called bool) {
	t.Helper()
	got, err := c.GetOrFetch(t.Context(), key, func(context.Context) ([]int, error) { called = true; return v, nil })
	if err != nil {
		t.Fatal(err)
	}
	return got, called
}

func TestCacheTTLs(t *testing.T) {
	cases := []struct {
		name  string
		value []int
		ttl   time.Duration
	}{
		{name: "hit", value: []int{7}, ttl: 6 * time.Hour},
		{name: "empty", value: nil, ttl: 15 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := New(NonEmpty[int])
				fetch(t, c, "k", tc.value)
				time.Sleep(tc.ttl - time.Second)
				if _, called := fetch(t, c, "k", tc.value); called {
					t.Errorf("fetched again before %v", tc.ttl)
				}
				time.Sleep(time.Second)
				if _, called := fetch(t, c, "k", tc.value); !called {
					t.Errorf("still kept at %v", tc.ttl)
				}
			})
		})
	}
}

func TestCacheBound(t *testing.T) {
	t.Run("expired_first", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c := New(NonEmpty[int])
			for i := range maxEntries {
				v := []int{i}
				if i%2 == 0 {
					v = nil
				}
				fetch(t, c, strconv.Itoa(i), v)
			}
			time.Sleep(emptyTTL)
			fetch(t, c, "new", []int{1})
			if got, want := len(c.entries), maxEntries/2+1; got != want {
				t.Errorf("entries = %d, want %d (the expired half dropped, no hit)", got, want)
			}
			if _, called := fetch(t, c, "1", []int{1}); called {
				t.Error("a live hit went with the expired entries")
			}
		})
	})
	t.Run("least_recently_used", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c := New(NonEmpty[int])
			for i := range maxEntries {
				fetch(t, c, strconv.Itoa(i), []int{i})
			}
			fetch(t, c, "0", nil)
			fetch(t, c, "new", []int{1})
			if got := len(c.entries); got != maxEntries {
				t.Errorf("entries = %d, want %d", got, maxEntries)
			}
			if _, called := fetch(t, c, "0", []int{0}); called {
				t.Error("the just-used oldest key was dropped")
			}
			if _, called := fetch(t, c, "1", []int{1}); !called {
				t.Error("the least recently used key was kept")
			}
			if got := len(c.entries); got > maxEntries {
				t.Errorf("entries = %d, over %d", got, maxEntries)
			}
		})
	})
}

// TestGetOrFetchNilInterface: an interface-typed cache answers a nil value
// as the zero value, kept as an empty answer.
func TestGetOrFetchNilInterface(t *testing.T) {
	c := New(func(v error) bool { return v != nil })
	for range 2 {
		got, err := c.GetOrFetch(t.Context(), "k", func(context.Context) (error, error) { return nil, nil })
		if got != nil || err != nil {
			t.Errorf("GetOrFetch = %v, %v; want nil, nil", got, err)
		}
	}
}

func TestGetOrFetchCoalesces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(NonEmpty[int])
		var calls atomic.Int32
		release := make(chan struct{})
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				_, _ = c.GetOrFetch(t.Context(), "k", func(context.Context) ([]int, error) {
					calls.Add(1)
					<-release
					return []int{1}, nil
				})
			})
		}
		synctest.Wait()
		close(release)
		wg.Wait()
		if n := calls.Load(); n != 1 {
			t.Errorf("fn ran %d times, want 1", n)
		}
	})
}

// outcome is what one GetOrFetch caller observed; panicked and stack come
// from the panic it re-raised.
type outcome struct {
	panicked any
	err      error
	got      []int
	stack    []byte
}

func callRecovering(ctx context.Context, c *Cache[[]int], fn func(context.Context) ([]int, error)) (o outcome) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		o.panicked = r
		if p, ok := r.(*panicError); ok {
			o.panicked, o.stack = p.value, p.stack
		}
	}()
	o.got, o.err = c.GetOrFetch(ctx, "k", fn)
	return o
}

func (o outcome) String() string {
	return fmt.Sprintf("{panicked %v (stack %d bytes), err %v, got %v}", o.panicked, len(o.stack), o.err, o.got)
}

func (o outcome) matches(want outcome) bool {
	if want.panicked != nil {
		return o.panicked == want.panicked && bytes.Contains(o.stack, []byte("TestFlightLifecycle")) && o.err == nil
	}
	return o.panicked == nil && errors.Is(o.err, want.err) && slices.Equal(o.got, want.got)
}

// lockedBuffer is a log sink the fetch goroutine writes while the test reads.
type lockedBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestFlightLifecycle: two callers share one fetch, the first optionally
// leaving before it ends, or both leaving before an fn that ignores its
// context panics. Each caller sees the fetch's end its own way, a panic no
// caller saw is logged, no flight stays listed, and a later call fetches
// again unless a value was kept.
func TestFlightLifecycle(t *testing.T) {
	boom := errors.New("boom")
	const panicValue = "fetch failed"
	succeed := func(context.Context) ([]int, error) { return []int{1}, nil }
	panics := func(context.Context) ([]int, error) { panic(panicValue) }
	cases := []struct {
		name          string
		fn            func(context.Context) ([]int, error)
		first, second outcome
		cancelFirst   bool
		allLeave      bool
		kept          bool
	}{
		{name: "success", fn: succeed, first: outcome{got: []int{1}}, second: outcome{got: []int{1}}, kept: true},
		{
			name:  "error",
			fn:    func(context.Context) ([]int, error) { return nil, boom },
			first: outcome{err: boom}, second: outcome{err: boom},
		},
		{name: "panic", fn: panics, first: outcome{panicked: panicValue}, second: outcome{panicked: panicValue}},
		{
			name:  "goexit",
			fn:    func(context.Context) ([]int, error) { runtime.Goexit(); return nil, nil },
			first: outcome{err: errFetchExited}, second: outcome{err: errFetchExited},
		},
		{
			name: "cancel_while_another_waits", fn: succeed, cancelFirst: true,
			first: outcome{err: context.Canceled}, second: outcome{got: []int{1}}, kept: true,
		},
		{
			name: "cancel_before_a_panic", fn: panics, cancelFirst: true,
			first: outcome{err: context.Canceled}, second: outcome{panicked: panicValue},
		},
		{
			name: "every_caller_left_before_a_panic", fn: panics, allLeave: true,
			first: outcome{err: context.Canceled}, second: outcome{err: context.Canceled},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := &lockedBuffer{}
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })
			synctest.Test(t, func(t *testing.T) {
				c := New(NonEmpty[int])
				release := make(chan struct{})
				var runs atomic.Int32
				fn := func(ctx context.Context) ([]int, error) {
					runs.Add(1)
					if tc.allLeave {
						<-release
						return tc.fn(ctx)
					}
					select {
					case <-release:
						return tc.fn(ctx)
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				firstCtx, cancel := context.WithCancel(t.Context())
				defer cancel()
				secondCtx, cancelSecond := context.WithCancel(t.Context())
				defer cancelSecond()
				var first, second outcome
				var wg sync.WaitGroup
				wg.Go(func() { first = callRecovering(firstCtx, c, fn) })
				synctest.Wait()
				wg.Go(func() { second = callRecovering(secondCtx, c, fn) })
				synctest.Wait()
				if tc.allLeave {
					cancel()
					cancelSecond()
					wg.Wait()
				}
				if tc.cancelFirst {
					cancel()
					synctest.Wait()
					if !first.matches(tc.first) {
						t.Errorf("first caller before the fetch ended = %v; want %v", first, tc.first)
					}
				}
				close(release)
				wg.Wait()
				synctest.Wait()
				logged := logs.String()
				if got := strings.Contains(logged, "cache: a fetch panicked after every caller left"); got != tc.allLeave {
					t.Errorf("unseen-panic log line written = %v; want %v (log %q)", got, tc.allLeave, logged)
				}
				if tc.allLeave && (!strings.Contains(logged, "panic=\"fetch failed\"") || !strings.Contains(logged, "TestFlightLifecycle")) {
					t.Errorf("unseen-panic log = %q; want the panic value and the fetch's stack", logged)
				}
				if !first.matches(tc.first) {
					t.Errorf("first caller = %v; want %v", first, tc.first)
				}
				if !second.matches(tc.second) {
					t.Errorf("second caller = %v; want %v", second, tc.second)
				}
				if n := runs.Load(); n != 1 {
					t.Errorf("fn ran %d times, want 1", n)
				}
				if n := len(c.flights); n != 0 {
					t.Errorf("%d flights still listed after the fetch ended", n)
				}
				if _, called := fetch(t, c, "k", []int{2}); called == tc.kept {
					t.Errorf("a later call fetched again = %v; want %v", called, !tc.kept)
				}
			})
		})
	}
}

// TestCallerTakesEndedFetch: a caller whose ctx ends as the fetch finishes
// takes the fetch's result, since finish counted it as still waiting. Both
// select arms are ready, so the rounds cover the ctx arm.
func TestCallerTakesEndedFetch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 64 {
		c := New(NonEmpty[int])
		f := &flight[[]int]{done: make(chan struct{}), cancel: func() {}, value: []int{3}}
		close(f.done)
		c.flights["k"] = f
		got, err := c.GetOrFetch(ctx, "k", func(context.Context) ([]int, error) { return nil, nil })
		if err != nil || !slices.Equal(got, []int{3}) {
			t.Fatalf("GetOrFetch on an ended fetch = %v, %v; want [3], nil", got, err)
		}
	}
}

// TestGetOrFetchLastCallerEndsFetch: the fetch's context ends once its only
// caller has left, and a call after that starts a fetch of its own rather
// than joining the ending one.
func TestGetOrFetchLastCallerEndsFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(NonEmpty[int])
		release := make(chan struct{})
		var sawEnd atomic.Bool
		ctx, cancel := context.WithCancel(t.Context())
		var first error
		var wg sync.WaitGroup
		wg.Go(func() {
			_, first = c.GetOrFetch(ctx, "k", func(ctx context.Context) ([]int, error) {
				<-ctx.Done()
				sawEnd.Store(true)
				<-release
				return nil, ctx.Err()
			})
		})
		synctest.Wait()
		cancel()
		synctest.Wait()
		if !errors.Is(first, context.Canceled) || !sawEnd.Load() {
			t.Errorf("caller err = %v, fetch saw its end %v; want context.Canceled, true", first, sawEnd.Load())
		}
		got, called := fetch(t, c, "k", []int{2})
		close(release)
		wg.Wait()
		if !called || len(got) != 1 || got[0] != 2 {
			t.Errorf("call after the last caller left: %v, fetched %v; want its own [2]", got, called)
		}
	})
}

func BenchmarkGetOrFetch(b *testing.B) {
	c := New(NonEmpty[int])
	fn := func(context.Context) ([]int, error) { return []int{1}, nil }
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = c.GetOrFetch(b.Context(), strconv.Itoa(i%64), fn)
			i++
		}
	})
}
