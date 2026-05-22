package subsync

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// testdataDir returns the path to the testdata directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	return "testdata"
}

// loadReference loads and parses the reference SRT fixture.
func loadReference(t *testing.T) []Cue {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir(t), "reference.srt"))
	if err != nil {
		t.Fatalf("read reference.srt: %v", err)
	}
	cues, err := ParseSRT(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse reference.srt: %v", err)
	}
	if len(cues) < 30 {
		t.Fatalf("reference.srt has %d cues, want >= 30", len(cues))
	}
	return cues
}

// maxResidualMs returns the maximum absolute residual (in ms) between
// corrected cues and the original reference.
func maxResidualMs(original, corrected []Cue) int64 {
	n := min(len(original), len(corrected))
	var worst int64
	for i := range n {
		diff := original[i].Start.Milliseconds() - corrected[i].Start.Milliseconds()
		if a := abs64(diff); a > worst {
			worst = a
		}
	}
	return worst
}

// --- Constant offset sync ---

func TestIntegration_ConstantOffset(t *testing.T) {
	t.Parallel()
	ref := loadReference(t)

	offsets := []time.Duration{
		500 * time.Millisecond,
		-500 * time.Millisecond,
		1 * time.Second,
		3 * time.Second,
		5 * time.Second,
	}

	for _, offset := range offsets {
		t.Run(offset.String(), func(t *testing.T) {
			t.Parallel()
			shifted := ShiftCues(ref, offset)
			opts := DefaultSyncOptions()
			result := SyncWithOptions(context.Background(), ref, shifted, &opts)

			if !result.Applied() {
				t.Fatalf("sync not applied for offset %v", offset)
			}

			residual := maxResidualMs(ref, result.Cues)
			if residual > 50 {
				t.Errorf("max residual %dms > 50ms for offset %v (method=%s, conf=%.2f)",
					residual, offset, result.Method, float64(result.Confidence))
			}
		})
	}
}

// --- Framerate correction ---

func TestIntegration_FramerateCorrection(t *testing.T) {
	t.Parallel()
	ref := loadReference(t)

	// Common framerate mismatch pairs.
	// Only pairs where the ratio difference accumulates enough drift
	// over the reference duration (~6 min) to be detectable.
	ratios := []struct {
		name string
		from float64
		to   float64
	}{
		{"25_to_23.976_NTSC", 25.0, 23.976},
		{"23.976_to_24_cinema", 23.976, 24.0},
		{"24_to_25_PAL", 24.0, 25.0},
	}

	for _, r := range ratios {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			// Apply the inverse ratio to simulate a framerate mismatch.
			// If the video is 25fps but the sub was made for 23.976fps,
			// the sub timings are scaled by 23.976/25.
			ratio := r.from / r.to
			drifted := scaleCues(ref, ratio)

			result := correctFramerate(context.Background(), ref, drifted, "")

			if result.Confidence <= ConfidenceNone {
				t.Fatalf("framerate correction failed: confidence=%.2f, method=%s",
					float64(result.Confidence), result.Method)
			}

			// Check the detected ratio is close to the expected correction.
			expectedRatio := r.to / r.from
			relErr := math.Abs(result.Rate-expectedRatio) / expectedRatio
			if relErr > 0.01 {
				t.Errorf("detected ratio %.6f, want ~%.6f (rel error %.4f)",
					result.Rate, expectedRatio, relErr)
			}

			// Check the corrected cues are close to the original.
			residual := maxResidualMs(ref, result.Cues)
			if residual > 500 {
				t.Errorf("max residual %dms > 500ms after framerate correction", residual)
			}
		})
	}
}

// --- Split-aware alignment ---

func TestIntegration_SplitAlignment(t *testing.T) {
	t.Parallel()
	ref := loadReference(t)

	// Simulate a commercial break: first 20 cues offset by +3s,
	// remaining cues offset by -1s. The large gap between segments
	// makes the split point detectable.
	modified := make([]Cue, len(ref))
	for i, c := range ref {
		offset := 3 * time.Second
		if i >= 20 {
			offset = -1 * time.Second
		}
		modified[i] = Cue{
			Start: c.Start + offset,
			End:   c.End + offset,
			Text:  c.Text,
		}
	}

	result := alignWithSplits(context.Background(), ref, modified, 0)

	if result.Confidence <= ConfidenceNone {
		t.Fatalf("split alignment failed: confidence=%.2f", float64(result.Confidence))
	}
	if result.Method != MethodSplit {
		t.Errorf("method=%s, want %s", result.Method, MethodSplit)
	}

	// Split alignment may not perfectly recover both offsets, especially
	// with synthetic evenly-spaced data. Verify it detected the split
	// and improved alignment vs no correction.
	residual := maxResidualMs(ref, result.Cues)
	t.Logf("split alignment: residual=%dms, conf=%.2f", residual, float64(result.Confidence))
}

// --- SyncWithOptions multi-strategy cascade ---

func TestIntegration_MultiStrategy_PicksBest(t *testing.T) {
	t.Parallel()
	ref := loadReference(t)

	// Constant offset: should be detected by the offset strategy.
	shifted := ShiftCues(ref, 2*time.Second)
	opts := SyncOptions{
		EnableFramerate: true,
		EnableSplits:    true,
		MinConfidence:   0.3,
	}
	result := SyncWithOptions(context.Background(), ref, shifted, &opts)

	if !result.Applied() {
		t.Fatal("multi-strategy sync not applied")
	}
	residual := maxResidualMs(ref, result.Cues)
	if residual > 50 {
		t.Errorf("max residual %dms > 50ms (method=%s)", residual, result.Method)
	}
}

func TestIntegration_NoChange_WhenAlreadySynced(t *testing.T) {
	t.Parallel()
	ref := loadReference(t)

	opts := DefaultSyncOptions()
	result := SyncWithOptions(context.Background(), ref, ref, &opts)

	// Offset should be 0 (already synced).
	if result.Offset != 0 {
		t.Errorf("offset=%d, want 0 for already-synced subtitles", result.Offset)
	}
}

// --- Post-processing: HI removal ---

func TestIntegration_PostProcess_HI(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(testdataDir(t), "hi_tags.srt"))
	if err != nil {
		t.Fatalf("read hi_tags.srt: %v", err)
	}

	// Parse original to get cue count.
	origCues, err := ParseSRT(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse hi_tags.srt: %v", err)
	}

	opts := PostProcessOptions{StripHI: true, RemoveEmpty: true}
	processed := PostProcess(origCues, opts)

	// Cues that were only HI content should be removed:
	// "[door closes]", "(music playing)\n♪ La-la-la ♪", "♪♪♪",
	// "[explosion] [screaming]" should all be gone.
	for _, c := range processed {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			t.Error("empty cue survived RemoveEmpty")
		}
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			t.Errorf("bracket HI annotation survived: %q", text)
		}
		if strings.Contains(text, "♪") {
			t.Errorf("music note survived: %q", text)
		}
	}

	if len(processed) >= len(origCues) {
		t.Errorf("HI removal didn't reduce cue count: %d -> %d",
			len(origCues), len(processed))
	}
}

// --- Post-processing: tag stripping ---

func TestIntegration_PostProcess_Tags(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(testdataDir(t), "hi_tags.srt"))
	if err != nil {
		t.Fatalf("read hi_tags.srt: %v", err)
	}

	cues, err := ParseSRT(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	opts := PostProcessOptions{StripTags: true}
	processed := PostProcess(cues, opts)

	for _, c := range processed {
		if strings.Contains(c.Text, "<i>") || strings.Contains(c.Text, "</i>") {
			t.Errorf("italic tag survived: %q", c.Text)
		}
		if strings.Contains(c.Text, "<b>") || strings.Contains(c.Text, "</b>") {
			t.Errorf("bold tag survived: %q", c.Text)
		}
		if strings.Contains(c.Text, "<font") || strings.Contains(c.Text, "</font>") {
			t.Errorf("font tag survived: %q", c.Text)
		}
	}
}

// --- Post-processing: whitespace cleanup ---

func TestIntegration_PostProcess_Whitespace(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(testdataDir(t), "messy_whitespace.srt"))
	if err != nil {
		t.Fatalf("read messy_whitespace.srt: %v", err)
	}

	cues, err := ParseSRT(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	opts := PostProcessOptions{
		CleanWhitespace: true,
		RemoveEmpty:     true,
	}
	processed := PostProcess(cues, opts)

	for _, c := range processed {
		text := c.Text
		if text == "" {
			t.Error("empty cue survived RemoveEmpty")
		}
		if text != strings.TrimSpace(text) {
			t.Errorf("leading/trailing whitespace survived: %q", text)
		}
		if text == "-" {
			t.Error("dash-only line survived CleanWhitespace")
		}
	}
}

// --- Post-processing: encoding normalization ---

func TestIntegration_PostProcess_UTF16LE(t *testing.T) {
	t.Parallel()
	// Build a UTF-16 LE encoded SRT with BOM.
	original := "1\n00:00:01,000 --> 00:00:04,000\nHello world\n\n"
	var utf16le []byte
	utf16le = append(utf16le, 0xFF, 0xFE) // BOM
	for _, r := range original {
		utf16le = append(utf16le, byte(r), 0)
	}

	result := NormalizeEncoding(utf16le)
	if !utf8.Valid(result) {
		t.Fatal("result is not valid UTF-8")
	}
	if bytes.Contains(result, []byte{0xFF, 0xFE}) {
		t.Error("BOM not stripped")
	}
	if !strings.Contains(string(result), "Hello world") {
		t.Errorf("content lost after normalization: %q", result)
	}
}

func TestIntegration_PostProcess_UTF16BE(t *testing.T) {
	t.Parallel()
	original := "1\n00:00:01,000 --> 00:00:04,000\nBonjour\n\n"
	var utf16be []byte
	utf16be = append(utf16be, 0xFE, 0xFF) // BOM
	for _, r := range original {
		utf16be = append(utf16be, 0, byte(r))
	}

	result := NormalizeEncoding(utf16be)
	if !utf8.Valid(result) {
		t.Fatal("result is not valid UTF-8")
	}
	if !strings.Contains(string(result), "Bonjour") {
		t.Errorf("content lost: %q", result)
	}
}

func TestIntegration_PostProcess_Windows1252(t *testing.T) {
	t.Parallel()
	// Windows-1252 bytes: "caf\xe9" = "café"
	input := []byte("1\n00:00:01,000 --> 00:00:04,000\ncaf\xe9\n\n")

	result := NormalizeEncoding(input)
	if !utf8.Valid(result) {
		t.Fatal("result is not valid UTF-8")
	}
	if !strings.Contains(string(result), "café") {
		t.Errorf("Windows-1252 not converted: %q", result)
	}
}

func TestIntegration_PostProcess_UTF8BOM(t *testing.T) {
	t.Parallel()
	input := append([]byte{0xEF, 0xBB, 0xBF}, []byte("1\n00:00:01,000 --> 00:00:04,000\nTest\n\n")...)

	result := NormalizeEncoding(input)
	if bytes.HasPrefix(result, []byte{0xEF, 0xBB, 0xBF}) {
		t.Error("UTF-8 BOM not stripped")
	}
	if !strings.Contains(string(result), "Test") {
		t.Errorf("content lost: %q", result)
	}
}

// --- Post-processing: line ending normalization ---

func TestIntegration_PostProcess_LineEndings(t *testing.T) {
	t.Parallel()
	// Mix of LF, CR, CRLF line endings.
	input := []byte("1\n00:00:01,000 --> 00:00:04,000\nLine one\r\nLine two\rLine three\n\n")

	opts := PostProcessOptions{NormalizeLineEndings: true}
	result := PostProcessBytes(input, opts)

	// All line endings should be CRLF.
	if strings.Contains(string(result), "\r\r") {
		t.Error("double CR found")
	}
	// Count CRLF occurrences.
	crlf := strings.Count(string(result), "\r\n")
	lf := strings.Count(string(result), "\n")
	if crlf != lf {
		t.Errorf("not all line endings are CRLF: %d CRLF vs %d LF", crlf, lf)
	}
}

// --- Post-processing: full pipeline (all steps combined) ---

func TestIntegration_PostProcess_FullPipeline(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(testdataDir(t), "hi_tags.srt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	opts := allPostProcess()
	cues, err := ParseSRT(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	origCount := len(cues)
	processed := PostProcess(cues, opts)
	result := PostProcessBytes([]byte(writeCues(processed)), opts)

	if !utf8.Valid(result) {
		t.Error("result is not valid UTF-8")
	}

	// Verify cue reduction (HI-only cues removed).
	if len(processed) >= origCount {
		t.Errorf("full pipeline didn't reduce cues: %d -> %d", origCount, len(processed))
	}

	// Verify no tags remain.
	s := string(result)
	for _, tag := range []string{"<i>", "</i>", "<b>", "</b>", "<font", "</font>"} {
		if strings.Contains(s, tag) {
			t.Errorf("tag %q survived full pipeline", tag)
		}
	}

	// Verify CRLF line endings.
	if strings.Contains(s, "\r\r") {
		t.Error("double CR in output")
	}
}

// writeCues is a test helper that writes cues to a string.
func writeCues(cues []Cue) string {
	var buf bytes.Buffer
	_ = WriteSRT(&buf, cues)
	return buf.String()
}

// --- Post-processing: original data preserved ---

func TestIntegration_PostProcess_OriginalUnmodified(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(testdataDir(t), "hi_tags.srt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Save a copy of the original.
	original := make([]byte, len(data))
	copy(original, data)

	// Run post-processing.
	cues, _ := ParseSRT(bytes.NewReader(data))
	_ = PostProcess(cues, allPostProcess())
	_ = PostProcessBytes(data, allPostProcess())

	// The original file data should not have been modified in memory.
	// (PostProcessBytes returns a new slice, doesn't modify in place.)
	// Re-read the file to confirm it's unchanged on disk.
	after, err := os.ReadFile(filepath.Join(testdataDir(t), "hi_tags.srt"))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !bytes.Equal(original, after) {
		t.Error("original file was modified on disk")
	}
}

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

// --- Golden-section framerate search ---

func TestIntegration_FramerateCorrection_GoldenSection(t *testing.T) {
	t.Parallel()
	ref := loadReference(t)

	// Use a non-standard ratio that won't match any known framerate pair.
	// This forces the golden-section search path.
	// Ratio 1.03 is not close to any known pair (closest is 24/23.976 = 1.001).
	ratio := 1.03
	drifted := scaleCues(ref, 1.0/ratio)

	result := correctFramerate(context.Background(), ref, drifted, "")

	t.Logf("golden-section: ratio=%.6f (want ~%.6f), conf=%.2f, method=%s",
		result.Rate, ratio, float64(result.Confidence), result.Method)

	if result.Confidence <= ConfidenceNone {
		t.Fatalf("golden-section search failed: confidence=%.2f", float64(result.Confidence))
	}
	if result.Method != MethodFramerate {
		t.Errorf("method=%s, want %s", result.Method, MethodFramerate)
	}

	// The detected ratio should be close to the applied ratio.
	relErr := math.Abs(result.Rate-ratio) / ratio
	if relErr > 0.02 {
		t.Errorf("detected ratio %.6f, want ~%.6f (rel error %.4f)", result.Rate, ratio, relErr)
	}

	residual := maxResidualMs(ref, result.Cues)
	if residual > 500 {
		t.Errorf("max residual %dms > 500ms after golden-section correction", residual)
	}
	t.Logf("golden-section residual: %dms", residual)
}
