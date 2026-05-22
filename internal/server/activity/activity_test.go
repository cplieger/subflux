package activity

import (
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestActivityLog_exported_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		log := New(5)
		var activeIDs []string

		ops := rapid.IntRange(1, 30).Draw(t, "ops")
		for range ops {
			op := rapid.IntRange(0, 5).Draw(t, "op")
			switch op {
			case 0: // Start
				id := log.Start("scan", "detail", "manual")
				activeIDs = append(activeIDs, id)
			case 1: // End
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.End(activeIDs[idx])
				}
			case 2: // Fail
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.Fail(activeIDs[idx])
				}
			case 3: // Dismiss
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.Dismiss(activeIDs[idx])
				}
			case 4: // PruneCompleted
				log.PruneCompleted(0)
			case 5: // Cancel
				if len(activeIDs) > 0 {
					idx := rapid.IntRange(0, len(activeIDs)-1).Draw(t, "idx")
					log.Cancel(activeIDs[idx])
				}
			}
		}

		// Invariant 1: Len() <= maxItems.
		entries := log.Entries()
		if len(entries) > 5 {
			t.Fatalf("Entries() len = %d, exceeds maxItems=5", len(entries))
		}

		// Invariant 2: unique IDs.
		seen := make(map[string]bool)
		for _, e := range entries {
			if seen[e.ID] {
				t.Fatalf("duplicate ID %q", e.ID)
			}
			seen[e.ID] = true
		}
	})
}

func TestActivityLog_concurrent_exported(t *testing.T) {
	t.Parallel()
	log := New(10)
	var wg sync.WaitGroup
	done := make(chan struct{})

	// 4 goroutines: Start → Progress → End loop.
	for range 4 {
		wg.Go(func() {
			for i := range 50 {
				id := log.Start("scan", "ep", "scheduled")
				log.Progress(id, i, 50, "working")
				log.End(id)
			}
		})
	}

	// 2 goroutines: Dismiss completed entries.
	for range 2 {
		wg.Go(func() {
			for range 100 {
				entries := log.Entries()
				for _, e := range entries {
					if e.Done {
						log.Dismiss(e.ID)
					}
				}
			}
		})
	}

	// 2 goroutines: PruneCompleted.
	for range 2 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					log.PruneCompleted(0)
				}
			}
		})
	}

	// 1 goroutine: Cancel + IsCancelled.
	wg.Go(func() {
		for range 100 {
			entries := log.Entries()
			for _, e := range entries {
				if !e.Done {
					log.Cancel(e.ID)
					log.IsCancelled(e.ID)
				}
			}
		}
	})

	// 1 goroutine: Entries snapshot reads.
	wg.Go(func() {
		for range 200 {
			_ = log.Entries()
		}
	})

	// Wait for the main workers, then signal prune goroutines.
	time.AfterFunc(200*time.Millisecond, func() { close(done) })
	wg.Wait()

	// Final invariant: no panic, Entries() <= maxItems.
	if len(log.Entries()) > 10 {
		t.Fatalf("final Entries() len = %d, exceeds maxItems=10", len(log.Entries()))
	}
}

func BenchmarkActivityLog_StartEnd(b *testing.B) {
	log := New(100)
	b.ReportAllocs()
	for b.Loop() {
		id := log.Start("scan", "bench", "scheduled")
		log.End(id)
	}
}
