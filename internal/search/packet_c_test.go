package search

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/testsupport"
)

// This file pins the packet-C engine behaviors: the per-media execution gate
// (P4), the post-work outcome-conditional scanned_at stamp (P5), and the
// inventory-only visit (S11).

// stampStore records RecordScanState calls and can gate SaveDownload/search
// visibility for concurrency assertions.
type stampStore struct {
	testsupport.NopStore

	mu     sync.Mutex
	stamps []api.ScanRecord
}

func (s *stampStore) RecordScanState(_ context.Context, rec *api.ScanRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stamps = append(s.stamps, *rec)
	return nil
}

func (s *stampStore) recorded() []api.ScanRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.ScanRecord(nil), s.stamps...)
}

// blockingProvider parks every Search call until released, counting
// concurrent entries so the test can prove mutual exclusion per media item.
type blockingProvider struct {
	name       api.ProviderID
	inFlight   atomic.Int32
	maxSeen    atomic.Int32
	release    chan struct{}
	entered    chan struct{}
	enterOnce  sync.Once
	enteredTwo chan struct{}
	enterCount atomic.Int32
}

func (p *blockingProvider) Name() api.ProviderID { return p.name }

func (p *blockingProvider) Search(ctx context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	cur := p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	for {
		prev := p.maxSeen.Load()
		if cur <= prev || p.maxSeen.CompareAndSwap(prev, cur) {
			break
		}
	}
	if n := p.enterCount.Add(1); n == 1 {
		p.enterOnce.Do(func() { close(p.entered) })
	} else if n == 2 && p.enteredTwo != nil {
		close(p.enteredTwo)
	}
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	return nil, nil
}

func (p *blockingProvider) Download(context.Context, *api.Subtitle) ([]byte, error) {
	return nil, nil
}

func TestSearchTargets_media_gate_serializes_same_item(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &stampStore{}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	p := &blockingProvider{
		name:    "block",
		release: make(chan struct{}),
		entered: make(chan struct{}),
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req1 := &api.SearchRequest{MediaType: "movie", ImdbID: "tt777"}
	req2 := &api.SearchRequest{MediaType: "movie", ImdbID: "tt777"}
	targets := []api.SubtitleTarget{{Code: "fr"}}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = e.SearchTargets(context.Background(), req1, videoPath, targets)
	}()
	// Ensure the first call is inside provider work before starting the second.
	<-p.entered
	go func() {
		defer wg.Done()
		_, _ = e.SearchTargets(context.Background(), req2, videoPath, targets)
	}()

	// Give the second call time to reach the gate, then release everything.
	time.Sleep(50 * time.Millisecond)
	close(p.release)
	wg.Wait()

	if got := p.maxSeen.Load(); got != 1 {
		t.Errorf("max concurrent provider entries for same media = %d, want 1 (media gate must serialize)", got)
	}
}

func TestSearchTargets_media_gate_distinct_items_run_concurrently(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ms := &stampStore{}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	p := &blockingProvider{
		name:       "block",
		release:    make(chan struct{}),
		entered:    make(chan struct{}),
		enteredTwo: make(chan struct{}),
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	targets := []api.SubtitleTarget{{Code: "fr"}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = e.SearchTargets(context.Background(),
			&api.SearchRequest{MediaType: "movie", ImdbID: "tt111"},
			filepath.Join(dir, "a.mkv"), targets)
	}()
	go func() {
		defer wg.Done()
		_, _ = e.SearchTargets(context.Background(),
			&api.SearchRequest{MediaType: "movie", ImdbID: "tt222"},
			filepath.Join(dir, "b.mkv"), targets)
	}()

	// Both provider calls must be in flight SIMULTANEOUSLY: a global lock
	// would deadlock this wait (second entry never happens while the first
	// blocks). Bounded by the test timeout rather than a flaky sleep.
	select {
	case <-p.enteredTwo:
	case <-time.After(10 * time.Second):
		t.Fatal("second distinct-media search never entered provider work: gate is not per-media")
	}
	close(p.release)
	wg.Wait()

	if got := p.maxSeen.Load(); got < 2 {
		t.Errorf("max concurrent provider entries across distinct media = %d, want 2", got)
	}
}

func TestSearchTargets_stamps_post_work_searched(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &stampStore{}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	p := &mockProvider{name: "test", results: nil}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123", Title: "T"}
	_, err := e.SearchTargets(context.Background(), req, videoPath, []api.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}

	stamps := ms.recorded()
	if len(stamps) != 1 {
		t.Fatalf("RecordScanState calls = %d, want exactly 1 (post-work stamp)", len(stamps))
	}
	if !stamps[0].Searched {
		t.Errorf("stamp.Searched = false, want true for a completed search")
	}
}

func TestSearchTargets_cancelled_run_does_not_stamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &stampStore{}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	p := &blockingProvider{
		name:    "block",
		release: make(chan struct{}),
		entered: make(chan struct{}),
	}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.SearchTargets(ctx, &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"},
			videoPath, []api.SubtitleTarget{{Code: "fr"}})
	}()
	<-p.entered
	cancel() // cancel mid-provider-work: the item is unfinished
	close(p.release)
	<-done

	for _, rec := range ms.recorded() {
		if rec.Searched {
			t.Errorf("cancelled run wrote a Searched=true stamp %+v; unfinished work must not enter the resume set", rec)
		}
	}
}

func TestInventoryCoverage_stamps_not_searched(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &stampStore{}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	p := &mockProvider{name: "test", results: nil}
	e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123", Title: "T"}
	e.InventoryCoverage(context.Background(), req, videoPath)

	stamps := ms.recorded()
	if len(stamps) != 1 {
		t.Fatalf("RecordScanState calls = %d, want exactly 1", len(stamps))
	}
	if stamps[0].Searched {
		t.Errorf("inventory stamp.Searched = true, want false (no provider work ran)")
	}
}
