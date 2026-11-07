package providercalls

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// hold starts a call that holds a slot until the returned func is called.
func hold(t *testing.T, b *Binding, c Class, done *sync.WaitGroup) func() {
	t.Helper()
	gate := make(chan struct{})
	done.Go(func() {
		_ = b.Call(t.Context(), Search, c, func(context.Context) error { <-gate; return nil })
	})
	synctest.Wait()
	return func() { close(gate) }
}

func TestInteractiveFirstInLimiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		var wg sync.WaitGroup
		free1 := hold(t, b, Automatic, &wg)
		free2 := hold(t, b, Automatic, &wg)
		var mu sync.Mutex
		var order []Class
		for _, c := range []Class{Automatic, Interactive} {
			wg.Go(func() {
				_ = b.Call(t.Context(), Search, c, func(context.Context) error {
					mu.Lock()
					order = append(order, c)
					mu.Unlock()
					return nil
				})
			})
			synctest.Wait()
		}
		if len(order) != 0 {
			t.Fatalf("a third call ran past two held slots: %v", order)
		}
		free1()
		synctest.Wait()
		free2()
		wg.Wait()
		if !slices.Equal(order, []Class{Interactive, Automatic}) {
			t.Errorf("order = %v, want the Interactive waiter first", order)
		}
	})
}

func TestSlotsBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		var inFlight, peak atomic.Int32
		gate := make(chan struct{})
		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() {
				_ = b.Call(t.Context(), Fetch, Interactive, func(context.Context) error {
					n := inFlight.Add(1)
					for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
					}
					<-gate
					inFlight.Add(-1)
					return nil
				})
			})
		}
		synctest.Wait()
		close(gate)
		wg.Wait()
		if got := peak.Load(); got != maxInFlight {
			t.Errorf("peak in flight = %d, want %d", got, maxInFlight)
		}
	})
}

func TestSlotWaitCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		var wg sync.WaitGroup
		free1 := hold(t, b, Interactive, &wg)
		free2 := hold(t, b, Interactive, &wg)
		ctx, cancel := context.WithCancel(t.Context())
		var err error
		wg.Go(func() { err = b.Call(ctx, Search, Interactive, returns(nil)) })
		synctest.Wait()
		cancel()
		synctest.Wait()
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a waiter whose caller left returned %v", err)
		}
		free1()
		free2()
		wg.Wait()
		if s := &b.p.slots; s.free != maxInFlight || len(s.waiting[Interactive]) != 0 {
			t.Errorf("slots after the waits: free %d, waiting %d; want every slot back", s.free, len(s.waiting[Interactive]))
		}
	})
}

func TestScanDelayPacesAutomatic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 5*time.Second)
		start := time.Now()
		var mu sync.Mutex
		var auto []time.Duration
		var interactive time.Duration
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				_ = b.Call(t.Context(), Search, Automatic, func(context.Context) error {
					mu.Lock()
					auto = append(auto, time.Since(start))
					mu.Unlock()
					return nil
				})
			})
		}
		synctest.Wait()
		wg.Go(func() {
			_ = b.Call(t.Context(), Search, Interactive, func(context.Context) error { interactive = time.Since(start); return nil })
		})
		wg.Wait()
		slices.Sort(auto)
		if want := []time.Duration{0, 5 * time.Second, 10 * time.Second, 15 * time.Second}; !slices.Equal(auto, want) {
			t.Errorf("automatic starts = %v, want %v", auto, want)
		}
		if interactive != 0 {
			t.Errorf("interactive started at %v, want undelayed", interactive)
		}
	})
}

func TestPaceCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", time.Minute)
		_ = b.Call(t.Context(), Search, Automatic, returns(nil))
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		called := false
		err := b.Call(ctx, Search, Automatic, func(context.Context) error { called = true; return nil })
		if called || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("a paced call whose caller left: ran %v, err %v", called, err)
		}
		start := time.Now()
		_ = b.Call(t.Context(), Search, Automatic, returns(nil))
		if got := time.Since(start); got != time.Minute-time.Second {
			t.Errorf("the next paced call waited %v, want the cancelled reservation returned (59s)", got)
		}
	})
}
