package effectqueue

import (
	"slices"
	"sync"
	"testing"
)

func TestDrain_runs_effects_in_the_order_they_were_added(t *testing.T) {
	var q Queue
	var got []int
	q.Add(func() { got = append(got, 1) }, func() { got = append(got, 2) })
	q.Add(func() { got = append(got, 3) })
	q.Drain()
	if want := []int{1, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("Drain ran %v, want %v", got, want)
	}
	q.Drain()
	if len(got) != 3 {
		t.Errorf("a second Drain ran %v, want nothing more", got[3:])
	}
}

// A batch added while another goroutine drains runs on that goroutine, after
// the effect it is draining, and its own Drain returns without running it.
func TestDrain_a_batch_added_mid_drain_runs_after_the_running_effect(t *testing.T) {
	var q Queue
	var mu sync.Mutex
	var got []string
	record := func(s string) func() {
		return func() {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, s)
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	q.Add(func() {
		close(entered)
		<-release
		record("first")()
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		q.Drain()
	}()
	<-entered

	q.Add(record("second"))
	q.Drain()
	mu.Lock()
	early := slices.Clone(got)
	mu.Unlock()
	if len(early) != 0 {
		t.Errorf("Drain during another drain ran %v, want nothing", early)
	}

	close(release)
	<-done
	if want := []string{"first", "second"}; !slices.Equal(got, want) {
		t.Errorf("effects ran %v, want %v", got, want)
	}
}

func TestDrain_a_panicking_effect_leaves_the_rest_for_the_next_drain(t *testing.T) {
	var q Queue
	var got []int
	q.Add(func() { got = append(got, 1) }, func() { panic("effect") }, func() { got = append(got, 3) })
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Drain did not propagate the effect's panic")
			}
		}()
		q.Drain()
	}()
	if want := []int{1}; !slices.Equal(got, want) {
		t.Fatalf("before the panic Drain ran %v, want %v", got, want)
	}
	q.Add(func() { got = append(got, 4) })
	q.Drain()
	if want := []int{1, 3, 4}; !slices.Equal(got, want) {
		t.Errorf("after the panic Drain ran %v, want %v", got, want)
	}
}
