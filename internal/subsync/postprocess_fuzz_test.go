package subsync

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// FuzzStripHI exercises hearing-impaired annotation removal with arbitrary
// subtitle text. stripHI only ever deletes content (regex spans → "" and
// music-only line drops) and loops its replacements to a fixed point, so two
// invariants must hold for any input: the output is never longer than the
// input, and a second pass is a no-op (idempotent).
func FuzzStripHI(f *testing.F) {
	f.Add("[Sound effect] Hello")
	f.Add("(laughing) World")
	f.Add("♪ Music ♪")
	f.Add("NARRATOR: Dialogue")
	f.Add("JOHN: MARY: stacked labels")
	f.Add("")
	f.Add(strings.Repeat("[", 100))

	f.Fuzz(func(t *testing.T, text string) {
		result := stripHI(text)
		if len(result) > len(text) {
			t.Fatalf("stripHI grew output: in %d bytes, out %d bytes (%q -> %q)",
				len(text), len(result), text, result)
		}
		if second := stripHI(result); second != result {
			t.Fatalf("stripHI not idempotent: %q -> %q -> %q", text, result, second)
		}
	})
}

// FuzzStripTags exercises HTML-like tag removal. No recognized subtitle
// formatting tag (<i>, <b>, <u>) may survive, and the pass is idempotent
// (it loops to a fixed point to absorb tags spliced together by an inner
// removal).
func FuzzStripTags(f *testing.F) {
	f.Add("<i>italic</i>")
	f.Add("<b>bold</b>")
	f.Add("<font color=\"red\">text</font>")
	f.Add("")
	f.Add("<i><b><u>nested</u></b></i>")
	f.Add("</<b0>b>")

	f.Fuzz(func(t *testing.T, text string) {
		result := stripTags(text)
		for _, tag := range []string{"<i>", "</i>", "<b>", "</b>", "<u>", "</u>"} {
			if strings.Contains(result, tag) {
				t.Fatalf("tag %q remains in output: %q", tag, result)
			}
		}
		if second := stripTags(result); second != result {
			t.Fatalf("stripTags not idempotent: %q -> %q -> %q", text, result, second)
		}
	})
}

// FuzzPostProcessIdempotent checks that running the full cue post-process
// pipeline twice yields the same cues as running it once. Idempotency is a
// design requirement (auto-sync may re-process already-clean subtitles).
func FuzzPostProcessIdempotent(f *testing.F) {
	f.Add("Hello world", "[gunshot]", "♪ music ♪")
	f.Add("<i>styled</i>", "JOHN: Hi", "  spaces  ")
	f.Add("", "- dash", "normal text")

	f.Fuzz(func(t *testing.T, t1, t2, t3 string) {
		cues := []Cue{
			{Text: t1, Start: time.Second, End: 2 * time.Second},
			{Text: t2, Start: 3 * time.Second, End: 4 * time.Second},
			{Text: t3, Start: 5 * time.Second, End: 6 * time.Second},
		}
		opts := PostProcessOptions{
			StripHI:         true,
			StripTags:       true,
			CleanWhitespace: true,
			RemoveEmpty:     true,
		}
		once := PostProcess(cues, opts)
		twice := PostProcess(once, opts)
		if len(once) != len(twice) {
			t.Fatalf("PostProcess not idempotent: len(once)=%d len(twice)=%d", len(once), len(twice))
		}
		for i := range once {
			if once[i].Text != twice[i].Text {
				t.Errorf("PostProcess not idempotent at cue %d: once=%q twice=%q", i, once[i].Text, twice[i].Text)
			}
		}
	})
}

// FuzzNormalizeLineEndingsIdempotent checks that line-ending normalization
// reaches a fixed point: once converted to canonical CRLF form, a second pass
// must leave the bytes unchanged.
func FuzzNormalizeLineEndingsIdempotent(f *testing.F) {
	f.Add([]byte("line1\r\nline2\r\n"))
	f.Add([]byte("line1\nline2\n"))
	f.Add([]byte("line1\rline2\r"))
	f.Add([]byte("mixed\r\nlines\nhere\r"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		once := normalizeLineEndings(data)
		twice := normalizeLineEndings(once)
		if !bytes.Equal(once, twice) {
			t.Errorf("normalizeLineEndings not idempotent: once=%q twice=%q", once, twice)
		}
	})
}
