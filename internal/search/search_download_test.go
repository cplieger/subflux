package search

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/subsync"
)

func TestSyncSubtitle(t *testing.T) {
	t.Parallel()

	type syncSubtitleCase struct {
		name       string
		refContent string
		syncCfg    api.SyncConfig
		wantOffset int64
		createRef  bool
		wantSame   bool
	}

	defaultData := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n")

	cases := []syncSubtitleCase{
		{
			name:       "no_reference_returns_original",
			createRef:  false,
			syncCfg:    api.SyncConfig{SyncSubtitles: true},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:       "non_srt_reference_returns_original",
			createRef:  false, // no .srt reference
			syncCfg:    api.SyncConfig{SyncSubtitles: true},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:       "audio_fallback_when_sync_disabled",
			createRef:  false,
			syncCfg:    api.SyncConfig{SyncSubtitles: false, AudioSyncFallback: true},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:       "audio_fallback_after_sync_noop",
			createRef:  false,
			syncCfg:    api.SyncConfig{SyncSubtitles: true, AudioSyncFallback: true},
			wantSame:   true,
			wantOffset: 0,
		},
		{
			name:       "both_disabled_returns_original",
			createRef:  false,
			syncCfg:    api.SyncConfig{SyncSubtitles: false, AudioSyncFallback: false},
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
			syncCfg:    api.SyncConfig{SyncSubtitles: true},
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
			mc := &mockConfig{searchCfg: api.SearchConfig{}}
			e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

			got, offset := e.syncSubtitle(context.Background(), defaultData, videoPath, "fr", tc.syncCfg)

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
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	got, _ := e.syncSubtitle(context.Background(), []byte(incSRT), videoPath, "fr", api.SyncConfig{SyncSubtitles: true})
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
	e := newEngine(nil, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	got, offsetMs := e.SyncAndPostProcess(context.Background(), data, videoPath, "fr", api.DefaultVariant)

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
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	p := &mockProvider{name: "os"}
	e := newEngine([]api.Provider{p}, &mockStore{}, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	sub := &api.Subtitle{Provider: "nonexistent"}
	_, err := e.downloadFromProvider(context.Background(), sub)
	if err == nil {
		t.Fatal("downloadFromProvider() expected error for unknown provider, got nil")
	}
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("downloadFromProvider() error = %q, want ErrProviderNotFound", err.Error())
	}
}

func TestDownloadFromProvider_success_with_metrics(t *testing.T) {
	t.Parallel()
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	metrics := &mockMetrics{}
	p := &mockProvider{name: "os", data: []byte("subtitle data")}
	e := newEngine([]api.Provider{p}, &mockStore{}, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	sub := &api.Subtitle{Provider: "os"}
	data, err := e.downloadFromProvider(context.Background(), sub)
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
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	metrics := &mockMetrics{}
	p := &mockProvider{name: "os", downloadErr: errors.New("timeout")}
	e := newEngine([]api.Provider{p}, &mockStore{}, mc, metrics, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	sub := &api.Subtitle{Provider: "os"}
	_, err := e.downloadFromProvider(context.Background(), sub)
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

	result := detectExisting(context.Background(), videoPath, noopDetector{}, nil)

	if len(result.External) != 3 {
		t.Fatalf("detectExisting(context.Background(), ) found %d external subs, want 3", len(result.External))
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

	result := detectExisting(context.Background(), videoPath, noopDetector{}, nil)
	if len(result.External) != 0 {
		t.Errorf("detectExisting(context.Background(), ) found %d external subs, want 0 (empty lang filtered)", len(result.External))
	}
}

// --- Engine.HashFile wrapper ---

func TestSyncAgainstReference_no_reference_returns_original(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv")
	data := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")

	result := syncAgainstReference(context.Background(), data, videoPath, "fr")

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

	result := syncAgainstReference(context.Background(), []byte(incSRT), videoPath, "fr")

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
	refCues, err := subsync.ExtractEmbeddedSRT(context.Background(), videoPath, "", "fr", nil)
	if err != nil || len(refCues) < 5 {
		t.Skipf("ffprobe cannot extract from hand-crafted MKV (cues=%d, err=%v)", len(refCues), err)
	}

	// Incoming French SRT shifted by -2 seconds relative to embedded English.
	incSRT := "1\n00:00:03,000 --> 00:00:05,000\nInc1\n\n" +
		"2\n00:00:08,000 --> 00:00:10,000\nInc2\n\n" +
		"3\n00:00:13,000 --> 00:00:15,000\nInc3\n\n" +
		"4\n00:00:18,000 --> 00:00:20,000\nInc4\n\n" +
		"5\n00:00:23,000 --> 00:00:25,000\nInc5\n\n"

	result := syncAgainstReference(context.Background(), []byte(incSRT), videoPath, "fr")

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
	result := syncAgainstReference(context.Background(), data, videoPath, "fr")

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
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	e := newEngine(nil, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	got, _ := e.syncSubtitle(context.Background(), data, videoPath, "fr", api.SyncConfig{SyncSubtitles: true})

	// Should return original data unchanged (offset == 0 path).
	if !bytes.Equal(got, data) {
		t.Error("syncSubtitle() modified data when already in sync, want original")
	}
}
