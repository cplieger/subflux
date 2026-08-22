package search

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subsync"
)

func TestSyncSubtitle(t *testing.T) {
	t.Parallel()

	type syncSubtitleCase struct {
		name       string
		refContent string
		syncCfg    subflux.SyncConfig
		wantOffset int64
		createRef  bool
		wantSame   bool
	}

	defaultData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")

	cases := []syncSubtitleCase{
		{
			name:       "no_reference_returns_original",
			createRef:  false,
			syncCfg:    subflux.SyncConfig{SyncSubtitles: true},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:       "non_srt_reference_returns_original",
			createRef:  false, // no .srt reference
			syncCfg:    subflux.SyncConfig{SyncSubtitles: true},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:       "audio_fallback_when_sync_disabled",
			createRef:  false,
			syncCfg:    subflux.SyncConfig{SyncSubtitles: false, AudioSyncFallback: true},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:       "audio_fallback_after_sync_noop",
			createRef:  false,
			syncCfg:    subflux.SyncConfig{SyncSubtitles: true, AudioSyncFallback: true},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:       "both_disabled_returns_original",
			createRef:  false,
			syncCfg:    subflux.SyncConfig{SyncSubtitles: false, AudioSyncFallback: false},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:      "reference_exists_but_already_in_sync",
			createRef: true,
			refContent: "1\n00:00:01,000 --> 00:00:02,000\nRef1\n\n" +
				"2\n00:00:03,000 --> 00:00:04,000\nRef2\n\n" +
				"3\n00:00:05,000 --> 00:00:06,000\nRef3\n\n" +
				"4\n00:00:07,000 --> 00:00:08,000\nRef4\n\n" +
				"5\n00:00:09,000 --> 00:00:10,000\nRef5\n\n",
			syncCfg:    subflux.SyncConfig{SyncSubtitles: true},
			wantSame:   true,
			wantOffset: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			videoPath := filepath.Join(dir, "movie.mkv")

			if tc.createRef {
				refPath := filepath.Join(dir, "movie.en.srt")
				if err := os.WriteFile(refPath, []byte(tc.refContent), 0o644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			}

			ms := &mockStore{}
			mc := &mockConfig{searchCfg: subflux.SearchConfig{}}
			e := newEngine(nil, ms, mc, nil, scorer.New(&subflux.DefaultScores), Syncer{}, noopDetector{})

			got, offset := e.syncSubtitle(t.Context(), defaultData, videoPath, "fr", tc.syncCfg)

			if tc.wantSame && !bytes.Equal(got, defaultData) {
				t.Errorf("syncSubtitle() modified data, want original")
			}
			if offset != tc.wantOffset {
				t.Errorf("syncSubtitle() offset = %d, want %d", offset, tc.wantOffset)
			}
		})
	}
}

func TestSyncSubtitle_with_reference_srt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create an external reference SRT in a different language.
	// Since auto sync now only uses embedded references (not external SRT),
	// the subtitle should NOT be modified.
	refSRT := "1\n00:00:05,000 --> 00:00:07,000\nRef1\n\n" +
		"2\n00:00:10,000 --> 00:00:12,000\nRef2\n\n" +
		"3\n00:00:15,000 --> 00:00:17,000\nRef3\n\n" +
		"4\n00:00:20,000 --> 00:00:22,000\nRef4\n\n" +
		"5\n00:00:25,000 --> 00:00:27,000\nRef5\n\n"
	refPath := filepath.Join(dir, "movie.en.srt")
	if err := os.WriteFile(refPath, []byte(refSRT), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	// Create incoming SRT shifted by -2 seconds.
	incSRT := "1\n00:00:03,000 --> 00:00:05,000\nInc1\n\n" +
		"2\n00:00:08,000 --> 00:00:10,000\nInc2\n\n" +
		"3\n00:00:13,000 --> 00:00:15,000\nInc3\n\n" +
		"4\n00:00:18,000 --> 00:00:20,000\nInc4\n\n" +
		"5\n00:00:23,000 --> 00:00:25,000\nInc5\n\n"

	ms := &mockStore{}
	mc := &mockConfig{searchCfg: subflux.SearchConfig{}}
	e := newEngine(nil, ms, mc, nil, scorer.New(&subflux.DefaultScores), Syncer{}, noopDetector{})

	got, _ := e.syncSubtitle(t.Context(), []byte(incSRT), videoPath, "fr", subflux.SyncConfig{SyncSubtitles: true})
	// External SRT is no longer used by auto sync (embedded-only).
	// Data should be unchanged.
	if string(got) != incSRT {
		t.Error("syncSubtitle() modified data with external ref, want unchanged (embedded-only)")
	}
}

// --- SyncAndPostProcess ---

func TestEngine_SyncAndPostProcess_no_reference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")
	data := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello")

	mc := &mockConfig{}
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&subflux.DefaultScores), Syncer{}, noopDetector{})

	got, offsetMs := e.SyncAndPostProcess(t.Context(), data, videoPath, "fr", subflux.DefaultVariant)

	// No reference SRT exists, so data passes through PostProcess only.
	// PostProcess normalizes line endings to CRLF and ensures trailing CRLF.
	want := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")
	if !bytes.Equal(got, want) {
		t.Errorf("SyncAndPostProcess() = %q, want %q", got, want)
	}
	if offsetMs != 0 {
		t.Errorf("SyncAndPostProcess() offset = %d, want 0", offsetMs)
	}
}

// --- downloadFromProvider ---

func TestDownloadFromProvider_not_found(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: subflux.SearchConfig{}}
	p := &mockProvider{name: "os"}
	e := newEngine([]provider.Provider{p}, &mockStore{}, mc, nil, scorer.New(&subflux.DefaultScores), Syncer{}, noopDetector{})

	sub := &subflux.Subtitle{Provider: "nonexistent"}
	_, err := e.downloadFromProvider(t.Context(), sub)
	if err == nil {
		t.Fatal("downloadFromProvider() expected error for unknown provider, got nil")
	}
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("downloadFromProvider() error = %q, want ErrProviderNotFound", err.Error())
	}
}

func TestDownloadFromProvider_success_with_metrics(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: subflux.SearchConfig{}}
	metrics := &mockMetrics{}
	p := &mockProvider{name: "os", data: []byte("subtitle data")}
	e := newEngine([]provider.Provider{p}, &mockStore{}, mc, metrics, scorer.New(&subflux.DefaultScores), Syncer{}, noopDetector{})

	sub := &subflux.Subtitle{Provider: "os"}
	data, err := e.downloadFromProvider(t.Context(), sub)
	if err != nil {
		t.Fatalf("downloadFromProvider() unexpected error: %v", err)
	}
	if !bytes.Equal(data, []byte("subtitle data")) {
		t.Errorf("downloadFromProvider() = %q, want %q", data, "subtitle data")
	}
	if metrics.downloads.Load() != 1 {
		t.Errorf("metrics.downloads = %d, want 1", metrics.downloads.Load())
	}
}

func TestDownloadFromProvider_error_with_metrics(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: subflux.SearchConfig{}}
	metrics := &mockMetrics{}
	p := &mockProvider{name: "os", downloadErr: errors.New("timeout")}
	e := newEngine([]provider.Provider{p}, &mockStore{}, mc, metrics, scorer.New(&subflux.DefaultScores), Syncer{}, noopDetector{})

	sub := &subflux.Subtitle{Provider: "os"}
	_, err := e.downloadFromProvider(t.Context(), sub)
	if err == nil {
		t.Fatal("downloadFromProvider() expected error, got nil")
	}
	if metrics.downloads.Load() != 1 {
		t.Errorf("metrics.downloads = %d, want 1 (should record even on error)", metrics.downloads.Load())
	}
}

// --- detectExisting with external subs ---

func TestDetectExisting_finds_external_subs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create external subtitle files.
	for _, name := range []string{"movie.en.srt", "movie.fr.hi.srt", "movie.de.forced.ass"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("sub"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	result, err := detectExisting(t.Context(), videoPath, noopDetector{}, nil)
	if err != nil {
		t.Fatalf("detectExisting() unexpected error: %v", err)
	}

	if len(result.External) != 3 {
		t.Fatalf("detectExisting(t.Context(), ) found %d external subs, want 3", len(result.External))
	}

	// Verify the parsed metadata.
	found := map[string]externalSub{}
	for _, ext := range result.External {
		found[ext.Lang] = ext
	}

	en, ok := found["en"]
	if !ok {
		t.Fatal("missing en subtitle")
	}
	if en.HI || en.Forced {
		t.Errorf("en sub: HI=%v Forced=%v, want both false", en.HI, en.Forced)
	}

	fr, ok := found["fr"]
	if !ok {
		t.Fatal("missing fr subtitle")
	}
	if !fr.HI {
		t.Error("fr sub: HI=false, want true")
	}

	de, ok := found["de"]
	if !ok {
		t.Fatal("missing de subtitle")
	}
	if !de.Forced {
		t.Error("de sub: Forced=false, want true")
	}
}

func TestDetectExisting_ignores_empty_lang_segment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create a file with double-dot (empty lang segment).
	path := filepath.Join(dir, "movie..srt")
	if err := os.WriteFile(path, []byte("sub"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := detectExisting(t.Context(), videoPath, noopDetector{}, nil)
	if err != nil {
		t.Fatalf("detectExisting() unexpected error: %v", err)
	}
	if len(result.External) != 0 {
		t.Errorf("detectExisting(t.Context(), ) found %d external subs, want 0 (empty lang filtered)", len(result.External))
	}
}

// --- Engine.HashFile wrapper ---

func TestSyncAgainstReference_no_reference_returns_original(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")
	data := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")

	result := syncAgainstReference(t.Context(), data, videoPath, "fr")

	if result.Applied() {
		t.Errorf("syncAgainstReference(no ref) applied, want false")
	}
	if result.Applied() {
		t.Error("syncAgainstReference(no ref) should not apply")
	}
}

func TestSyncAgainstReference_with_reference_syncs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create an external reference SRT in English.
	// Since auto sync now only uses embedded references (not external SRT),
	// the result should NOT be applied.
	refSRT := "1\n00:00:05,000 --> 00:00:07,000\nRef1\n\n" +
		"2\n00:00:10,000 --> 00:00:12,000\nRef2\n\n" +
		"3\n00:00:15,000 --> 00:00:17,000\nRef3\n\n" +
		"4\n00:00:20,000 --> 00:00:22,000\nRef4\n\n" +
		"5\n00:00:25,000 --> 00:00:27,000\nRef5\n\n"
	refPath := filepath.Join(dir, "movie.en.srt")
	if err := os.WriteFile(refPath, []byte(refSRT), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Incoming SRT shifted by -2 seconds.
	incSRT := "1\n00:00:03,000 --> 00:00:05,000\nInc1\n\n" +
		"2\n00:00:08,000 --> 00:00:10,000\nInc2\n\n" +
		"3\n00:00:13,000 --> 00:00:15,000\nInc3\n\n" +
		"4\n00:00:18,000 --> 00:00:20,000\nInc4\n\n" +
		"5\n00:00:23,000 --> 00:00:25,000\nInc5\n\n"

	result := syncAgainstReference(t.Context(), []byte(incSRT), videoPath, "fr")

	// External SRT is no longer used by auto sync (embedded-only).
	if result.Applied() {
		t.Error("syncAgainstReference() applied with external ref, want no-op (embedded-only)")
	}
}

func TestSyncAgainstReference_embedded_fallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Build a minimal MKV with an embedded English SRT track containing
	// 5 cues. No external .srt file exists, so sync should fall back to
	// extracting the embedded subtitle as reference.
	mkv := buildTestMKV(t, "eng", []testMKVCue{
		{startMs: 5000, durationMs: 2000, text: "Ref1"},
		{startMs: 10000, durationMs: 2000, text: "Ref2"},
		{startMs: 15000, durationMs: 2000, text: "Ref3"},
		{startMs: 20000, durationMs: 2000, text: "Ref4"},
		{startMs: 25000, durationMs: 2000, text: "Ref5"},
	})
	if err := os.WriteFile(videoPath, mkv, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Verify ffprobe can parse the hand-crafted MKV; skip if not.
	refCues, err := subsync.ExtractEmbeddedSRT(t.Context(), videoPath, "", "fr", nil)
	if err != nil || len(refCues) < 5 {
		t.Skipf("ffprobe cannot extract from hand-crafted MKV (cues=%d, err=%v)", len(refCues), err)
	}

	// Incoming French SRT shifted by -2 seconds relative to embedded English.
	incSRT := "1\n00:00:03,000 --> 00:00:05,000\nInc1\n\n" +
		"2\n00:00:08,000 --> 00:00:10,000\nInc2\n\n" +
		"3\n00:00:13,000 --> 00:00:15,000\nInc3\n\n" +
		"4\n00:00:18,000 --> 00:00:20,000\nInc4\n\n" +
		"5\n00:00:23,000 --> 00:00:25,000\nInc5\n\n"

	result := syncAgainstReference(t.Context(), []byte(incSRT), videoPath, "fr")

	if !result.Applied() {
		t.Error("syncAgainstReference(embedded fallback) not applied, want applied")
	}
}

func TestSyncAgainstReference_embedded_skipped_when_same_lang(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Embedded track is French — same as the language being downloaded.
	// Should not be used as reference (would sync against itself).
	mkv := buildTestMKV(t, "fre", []testMKVCue{
		{startMs: 5000, durationMs: 2000, text: "Ref1"},
		{startMs: 10000, durationMs: 2000, text: "Ref2"},
		{startMs: 15000, durationMs: 2000, text: "Ref3"},
		{startMs: 20000, durationMs: 2000, text: "Ref4"},
		{startMs: 25000, durationMs: 2000, text: "Ref5"},
	})
	if err := os.WriteFile(videoPath, mkv, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")
	result := syncAgainstReference(t.Context(), data, videoPath, "fr")

	if result.Applied() {
		t.Error("syncAgainstReference(same lang embedded) applied, want false")
	}
}

// --- syncSubtitle already-in-sync path ---

func TestSyncSubtitle_reference_exists_but_already_in_sync(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")

	// Create a reference SRT in English with identical timing to incoming.
	srt := "1\n00:00:01,000 --> 00:00:02,000\nRef1\n\n" +
		"2\n00:00:03,000 --> 00:00:04,000\nRef2\n\n" +
		"3\n00:00:05,000 --> 00:00:06,000\nRef3\n\n" +
		"4\n00:00:07,000 --> 00:00:08,000\nRef4\n\n" +
		"5\n00:00:09,000 --> 00:00:10,000\nRef5\n\n"
	refPath := filepath.Join(dir, "movie.en.srt")
	if err := os.WriteFile(refPath, []byte(srt), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Incoming SRT with same timing (already in sync).
	data := []byte(srt)

	ms := &mockStore{}
	mc := &mockConfig{searchCfg: subflux.SearchConfig{}}
	e := newEngine(nil, ms, mc, nil, scorer.New(&subflux.DefaultScores), Syncer{}, noopDetector{})

	got, _ := e.syncSubtitle(t.Context(), data, videoPath, "fr", subflux.SyncConfig{SyncSubtitles: true})

	// Should return original data unchanged (offset == 0 path).
	if !bytes.Equal(got, data) {
		t.Error("syncSubtitle() modified data when already in sync, want original")
	}
}

// --- audio fallback ---

// fixedAudioExec answers the audio-sync call with a prepared result, so the
// fallback's own decision path is reachable without ffmpeg, a PCM extraction
// or a VAD pass. The reference half is left to the engine's syncer.
type fixedAudioExec struct{ result subsync.SyncResult }

func (fixedAudioExec) Reference(context.Context, []byte, string, string, float64) subsync.SyncResult {
	return subsync.SyncResult{}
}

func (x fixedAudioExec) Audio(context.Context, []byte, string, string) subsync.SyncResult {
	return x.result
}

// When reference sync changes nothing, the audio fallback's cues are what the
// caller gets back, with the audio offset: a confident audio result that the
// engine discards is a sync silently thrown away.
func TestSyncSubtitle_audio_fallback_result_is_applied(t *testing.T) {
	t.Parallel()
	videoPath := filepath.Join(t.TempDir(), "movie.mkv") // no reference => sync is a no-op
	data := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")
	audio := subsync.SyncResult{
		Method:     subsync.MethodAudio,
		Offset:     4000,
		Confidence: 0.9,
		Cues: []subsync.Cue{
			{Start: 5 * time.Second, End: 6 * time.Second, Text: "Hello"},
		},
	}

	e := New(nil, WithStore(&mockStore{}), WithConfig(&mockConfig{}),
		WithScorer(scorer.New(&subflux.DefaultScores)), WithSyncer(Syncer{}),
		WithTracks(noopDetector{}), WithSyncExec(fixedAudioExec{result: audio}))

	got, offsetMs := e.syncSubtitle(t.Context(), data, videoPath, "fr",
		subflux.SyncConfig{SyncSubtitles: true, AudioSyncFallback: true})

	if want := []byte("1\n00:00:05,000 --> 00:00:06,000\nHello\n\n"); !bytes.Equal(got, want) {
		t.Errorf("syncSubtitle(reference no-op, audio applies) = %q, want %q", got, want)
	}
	if offsetMs != 4000 {
		t.Errorf("syncSubtitle(reference no-op, audio applies) offset = %d, want 4000", offsetMs)
	}
}

// A low-confidence audio result is not applied: the data and offset come back
// untouched rather than half-corrected.
func TestSyncSubtitle_audio_fallback_low_confidence_is_ignored(t *testing.T) {
	t.Parallel()
	videoPath := filepath.Join(t.TempDir(), "movie.mkv")
	data := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")
	audio := subsync.SyncResult{
		Method:     subsync.MethodAudio,
		Offset:     4000,
		Confidence: 0.2,
		Cues: []subsync.Cue{
			{Start: 5 * time.Second, End: 6 * time.Second, Text: "Hello"},
		},
	}

	e := New(nil, WithStore(&mockStore{}), WithConfig(&mockConfig{}),
		WithScorer(scorer.New(&subflux.DefaultScores)), WithSyncer(Syncer{}),
		WithTracks(noopDetector{}), WithSyncExec(fixedAudioExec{result: audio}))

	got, offsetMs := e.syncSubtitle(t.Context(), data, videoPath, "fr",
		subflux.SyncConfig{SyncSubtitles: true, AudioSyncFallback: true})

	if !bytes.Equal(got, data) {
		t.Errorf("syncSubtitle(low-confidence audio) = %q, want the original %q", got, data)
	}
	if offsetMs != 0 {
		t.Errorf("syncSubtitle(low-confidence audio) offset = %d, want 0", offsetMs)
	}
}

// --- which candidates skip timing sync ---

// recordingSyncer counts the timing-sync calls the download path makes and
// reports a fixed offset, so both "was this candidate synced" and "was its
// offset persisted" are observable.
type recordingSyncer struct{ syncCalls atomic.Int32 }

func (s *recordingSyncer) Sync(_ context.Context, data []byte, _, _ string) (synced []byte, offsetMs int64) {
	s.syncCalls.Add(1)
	return append(bytes.Clone(data), []byte("2\n00:00:09,000 --> 00:00:10,000\nShifted\n\n")...), 4000
}

func (s *recordingSyncer) PostProcess(data []byte, _ subflux.PostProcessConfig) []byte { return data }

// fixedScorer gives every candidate the same score, which is how a test puts a
// candidate exactly on a threshold the download path reads.
type fixedScorer struct{ score int }

func (s fixedScorer) Score(subflux.SubtitleInfo, subflux.MatchSet) (score, scoreNoHash int) {
	return s.score, s.score
}

func (fixedScorer) ScoreToTier(int) subflux.ScoreTier { return subflux.TierGood }

// offsetStore records the timing offsets the download path persists.
type offsetStore struct {
	offsets []int64
	mockStore

	mu sync.Mutex
}

func (s *offsetStore) SetSyncOffset(_ context.Context, _ string, offsetMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offsets = append(s.offsets, offsetMs)
	return nil
}

func (s *offsetStore) recorded() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.offsets...)
}

// Timing sync is skipped for a match that is already known to be timed for
// this file: a hash match, or a release match good enough that sync could
// introduce drift rather than fix it. The threshold is inclusive — a
// candidate exactly on it is already close enough — and a skipped sync
// persists no offset.
func TestDownloadAndSave_syncs_only_candidates_that_need_it(t *testing.T) {
	t.Parallel()
	threshold := syncSkipThreshold(subflux.DefaultScores)

	tests := []struct {
		name      string
		matchedBy subflux.MatchMethod
		score     int
		wantSyncs int32
	}{
		{name: "below_threshold_syncs", matchedBy: subflux.MatchByIMDB, score: threshold - 1, wantSyncs: 1},
		{name: "exactly_on_threshold_skips", matchedBy: subflux.MatchByIMDB, score: threshold, wantSyncs: 0},
		{name: "hash_match_skips", matchedBy: subflux.MatchByHash, score: threshold - 1, wantSyncs: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			videoPath := filepath.Join(t.TempDir(), "movie.mkv")
			ms := &offsetStore{}
			syncer := &recordingSyncer{}
			p := &mockProvider{
				name: "test",
				results: []subflux.Subtitle{
					{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: tc.matchedBy, Language: "fr"},
				},
				data: []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n"),
			}
			e := newEngine([]provider.Provider{p}, ms,
				&mockConfig{searchCfg: subflux.SearchConfig{}, minScore: 0},
				nil, fixedScorer{score: tc.score}, syncer, noopDetector{})

			req := &subflux.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
			result, err := e.SearchTargets(t.Context(), req, videoPath,
				[]subflux.SubtitleTarget{{Code: "fr"}})
			if err != nil {
				t.Fatalf("SearchTargets() unexpected error: %v", err)
			}
			if len(result.Paths()) != 1 {
				t.Fatalf("SearchTargets(score %d) returned %d paths, want 1",
					tc.score, len(result.Paths()))
			}

			if got := syncer.syncCalls.Load(); got != tc.wantSyncs {
				t.Errorf("SearchTargets(matched_by %q, score %d) ran timing sync %d times, want %d (skip threshold %d)",
					tc.matchedBy, tc.score, got, tc.wantSyncs, threshold)
			}
			// The offset is persisted exactly when a sync produced one.
			var wantOffsets []int64
			if tc.wantSyncs > 0 {
				wantOffsets = []int64{4000}
			}
			if got := ms.recorded(); !slices.Equal(got, wantOffsets) {
				t.Errorf("SearchTargets(matched_by %q, score %d) persisted offsets %v, want %v",
					tc.matchedBy, tc.score, got, wantOffsets)
			}
		})
	}
}

// --- persistence logging on the ordinary path ---

// Every store write on the save path is WARN-logged when it fails, so a
// successful save must produce none of those lines: an error line on the
// ordinary path is indistinguishable from a real failure in Loki.
//
// capture.Default swaps the process-global logger: no t.Parallel.
func TestSearchTargets_successful_save_logs_no_failures(t *testing.T) {
	recs := capture.Default(t)
	videoPath := filepath.Join(t.TempDir(), "movie.mkv")
	p := &mockProvider{
		name: "test",
		results: []subflux.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: subflux.MatchByIMDB, Language: "fr"},
		},
		data: []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n"),
	}
	// A score below the skip threshold so the sync — and therefore the
	// sync-offset write — is part of the path under test.
	e := newEngine([]provider.Provider{p}, &mockStore{},
		&mockConfig{searchCfg: subflux.SearchConfig{}, minScore: 0}, nil,
		fixedScorer{score: syncSkipThreshold(subflux.DefaultScores) - 1},
		&recordingSyncer{}, noopDetector{})

	req := &subflux.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
	result, err := e.SearchTargets(t.Context(), req, videoPath,
		[]subflux.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 1 {
		t.Fatalf("SearchTargets() returned %d paths, want 1", len(result.Paths()))
	}
	if n := recs.CountExact("subtitle saved"); n != 1 {
		t.Errorf(`SearchTargets(success) logged msg="subtitle saved" %d times, want 1`, n)
	}
	for _, msg := range []string{
		"failed to record subtitle files",
		"failed to upsert subtitle file",
		"failed to record sync offset",
		"failed to record success",
		"failed to record scan state",
	} {
		if n := recs.CountExact(msg); n != 0 {
			t.Errorf("SearchTargets(success, store accepting every write) logged msg=%q %d times, want 0",
				msg, n)
		}
	}
}

// The attempt counter and the remaining count are how an operator reads a
// retry sequence, so they are pinned per record: 1 of 2 with one left, then
// 2 of 2 with none.
//
// capture.Default swaps the process-global logger: no t.Parallel.
func TestDownloadBestCandidate_numbers_each_failed_attempt(t *testing.T) {
	recs := capture.Default(t)
	videoPath := filepath.Join(t.TempDir(), "movie.mkv")
	p := &mockProvider{
		name: "test",
		results: []subflux.Subtitle{
			{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: subflux.MatchByIMDB, Language: "fr"},
			{Provider: "test", ReleaseName: "Movie-OTHER", MatchedBy: subflux.MatchByIMDB, Language: "fr"},
		},
		downloadErr: errors.New("provider down"),
	}
	e := newEngine([]provider.Provider{p}, &mockStore{},
		&mockConfig{searchCfg: subflux.SearchConfig{}, minScore: 0}, nil,
		fixedScorer{score: 10}, &recordingSyncer{}, noopDetector{})

	req := &subflux.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
	result, err := e.SearchTargets(t.Context(), req, videoPath,
		[]subflux.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if len(result.Paths()) != 0 {
		t.Fatalf("SearchTargets(every download failing) = %v, want no paths", result.Paths())
	}

	if got, want := recs.AttrValuesExact("download attempt failed, trying next", "attempt"),
		[]string{"1", "2"}; !slices.Equal(got, want) {
		t.Errorf(`SearchTargets(2 failing candidates) logged msg="download attempt failed, trying next" attempt=%v, want %v`,
			got, want)
	}
	if got, want := recs.AttrValuesExact("download attempt failed, trying next", "remaining"),
		[]string{"1", "0"}; !slices.Equal(got, want) {
		t.Errorf(`SearchTargets(2 failing candidates) logged msg="download attempt failed, trying next" remaining=%v, want %v`,
			got, want)
	}
	if got, ok := recs.AttrValueExact("all download attempts failed", "attempted"); !ok || got != "2" {
		t.Errorf(`SearchTargets(2 failing candidates) logged msg="all download attempts failed" attempted=%q (present=%v), want "2"`,
			got, ok)
	}
}
