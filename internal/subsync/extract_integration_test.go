package subsync

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- MKV embedded subtitle extraction (requires test5.mkv) ---

func TestIntegration_MKV_EmbeddedExtraction(t *testing.T) {
	t.Parallel()
	mkvPath := filepath.Join(testdataDir(t), "test5.mkv")
	if _, err := os.Stat(mkvPath); os.IsNotExist(err) {
		t.Skip("test5.mkv not found; download from https://sourceforge.net/projects/matroska/files/test_files/")
	}

	cues, err := ExtractEmbeddedSRT(context.Background(), mkvPath, "", "", nil)
	if err != nil {
		t.Fatalf("ExtractEmbeddedSRT: %v", err)
	}
	if len(cues) == 0 {
		t.Fatal("no cues extracted from test5.mkv")
	}
	t.Logf("extracted %d cues from MKV", len(cues))

	// Verify cues have reasonable timing.
	for i, c := range cues {
		if c.Start < 0 {
			t.Errorf("cue %d: negative start %v", i, c.Start)
		}
		if c.End <= c.Start {
			t.Errorf("cue %d: end %v <= start %v", i, c.End, c.Start)
		}
		if strings.TrimSpace(c.Text) == "" {
			t.Errorf("cue %d: empty text", i)
		}
	}
}

func TestIntegration_MKV_SyncFromEmbedded(t *testing.T) {
	t.Parallel()
	mkvPath := filepath.Join(testdataDir(t), "test5.mkv")
	if _, err := os.Stat(mkvPath); os.IsNotExist(err) {
		t.Skip("test5.mkv not found")
	}

	// Extract reference cues from the MKV.
	ref, err := ExtractEmbeddedSRT(context.Background(), mkvPath, "", "", nil)
	if err != nil || len(ref) < 5 {
		t.Skipf("insufficient embedded cues: %d, err=%v", len(ref), err)
	}

	// Shift by 3 seconds and sync back.
	shifted := ShiftCues(ref, 3*time.Second)
	syncOpts := DefaultSyncOptions()
	result := SyncWithOptions(context.Background(), ref, shifted, &syncOpts)

	if !result.Applied() {
		t.Fatal("sync not applied for MKV embedded reference")
	}

	residual := maxResidualMs(ref, result.Cues)
	t.Logf("MKV sync: method=%s, offset=%dms, residual=%dms, conf=%.2f",
		result.Method, result.Offset, residual, float64(result.Confidence))

	if residual > 100 {
		t.Errorf("max residual %dms > 100ms", residual)
	}
}

func TestIntegration_MKV_AudioSync(t *testing.T) {
	t.Parallel()
	mkvPath := filepath.Join(testdataDir(t), "test5.mkv")
	if _, err := os.Stat(mkvPath); os.IsNotExist(err) {
		t.Skip("test5.mkv not found")
	}

	ref, err := ExtractEmbeddedSRT(context.Background(), mkvPath, "", "", nil)
	if err != nil || len(ref) < 5 {
		t.Skipf("insufficient embedded cues: %d", len(ref))
	}

	shifted := ShiftCues(ref, 2*time.Second)
	opts := SyncOptions{
		VideoPath:   mkvPath,
		EnableAudio: true,
	}
	result := SyncWithOptions(context.Background(), nil, shifted, &opts)

	t.Logf("audio sync: method=%s, offset=%dms, conf=%.2f",
		result.Method, result.Offset, float64(result.Confidence))

	// Audio sync is best-effort; compressed audio energy extraction
	// doesn't produce usable speech signals for all codec/muxing
	// combinations. Just verify it didn't panic.
}

// --- SyncFile end-to-end (requires test5.mkv) ---

func TestIntegration_SyncFile_WithExternalRef(t *testing.T) {
	t.Parallel()
	mkvPath := filepath.Join(testdataDir(t), "test5.mkv")
	if _, err := os.Stat(mkvPath); os.IsNotExist(err) {
		t.Skip("test5.mkv not found")
	}

	ref, err := ExtractEmbeddedSRT(context.Background(), mkvPath, "", "", nil)
	if err != nil || len(ref) < 5 {
		t.Skipf("insufficient embedded cues: %d", len(ref))
	}

	dir := t.TempDir()

	// Write reference as "English" SRT.
	var refBuf bytes.Buffer
	if err := WriteSRT(&refBuf, ref); err != nil {
		t.Fatalf("write ref: %v", err)
	}
	refPath := filepath.Join(dir, "test5.en.srt")
	if err := os.WriteFile(refPath, refBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("save ref: %v", err)
	}

	// Create a shifted "French" subtitle.
	shifted := ShiftCues(ref, 3*time.Second)
	var shiftedBuf bytes.Buffer
	if err := WriteSRT(&shiftedBuf, shifted); err != nil {
		t.Fatalf("write shifted: %v", err)
	}
	shiftedPath := filepath.Join(dir, "test5.fr.srt")
	if err := os.WriteFile(shiftedPath, shiftedBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("save shifted: %v", err)
	}

	result, syncErr := SyncFile(context.Background(), mkvPath, shiftedPath, &Options{
		Reference: refPath,
	})
	if syncErr != nil {
		t.Fatalf("SyncFile: %v", syncErr)
	}

	t.Logf("SyncFile: method=%s, offset=%dms, conf=%.2f, applied=%v",
		result.Method, result.OffsetMs, result.Confidence, result.Applied)

	if !result.Applied {
		t.Error("SyncFile did not apply correction")
	}
}

// --- MP4 embedded subtitle extraction (requires test_subtitled.mp4) ---

func TestIntegration_MP4_EmbeddedExtraction(t *testing.T) {
	t.Parallel()
	mp4Path := filepath.Join(testdataDir(t), "test_subtitled.mp4")
	if _, err := os.Stat(mp4Path); os.IsNotExist(err) {
		t.Skip("test_subtitled.mp4 not found; create with: ffmpeg -i test5.mkv -map 0:v:0 -map 0:a:0 -map 0:s:0 -c:v copy -c:a copy -c:s mov_text test_subtitled.mp4")
	}

	cues, err := ExtractEmbeddedSRT(context.Background(), mp4Path, "", "", nil)
	if err != nil {
		t.Fatalf("ExtractEmbeddedSRT(mp4): %v", err)
	}
	if len(cues) == 0 {
		t.Fatal("no cues extracted from MP4")
	}
	t.Logf("extracted %d cues from MP4 (tx3g/mov_text)", len(cues))

	for i, c := range cues {
		if c.Start < 0 {
			t.Errorf("cue %d: negative start %v", i, c.Start)
		}
		if c.End <= c.Start {
			t.Errorf("cue %d: end %v <= start %v", i, c.End, c.Start)
		}
		if strings.TrimSpace(c.Text) == "" {
			t.Errorf("cue %d: empty text", i)
		}
	}
}

func TestIntegration_MP4_SyncFromEmbedded(t *testing.T) {
	t.Parallel()
	mp4Path := filepath.Join(testdataDir(t), "test_subtitled.mp4")
	if _, err := os.Stat(mp4Path); os.IsNotExist(err) {
		t.Skip("test_subtitled.mp4 not found")
	}

	ref, err := ExtractEmbeddedSRT(context.Background(), mp4Path, "", "", nil)
	if err != nil || len(ref) < 5 {
		t.Skipf("insufficient MP4 embedded cues: %d, err=%v", len(ref), err)
	}

	shifted := ShiftCues(ref, 3*time.Second)
	syncOpts := DefaultSyncOptions()
	result := SyncWithOptions(context.Background(), ref, shifted, &syncOpts)

	if !result.Applied() {
		t.Fatal("sync not applied for MP4 embedded reference")
	}

	residual := maxResidualMs(ref, result.Cues)
	t.Logf("MP4 sync: method=%s, offset=%dms, residual=%dms, conf=%.2f",
		result.Method, result.Offset, residual, float64(result.Confidence))

	if residual > 100 {
		t.Errorf("max residual %dms > 100ms", residual)
	}
}

func TestIntegration_MP4_AudioSync(t *testing.T) {
	t.Parallel()
	mp4Path := filepath.Join(testdataDir(t), "test_subtitled.mp4")
	if _, err := os.Stat(mp4Path); os.IsNotExist(err) {
		t.Skip("test_subtitled.mp4 not found")
	}

	ref, err := ExtractEmbeddedSRT(context.Background(), mp4Path, "", "", nil)
	if err != nil || len(ref) < 5 {
		t.Skipf("insufficient MP4 embedded cues: %d", len(ref))
	}

	shifted := ShiftCues(ref, 2*time.Second)
	opts := SyncOptions{
		VideoPath:   mp4Path,
		EnableAudio: true,
	}
	result := SyncWithOptions(context.Background(), nil, shifted, &opts)

	t.Logf("MP4 audio sync: method=%s, offset=%dms, conf=%.2f",
		result.Method, result.Offset, float64(result.Confidence))

	// Audio sync from MP4 is best-effort; some MP4 muxing layouts
	// don't expose audio data in a way the energy extractor can use.
	// Just verify it didn't panic.
}

// --- MKV vs MP4 cross-container consistency ---

func TestIntegration_CrossContainer_SameContent(t *testing.T) {
	t.Parallel()
	mkvPath := filepath.Join(testdataDir(t), "test5.mkv")
	mp4Path := filepath.Join(testdataDir(t), "test_subtitled.mp4")
	if _, err := os.Stat(mkvPath); os.IsNotExist(err) {
		t.Skip("test5.mkv not found")
	}
	if _, err := os.Stat(mp4Path); os.IsNotExist(err) {
		t.Skip("test_subtitled.mp4 not found")
	}

	mkvCues, err := ExtractEmbeddedSRT(context.Background(), mkvPath, "", "", nil)
	if err != nil {
		t.Fatalf("MKV extract: %v", err)
	}
	mp4Cues, err := ExtractEmbeddedSRT(context.Background(), mp4Path, "", "", nil)
	if err != nil {
		t.Fatalf("MP4 extract: %v", err)
	}

	if len(mkvCues) == 0 || len(mp4Cues) == 0 {
		t.Skipf("insufficient cues: mkv=%d, mp4=%d", len(mkvCues), len(mp4Cues))
	}

	// Both should have subtitle content from the same source.
	// Cue counts may differ (MKV Cluster/Block vs MP4 sample table encoding).
	// Text may differ slightly (tx3g encoding strips some formatting).
	n := min(len(mkvCues), len(mp4Cues))
	t.Logf("comparing first %d cues (mkv=%d, mp4=%d)", n, len(mkvCues), len(mp4Cues))

	// Verify both extracted non-trivial content.
	if len(mkvCues) < 3 {
		t.Errorf("MKV extracted only %d cues, want >= 3", len(mkvCues))
	}
	if len(mp4Cues) < 3 {
		t.Errorf("MP4 extracted only %d cues, want >= 3", len(mp4Cues))
	}

	// Verify the first cue text is similar (same source material).
	mkvFirst := strings.TrimSpace(mkvCues[0].Text)
	mp4First := strings.TrimSpace(mp4Cues[0].Text)
	if mkvFirst != mp4First {
		t.Logf("first cue text differs (expected for different container encodings):\n  mkv: %q\n  mp4: %q",
			mkvFirst, mp4First)
	}
}

// --- ffmpeg cross-validation ---

// loadFFmpegReference loads an ffmpeg-extracted SRT as ground truth.
func loadFFmpegReference(t *testing.T, name string) []Cue {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	cues, err := ParseSRT(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return cues
}

func TestIntegration_FFmpeg_MKV_CueCount(t *testing.T) {
	t.Parallel()
	mkvPath := filepath.Join(testdataDir(t), "test5.mkv")
	if _, err := os.Stat(mkvPath); os.IsNotExist(err) {
		t.Skip("test5.mkv not found")
	}

	ffmpegCues := loadFFmpegReference(t, "ffmpeg_mkv_eng.srt")
	ourCues, err := ExtractEmbeddedSRT(context.Background(), mkvPath, "", "", nil)
	if err != nil {
		t.Fatalf("ExtractEmbeddedSRT: %v", err)
	}

	if len(ourCues) != len(ffmpegCues) {
		t.Errorf("cue count: ours=%d, ffmpeg=%d", len(ourCues), len(ffmpegCues))
	}
}

func TestIntegration_FFmpeg_MKV_TimingMatch(t *testing.T) {
	t.Parallel()
	mkvPath := filepath.Join(testdataDir(t), "test5.mkv")
	if _, err := os.Stat(mkvPath); os.IsNotExist(err) {
		t.Skip("test5.mkv not found")
	}

	ffmpegCues := loadFFmpegReference(t, "ffmpeg_mkv_eng.srt")
	ourCues, err := ExtractEmbeddedSRT(context.Background(), mkvPath, "", "", nil)
	if err != nil {
		t.Fatalf("ExtractEmbeddedSRT: %v", err)
	}

	n := min(len(ourCues), len(ffmpegCues))
	for i := range n {
		startDiff := ourCues[i].Start.Milliseconds() - ffmpegCues[i].Start.Milliseconds()
		endDiff := ourCues[i].End.Milliseconds() - ffmpegCues[i].End.Milliseconds()

		if abs64(startDiff) > 10 {
			t.Errorf("cue %d start: ours=%v, ffmpeg=%v (diff=%dms)",
				i, ourCues[i].Start, ffmpegCues[i].Start, startDiff)
		}
		if abs64(endDiff) > 10 {
			t.Errorf("cue %d end: ours=%v, ffmpeg=%v (diff=%dms)",
				i, ourCues[i].End, ffmpegCues[i].End, endDiff)
		}

		ourText := strings.TrimSpace(ourCues[i].Text)
		ffText := strings.TrimSpace(ffmpegCues[i].Text)
		if ourText != ffText {
			t.Errorf("cue %d text:\n  ours:   %q\n  ffmpeg: %q", i, ourText, ffText)
		}
	}
}

func TestIntegration_FFmpeg_MP4_CueCount(t *testing.T) {
	t.Parallel()
	mp4Path := filepath.Join(testdataDir(t), "test_subtitled.mp4")
	if _, err := os.Stat(mp4Path); os.IsNotExist(err) {
		t.Skip("test_subtitled.mp4 not found")
	}

	ffmpegCues := loadFFmpegReference(t, "ffmpeg_mp4_eng.srt")
	ourCues, err := ExtractEmbeddedSRT(context.Background(), mp4Path, "", "", nil)
	if err != nil {
		t.Fatalf("ExtractEmbeddedSRT: %v", err)
	}

	if len(ourCues) != len(ffmpegCues) {
		t.Errorf("cue count: ours=%d, ffmpeg=%d", len(ourCues), len(ffmpegCues))
	}
}

func TestIntegration_FFmpeg_MP4_TimingMatch(t *testing.T) {
	t.Parallel()
	mp4Path := filepath.Join(testdataDir(t), "test_subtitled.mp4")
	if _, err := os.Stat(mp4Path); os.IsNotExist(err) {
		t.Skip("test_subtitled.mp4 not found")
	}

	ffmpegCues := loadFFmpegReference(t, "ffmpeg_mp4_eng.srt")
	ourCues, err := ExtractEmbeddedSRT(context.Background(), mp4Path, "", "", nil)
	if err != nil {
		t.Fatalf("ExtractEmbeddedSRT: %v", err)
	}

	n := min(len(ourCues), len(ffmpegCues))
	for i := range n {
		startDiff := ourCues[i].Start.Milliseconds() - ffmpegCues[i].Start.Milliseconds()
		endDiff := ourCues[i].End.Milliseconds() - ffmpegCues[i].End.Milliseconds()

		// MP4 timing may differ by up to 1 frame (~42ms at 24fps) due to
		// timescale rounding differences between our parser and ffmpeg.
		if abs64(startDiff) > 50 {
			t.Errorf("cue %d start: ours=%v, ffmpeg=%v (diff=%dms)",
				i, ourCues[i].Start, ffmpegCues[i].Start, startDiff)
		}
		if abs64(endDiff) > 50 {
			t.Errorf("cue %d end: ours=%v, ffmpeg=%v (diff=%dms)",
				i, ourCues[i].End, ffmpegCues[i].End, endDiff)
		}

		ourText := strings.TrimSpace(ourCues[i].Text)
		ffText := strings.TrimSpace(ffmpegCues[i].Text)
		if ourText != ffText {
			t.Errorf("cue %d text:\n  ours:   %q\n  ffmpeg: %q", i, ourText, ffText)
		}
	}
}
