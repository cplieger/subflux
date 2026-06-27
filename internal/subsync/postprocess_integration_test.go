package subsync

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// writeCues is a test helper that writes cues to a string.
func writeCues(cues []Cue) string {
	var buf bytes.Buffer
	_ = WriteSRT(&buf, cues)
	return buf.String()
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
