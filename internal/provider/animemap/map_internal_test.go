package animemap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/provider"
)

// holdRefresh makes every refresh wait for its end, then take wind to
// return, counting the attempts begun and returned.
func holdRefresh(t *testing.T, wind time.Duration) (begun, returned *atomic.Int32) {
	begun, returned = new(atomic.Int32), new(atomic.Int32)
	prev := attemptFn
	attemptFn = func(ctx context.Context, _ provider.Client, _ string, _ func(string) error) outcome {
		begun.Add(1)
		<-ctx.Done()
		time.Sleep(wind)
		returned.Add(1)
		return outcome{err: ctx.Err()}
	}
	t.Cleanup(func() { attemptFn = prev })
	return begun, returned
}

// TestCloseJoinsRefresh: Close ends the refresh in flight and returns only
// once its goroutine has, the ended attempt reports nothing, and an Index
// call after Close starts none.
func TestCloseJoinsRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		begun, returned := holdRefresh(t, time.Second)
		m := New()
		var reports atomic.Int32
		m.Report("animetosho", func(string, bool) { reports.Add(1) })
		var wg sync.WaitGroup
		wg.Go(func() { m.Index(t.Context(), provider.Client{}) })
		synctest.Wait()
		if begun.Load() != 1 {
			t.Fatalf("%d refreshes begun, want 1", begun.Load())
		}
		start := time.Now()
		if err := m.Close(t.Context()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if took := time.Since(start); returned.Load() != 1 || took != time.Second {
			t.Errorf("Close returned after %v with %d refreshes returned; want after the refresh's 1s wind-down", took, returned.Load())
		}
		wg.Wait()
		if n := reports.Load(); n != 1 {
			t.Errorf("%d reports, want only Report's own: the ended attempt reports nothing", n)
		}
		if x := m.Index(t.Context(), provider.Client{}); x != nil || begun.Load() != 1 {
			t.Errorf("Index after Close: %v, %d refreshes begun; want the held nil index and none begun", x, begun.Load())
		}
		if err := m.Close(t.Context()); err != nil {
			t.Errorf("a second Close: %v", err)
		}
	})
}

// TestFinishEndsFlight: finish closes the flight it clears before it returns,
// so a Close that finds no flight has no refresh goroutine left to wait for.
func TestFinishEndsFlight(t *testing.T) {
	for name, closed := range map[string]bool{"open": false, "closed": true} {
		t.Run(name, func(t *testing.T) {
			m := New()
			done := make(chan struct{})
			m.flight, m.cancel, m.closed = done, func() {}, closed
			m.finish(&outcome{})
			select {
			case <-done:
			default:
				t.Error("finish cleared the flight and left it open")
			}
		})
	}
}

// TestCloseIdle: Close with no refresh in flight returns at once.
func TestCloseIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		if err := New().Close(t.Context()); err != nil || time.Since(start) != 0 {
			t.Errorf("Close: %v after %v, want nil at once", err, time.Since(start))
		}
	})
}

// TestCloseBoundedByContext: a refresh slower to wind down than Close's
// context keeps Close waiting only until that context ends.
func TestCloseBoundedByContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		holdRefresh(t, time.Minute)
		m := New()
		var wg sync.WaitGroup
		wg.Go(func() { m.Index(t.Context(), provider.Client{}) })
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := m.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Close: %v, want its context's DeadlineExceeded", err)
		}
		wg.Wait()
		time.Sleep(time.Minute)
	})
}
