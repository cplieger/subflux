package syncsched

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

func TestSlotOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newSlot(newMetrics())
		ctx := context.Background()
		release, err := s.acquire(ctx, classBatch) // a batch file runs
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var order []string
		queue := func(name string, c class) {
			go func() {
				rel, err := s.acquire(ctx, c)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				rel()
			}()
			synctest.Wait()
		}
		queue("batch 1", classBatch)
		queue("automatic 1", classAutomatic)
		queue("batch 2", classBatch)
		queue("automatic 2", classAutomatic)
		queue("interactive", classInteractive)
		release()
		synctest.Wait()
		want := []string{"interactive", "automatic 1", "batch 1", "automatic 2", "batch 2"}
		if !slices.Equal(order, want) {
			t.Errorf("grant order %q, want %q", order, want)
		}
		if again, err := s.acquire(ctx, classAutomatic); err != nil {
			t.Errorf("the drained slot: %v, want it free", err)
		} else {
			again()
		}
	})
}

// free reports whether s grants class c at once.
func free(t *testing.T, s *slot) bool {
	t.Helper()
	got := make(chan struct{})
	go func() {
		rel, err := s.acquire(context.Background(), classBatch)
		if err != nil {
			t.Error(err)
			return
		}
		rel()
		close(got)
	}()
	synctest.Wait()
	select {
	case <-got:
		return true
	default:
		return false
	}
}

func TestSlotWaiterCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newSlot(newMetrics())
		release, err := s.acquire(context.Background(), classAutomatic)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error)
		go func() {
			_, err := s.acquire(ctx, classInteractive)
			done <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled waiter: %v, want context.Canceled", err)
		}
		release()
		if !free(t, s) {
			t.Error("the slot stayed taken after its holder released and its waiter left")
		}
	})
}

// A waiter granted the slot as its context ends either takes it or passes
// it on; the slot is never left held by no one.
func TestSlotGrantedAsWaiterCancels(t *testing.T) {
	for range 20 {
		synctest.Test(t, func(t *testing.T) {
			s := newSlot(newMetrics())
			release, err := s.acquire(context.Background(), classAutomatic)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan func())
			go func() {
				rel, _ := s.acquire(ctx, classInteractive)
				done <- rel
			}()
			synctest.Wait()
			cancel()
			release()
			if rel := <-done; rel != nil {
				rel()
			}
			if !free(t, s) {
				t.Fatal("the slot is held by no one")
			}
		})
	}
}

func TestSlotReleaseOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newSlot(newMetrics())
		first, err := s.acquire(context.Background(), classAutomatic)
		if err != nil {
			t.Fatal(err)
		}
		second := make(chan func())
		go func() {
			rel, err := s.acquire(context.Background(), classAutomatic)
			if err != nil {
				t.Error(err)
			}
			second <- rel
		}()
		synctest.Wait()
		first()
		rel := <-second
		first()
		if free(t, s) {
			t.Error("a second release by the first holder freed the slot the second holds")
		}
		rel()
		synctest.Wait()
	})
}
