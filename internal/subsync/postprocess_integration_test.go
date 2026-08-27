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

// The decoder itself is exercised in internal/subtitleenc, which owns it. What
// these two tests own is the WIRING: that the NormalizeEncoding option actually
// reaches the decoder, and that turning it off leaves the bytes alone.

func TestIntegration_PostProcessBytes_normalizes_when_enabled(t *testing.T) {
	t.Parallel()
	srt := "1\n00:00:01,000 --> 00:00:04,000\nHello world\n\n"
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{"UTF-16LE with BOM", utf16LE(srt), "Hello world"},
		{"UTF-16BE with BOM", utf16BE(srt), "Hello world"},
		{"Windows-1252", []byte("1\n00:00:01,000 --> 00:00:04,000\ncaf\xe9\n\n"), "café"},
		{"UTF-8 with BOM", append([]byte{0xEF, 0xBB, 0xBF}, srt...), "Hello world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := PostProcessBytes(tt.input, PostProcessOptions{NormalizeEncoding: true})
			if !utf8.Valid(got) {
				t.Errorf("PostProcessBytes(%s) = %x, want valid UTF-8", tt.name, got)
			}
			if bytes.HasPrefix(got, []byte{0xEF, 0xBB, 0xBF}) {
				t.Errorf("PostProcessBytes(%s) kept a UTF-8 BOM", tt.name)
			}
			if !strings.Contains(string(got), tt.want) {
				t.Errorf("PostProcessBytes(%s) = %q, want it to contain %q", tt.name, got, tt.want)
			}
		})
	}
}

// TestIntegration_PostProcessBytes_passes_through_when_disabled pins the
// contract that makes normalize_utf8 a real setting rather than a description of
// what happens anyway: with it off, a subtitle reaches disk byte-for-byte as the
// provider sent it, whatever encoding that is. Every other layer may decode a
// throwaway view to judge or parse the bytes; this is the one place that decides
// what gets written, so this is where the guarantee has to hold.
func TestIntegration_PostProcessBytes_passes_through_when_disabled(t *testing.T) {
	t.Parallel()
	srt := "1\n00:00:01,000 --> 00:00:04,000\nHello world\n\n"
	for _, tt := range []struct {
		name  string
		input []byte
	}{
		{"UTF-16LE with BOM", utf16LE(srt)},
		{"UTF-16BE with BOM", utf16BE(srt)},
		{"Windows-1252", []byte("1\n00:00:01,000 --> 00:00:04,000\ncaf\xe9\n\n")},
		{"UTF-8 with BOM", append([]byte{0xEF, 0xBB, 0xBF}, srt...)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want := bytes.Clone(tt.input)
			got := PostProcessBytes(tt.input, PostProcessOptions{})
			if !bytes.Equal(got, want) {
				t.Errorf("PostProcessBytes(%s, normalize off) = %x, want the input unchanged %x",
					tt.name, got, want)
			}
		})
	}
}

// utf16LE and utf16BE encode ASCII-only text as UTF-16 with a BOM, which is the
// shape SubSource packs actually ship (measured: 4 of 8 members in one pack).
func utf16LE(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, r := range s {
		out = append(out, byte(r), 0)
	}
	return out
}

func utf16BE(s string) []byte {
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		out = append(out, 0, byte(r))
	}
	return out
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
