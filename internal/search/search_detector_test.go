package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/scorer"
)

// Behavior-stability fixtures for the embedded-detector separation (R4.2):
// usable-track suppression, ignored-codec search trigger, detector-error
// fail-open with coverage retention, the HI-fallback scoring edge, and
// nil-detector construction impossibility.

// coverageRecordingStore records RecordSubtitleFiles calls so the
// coverage-retention fixtures can assert whether the full-set replacement
// ran for a video.
type coverageRecordingStore struct {
	lastFiles []api.SubtitleFile
	mockStore
	recordCalls int
}

func (s *coverageRecordingStore) RecordSubtitleFiles(_ context.Context, _ api.MediaType, _ string, files []api.SubtitleFile) (bool, error) {
	s.recordCalls++
	s.lastFiles = files
	return true, nil
}

// A usable embedded track satisfies the target: no provider search runs
// (current suppress behavior preserved through the seam change).
func TestSearchTargets_usable_embedded_track_suppresses_search(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &coverageRecordingStore{}
	mc := &mockConfig{embedded: api.EmbeddedPolicy{IgnorePGS: true, IgnoreVobSub: true}}
	metrics := &mockMetrics{}
	p := &mockProvider{
		name:    "test",
		results: []api.Subtitle{{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"}},
	}
	detector := trackDetector{tracks: []api.EmbeddedTrack{{Lang: "fr", Codec: "srt"}}}
	e := newEngine([]api.Provider{p}, ms, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, detector)

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
	result, err := e.SearchTargets(context.Background(), req, videoPath, []api.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if got := metrics.searches.Load(); got != 0 {
		t.Errorf("provider searches = %d, want 0 (usable embedded track suppresses search)", got)
	}
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() paths = %v, want none", result.Paths())
	}
	// The successful probe still records the coverage inventory.
	if ms.recordCalls != 1 {
		t.Errorf("RecordSubtitleFiles calls = %d, want 1", ms.recordCalls)
	}
}

// An ignored-codec embedded track stays visible in coverage but triggers
// external provider search.
func TestSearchTargets_ignored_codec_triggers_search(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &coverageRecordingStore{}
	mc := &mockConfig{embedded: api.EmbeddedPolicy{IgnorePGS: true, IgnoreVobSub: true}}
	metrics := &mockMetrics{}
	subData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")
	p := &mockProvider{
		name:    "test",
		results: []api.Subtitle{{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"}},
		data:    subData,
	}
	detector := trackDetector{tracks: []api.EmbeddedTrack{{Lang: "fr", Codec: "pgs"}}}
	e := newEngine([]api.Provider{p}, ms, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, detector)

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
	result, err := e.SearchTargets(context.Background(), req, videoPath, []api.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if got := metrics.searches.Load(); got != 1 {
		t.Errorf("provider searches = %d, want 1 (ignored codec does not satisfy the target)", got)
	}
	if len(result.Paths()) != 1 {
		t.Fatalf("SearchTargets() returned %d paths, want 1 (external download)", len(result.Paths()))
	}
	// The pgs track stays visible in the recorded coverage inventory.
	foundPGS := false
	for _, f := range ms.lastFiles {
		if f.Source == api.SourceEmbedded && f.Codec == "pgs" && f.Language == "fr" {
			foundPGS = true
		}
	}
	if !foundPGS {
		t.Errorf("recorded coverage %+v lacks the ignored-codec embedded row", ms.lastFiles)
	}
}

// A detector failure is observable (metric), the provider search continues
// fail-open, and the coverage replacement is SKIPPED for the video so the
// last complete snapshot survives (RecordSubtitleFiles is full-set
// replacement; stamping zero tracks would delete valid persisted rows).
func TestSearchTargets_detector_error_fail_open_coverage_retained(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &coverageRecordingStore{}
	mc := &mockConfig{embedded: api.EmbeddedPolicy{IgnorePGS: true, IgnoreVobSub: true}}
	metrics := &mockMetrics{}
	subData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")
	p := &mockProvider{
		name:    "test",
		results: []api.Subtitle{{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"}},
		data:    subData,
	}
	e := newEngine([]api.Provider{p}, ms, mc, metrics, scorer.New(&api.DefaultScores), Syncer{},
		errDetector{err: errors.New("ffprobe exploded")})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
	result, err := e.SearchTargets(context.Background(), req, videoPath, []api.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	// Fail-open: external search still ran and downloaded.
	if got := metrics.searches.Load(); got != 1 {
		t.Errorf("provider searches = %d, want 1 (fail-open)", got)
	}
	if len(result.Paths()) != 1 {
		t.Errorf("SearchTargets() returned %d paths, want 1", len(result.Paths()))
	}
	// Coverage retention: the full-set replacement was skipped.
	if ms.recordCalls != 0 {
		t.Errorf("RecordSubtitleFiles calls = %d, want 0 (prior rows must survive a failed probe)", ms.recordCalls)
	}
	if result.CoverageChanged {
		t.Error("CoverageChanged = true, want false on a failed probe")
	}
	// The error is observable.
	if got := metrics.detectorErrs.Load(); got != 1 {
		t.Errorf("detector error metric = %d, want 1", got)
	}
}

// InventoryCoverage applies the same retention rule on the provider-free path.
func TestInventoryCoverage_detector_error_skips_replacement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &coverageRecordingStore{}
	mc := &mockConfig{}
	metrics := &mockMetrics{}
	e := newEngine(nil, ms, mc, metrics, scorer.New(&api.DefaultScores), Syncer{},
		errDetector{err: errors.New("ffprobe exploded")})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"}
	changed := e.InventoryCoverage(context.Background(), req, videoPath)
	if changed {
		t.Error("InventoryCoverage() = true, want false on detector error")
	}
	if ms.recordCalls != 0 {
		t.Errorf("RecordSubtitleFiles calls = %d, want 0 (retention)", ms.recordCalls)
	}
	if got := metrics.detectorErrs.Load(); got != 1 {
		t.Errorf("detector error metric = %d, want 1", got)
	}
}

// Context cancellation is neither counted nor warned: a shutdown mid-probe
// is not a detector failure.
func TestDetectExistingObserved_context_cancel_not_counted(t *testing.T) {
	t.Parallel()
	ms := &coverageRecordingStore{}
	mc := &mockConfig{}
	metrics := &mockMetrics{}
	e := newEngine(nil, ms, mc, metrics, scorer.New(&api.DefaultScores), Syncer{},
		errDetector{err: context.Canceled})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, probeOK := e.detectExistingObserved(ctx, "/some/movie.mkv")
	if probeOK {
		t.Error("probeOK = true, want false (probe failed)")
	}
	if got := metrics.detectorErrs.Load(); got != 0 {
		t.Errorf("detector error metric = %d, want 0 (cancellation excluded)", got)
	}
}

// The named HI-fallback edge (fixture-pinned FIX): a standard-variant target
// with an HI-only embedded track and no external results must trigger a
// search and score/download NOTHING — pre-separation, the fake provider's HI
// results could survive the standardSubs HI-fallback and reach scoring (and
// the impossible Download). With no embedded results in the pipeline there
// is nothing to score.
func TestSearchTargets_hi_fallback_edge_no_embedded_in_scoring(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	ms := &coverageRecordingStore{}
	mc := &mockConfig{}
	metrics := &mockMetrics{}
	// External providers return NO results.
	p := &mockProvider{name: "test"}
	// HI-only embedded track in a usable text codec.
	detector := trackDetector{tracks: []api.EmbeddedTrack{{Lang: "fr", Codec: "srt", HearingImpaired: true}}}
	e := newEngine([]api.Provider{p}, ms, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, detector)

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
	result, err := e.SearchTargets(context.Background(), req, videoPath, []api.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	// The HI track does not satisfy the standard target: a search runs.
	if got := metrics.searches.Load(); got != 1 {
		t.Errorf("provider searches = %d, want 1 (HI-only track does not satisfy standard)", got)
	}
	// Nothing scored, nothing downloaded, nothing saved: embedded results
	// cannot enter scoring/downloading anymore.
	if len(result.Paths()) != 0 {
		t.Errorf("SearchTargets() paths = %v, want none", result.Paths())
	}
	if ms.successCalled {
		t.Error("SaveDownload called, want no download for the HI-fallback edge")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("unexpected files written: %v", entries)
	}
}

// search.New requires a detector: a missed injection is a construction-time
// fact, never a nil-deref at probe time.
func TestNew_nil_detector_panics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("New() without WithTracks did not panic")
		}
	}()
	New(nil, WithStore(&mockStore{}), WithConfig(&mockConfig{}),
		WithScorer(scorer.New(&api.DefaultScores)), WithSyncer(Syncer{}))
}
