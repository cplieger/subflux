package sessionbatch

import (
	"context"
	"sync"
	"testing"
	"time"
)

type mockDB struct {
	mu      sync.Mutex
	calls   int
	hashes  [][]string
	lastNow time.Time
}

func (m *mockDB) BatchUpdateSessionActivity(_ context.Context, hashes []string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.hashes = append(m.hashes, hashes)
	m.lastNow = now
	return nil
}

func TestBatcher_flushes_on_stop(t *testing.T) {
	t.Parallel()
	db := &mockDB{}
	b := New(context.Background(), db)
	now := time.Now()
	b.Send("hash1", now)
	b.Send("hash2", now.Add(time.Second))
	b.Stop()

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.calls == 0 {
		t.Fatal("expected at least one flush call")
	}
	total := 0
	for _, h := range db.hashes {
		total += len(h)
	}
	if total != 2 {
		t.Fatalf("total hashes = %d, want 2", total)
	}
}

func TestBatcher_flushes_at_max_batch(t *testing.T) {
	t.Parallel()
	db := &mockDB{}
	b := New(context.Background(), db)
	now := time.Now()
	// Send 16 (maxBatch) to trigger immediate flush.
	for i := range 16 {
		b.Send("h"+string(rune('a'+i)), now)
	}
	// Give the goroutine time to process.
	time.Sleep(50 * time.Millisecond)
	b.Stop()

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.calls < 1 {
		t.Fatalf("calls = %d, want >= 1", db.calls)
	}
}

func TestBatcher_drop_when_full(t *testing.T) {
	t.Parallel()
	db := &mockDB{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so flushes error but don't block
	b := New(ctx, db)
	// Fill beyond channel capacity (64) — should not block.
	for range 100 {
		b.Send("overflow", time.Now())
	}
	b.Stop()
}
