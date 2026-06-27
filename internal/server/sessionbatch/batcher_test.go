package sessionbatch_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/server/sessionbatch"
)

// maxBatch and chanBuf mirror sessionbatch's internal flush threshold and
// channel capacity. They are part of the documented contract ("flushes when 16
// updates accumulate"; the send buffer drops beyond its capacity). If either
// internal value changes, these tests should fail and be updated deliberately.
const (
	maxBatch = 16
	chanBuf  = 64
)

// flushCall records one batch write the fake was asked to perform.
type flushCall struct {
	hashes []string
	now    time.Time
}

// fakeUpdater is a hand-written BatchUpdater that records every batch the
// Batcher asks it to write. It is safe for concurrent use by the run goroutine
// and the test goroutine. The flushed channel is a best-effort wakeup hint; the
// recorded calls are the source of truth.
type fakeUpdater struct {
	mu      sync.Mutex
	calls   []flushCall
	flushed chan struct{} // signalled (best-effort) after each recorded call
	entered chan struct{} // signalled (best-effort) when a call begins (opt-in)
	block   chan struct{} // when non-nil, each call blocks until it is closed
}

func newFakeUpdater() *fakeUpdater {
	return &fakeUpdater{flushed: make(chan struct{}, 1024)}
}

// BatchUpdateSessionActivity records the batch it is given. tokenHashes is
// copied because the caller is free to reuse its backing array.
func (f *fakeUpdater) BatchUpdateSessionActivity(_ context.Context, tokenHashes []string, now time.Time) error {
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	f.calls = append(f.calls, flushCall{hashes: slices.Clone(tokenHashes), now: now})
	f.mu.Unlock()
	select {
	case f.flushed <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeUpdater) snapshot() []flushCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// flushedHashes returns every flushed hash, concatenated in flush order.
func (f *fakeUpdater) flushedHashes() []string {
	out := []string{}
	for _, c := range f.snapshot() {
		out = append(out, c.hashes...)
	}
	return out
}

func (f *fakeUpdater) totalFlushed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += len(c.hashes)
	}
	return n
}

func hashName(i int) string {
	return "hash-" + strconv.Itoa(i)
}

// waitForFlushedItems blocks until the fake has recorded at least n flushed
// hashes in total, then returns; it fails the test on timeout. It wakes on each
// flush rather than spinning on a timer, so it returns promptly once the target
// is reached and never sleeps blindly.
func waitForFlushedItems(t *testing.T, f *fakeUpdater, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if f.totalFlushed() >= n {
			return
		}
		select {
		case <-f.flushed:
		case <-deadline:
			t.Fatalf("timed out after %s waiting for %d flushed items; got %d", timeout, n, f.totalFlushed())
		}
	}
}

// Sending one full batch plus one extra proves the size threshold (not the
// 500ms timer) triggers the flush: the first 16 are written as a single batch
// while the 17th stays pending. A timer-only or eager implementation produces a
// different first batch.
func TestBatcher_flushesWhenSizeThresholdReached(t *testing.T) {
	f := newFakeUpdater()
	b := sessionbatch.New(context.Background(), f)
	t.Cleanup(b.Stop)

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := make([]string, maxBatch)
	for i := range maxBatch + 1 {
		h := hashName(i)
		if i < maxBatch {
			want[i] = h
		}
		b.Send(h, at)
	}

	waitForFlushedItems(t, f, maxBatch, 2*time.Second)

	calls := f.snapshot()
	if len(calls) == 0 {
		t.Fatal("no flush recorded after sending a full batch")
	}
	for i, c := range calls {
		if len(c.hashes) > maxBatch {
			t.Errorf("flush %d wrote %d hashes, want <= %d (size threshold must cap a batch)", i, len(c.hashes), maxBatch)
		}
	}
	if !slices.Equal(calls[0].hashes, want) {
		t.Errorf("first flush = %v, want the first %d sent in order %v", calls[0].hashes, maxBatch, want)
	}
}

// A partial batch (fewer than maxBatch) must still be flushed by the periodic
// timer without any Stop. If the timer flush is removed, these never flush and
// the wait times out.
func TestBatcher_flushesPartialBatchOnTimer(t *testing.T) {
	f := newFakeUpdater()
	b := sessionbatch.New(context.Background(), f)
	t.Cleanup(b.Stop)

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := []string{hashName(0), hashName(1), hashName(2)}
	for _, h := range want {
		b.Send(h, at)
	}

	// No Stop: the only thing that can flush a sub-threshold batch is the timer.
	waitForFlushedItems(t, f, len(want), 2*time.Second)

	if got := f.flushedHashes(); !slices.Equal(got, want) {
		t.Errorf("timer flush = %v, want %v", got, want)
	}
}

// Updates still buffered when Stop is called must be drained and flushed, not
// lost. Sent and stopped within the timer interval so the drain path (not the
// timer) is exercised.
func TestBatcher_drainsRemainingOnStop(t *testing.T) {
	f := newFakeUpdater()
	b := sessionbatch.New(context.Background(), f)

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := make([]string, 5)
	for i := range want {
		want[i] = hashName(i)
		b.Send(want[i], at)
	}

	b.Stop() // closes the batcher and waits for the run goroutine to drain.

	if got := f.flushedHashes(); !slices.Equal(got, want) {
		t.Errorf("after Stop, flushed = %v, want all %d drained in order %v", got, len(want), want)
	}
}

// A flush preserves send order and reports the most recent activity time across
// the batch. The peak time is the middle item so neither a first-only nor a
// last-only timestamp bug passes.
func TestBatcher_flushPreservesOrderAndReportsLatestTime(t *testing.T) {
	f := newFakeUpdater()
	b := sessionbatch.New(context.Background(), f)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b.Send(hashName(0), base.Add(1*time.Second))
	b.Send(hashName(1), base.Add(3*time.Second)) // latest
	b.Send(hashName(2), base.Add(2*time.Second))
	wantOrder := []string{hashName(0), hashName(1), hashName(2)}
	wantLatest := base.Add(3 * time.Second)

	b.Stop()

	if got := f.flushedHashes(); !slices.Equal(got, wantOrder) {
		t.Errorf("flushed order = %v, want %v", got, wantOrder)
	}
	var gotLatest time.Time
	for _, c := range f.snapshot() {
		if c.now.After(gotLatest) {
			gotLatest = c.now
		}
	}
	if !gotLatest.Equal(wantLatest) {
		t.Errorf("reported activity time = %s, want the batch max %s", gotLatest, wantLatest)
	}
}

// Send is documented as non-blocking and best-effort: when the buffer is full
// it drops rather than blocks. With the consumer parked mid-flush, the buffer
// fills and further sends must return promptly and be discarded, never queued.
func TestBatcher_sendDropsWhenBufferFullWithoutBlocking(t *testing.T) {
	f := newFakeUpdater()
	f.entered = make(chan struct{}, 1)
	f.block = make(chan struct{})
	b := sessionbatch.New(context.Background(), f)

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Fill one batch so the run goroutine enters flush and parks on f.block,
	// leaving it unable to drain the channel.
	for i := range maxBatch {
		b.Send(hashName(i), at)
	}
	select {
	case <-f.entered:
	case <-time.After(2 * time.Second):
		close(f.block)
		b.Stop()
		t.Fatal("batcher never entered flush; cannot create a buffer-full condition")
	}

	// Consumer is parked. Fill the channel buffer, then send well beyond it.
	// Every send must return promptly; the excess must be dropped.
	sendNonBlocking := func(h string) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			b.Send(h, at)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("Send(%q) blocked; want non-blocking drop when the buffer is full", h)
		}
	}
	const overflow = 500
	for i := range chanBuf + overflow {
		sendNonBlocking(hashName(maxBatch + i))
	}

	close(f.block) // release the consumer
	b.Stop()

	// At most one read batch plus a full buffer can ever be flushed; the rest
	// were dropped. If Send queued instead of dropping, far more would appear.
	if total := f.totalFlushed(); total > maxBatch+chanBuf {
		t.Errorf("flushed %d items; want <= %d, excess must be dropped not queued", total, maxBatch+chanBuf)
	} else if total < maxBatch {
		t.Errorf("flushed %d items; want at least the initial batch of %d", total, maxBatch)
	}
}
