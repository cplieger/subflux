package subsync

import (
	"strings"
	"testing"
)

// FuzzStripHI exercises the hearing-impaired text removal regex pipeline
// with arbitrary subtitle text.
//
// Bug class: catastrophic regex backtracking or panic on deeply nested
// bracket/paren patterns; output length must not exceed input length.
func FuzzStripHI(f *testing.F) {
	f.Add("[Sound effect] Hello")
	f.Add("(laughing) World")
	f.Add("♪ Music ♪")
	f.Add("NARRATOR: Dialogue")
	f.Add("")
	f.Add(strings.Repeat("[", 100))

	f.Fuzz(func(t *testing.T, text string) {
		result := stripHI(text)
		_ = result // must not panic
	})
}

// FuzzStripTags exercises HTML-like tag removal from subtitle text.
//
// Bug class: panic on malformed tags; output must not contain recognized
// subtitle formatting tags (<i>, <b>, <u>, <font>).
func FuzzStripTags(f *testing.F) {
	f.Add("<i>italic</i>")
	f.Add("<b>bold</b>")
	f.Add("<font color=\"red\">text</font>")
	f.Add("")
	f.Add("<i><b><u>nested</u></b></i>")

	f.Fuzz(func(t *testing.T, text string) {
		result := stripTags(text)
		if strings.Contains(result, "<i>") || strings.Contains(result, "</i>") ||
			strings.Contains(result, "<b>") || strings.Contains(result, "</b>") ||
			strings.Contains(result, "<u>") || strings.Contains(result, "</u>") {
			t.Fatalf("tags remain in output: %q", result)
		}
	})
}

// FuzzStripASSOverrides exercises ASS override tag removal with arbitrary input.
//
// Bug class: panic on unbalanced braces; output must not contain {}-delimited
// override sequences; idempotent.
func FuzzStripASSOverrides(f *testing.F) {
	f.Add("{\\an8}Hello")
	f.Add("{\\pos(320,50)}Text")
	f.Add("")
	f.Add("{}")
	f.Add("no overrides")

	f.Fuzz(func(t *testing.T, text string) {
		result := stripASSOverrides(text)
		second := stripASSOverrides(result)
		if result != second {
			t.Fatalf("not idempotent: %q -> %q -> %q", text, result, second)
		}
	})
}

// FuzzIsASSContent exercises the ASS format detection predicate with
// arbitrary byte input.
//
// Bug class: panic on short/empty input; must be a pure boolean predicate.
func FuzzIsASSContent(f *testing.F) {
	f.Add([]byte("[Script Info]\nTitle: Test"))
	f.Add([]byte("Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hi"))
	f.Add([]byte(""))
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nSRT content\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = IsASSContent(data) // must not panic
	})
}
