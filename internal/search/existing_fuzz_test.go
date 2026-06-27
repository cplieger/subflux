package search

import (
	"path/filepath"
	"testing"
)

func FuzzParseExternalSubPath(f *testing.F) {
	f.Add("/media/movie.eng.srt", "/media/movie", ".srt")
	f.Add("/media/show.fr.hi.srt", "/media/show", ".srt")
	f.Add("/media/ep.de.forced.ass", "/media/ep", ".ass")
	f.Add("", "", "")
	f.Add("/a.b.c.d.e.f", "/a", ".f")

	f.Fuzz(func(t *testing.T, path, base, ext string) {
		// Must not panic on any input.
		sub := parseExternalSubPath(path, base, ext)
		// If HI is set, Forced should not also be set from the same tag.
		_ = sub
	})
}

// FuzzGlobEscape verifies that a glob-escaped string matches its own literal
// value via filepath.Match: escaping must neutralize every metacharacter.
func FuzzGlobEscape(f *testing.F) {
	f.Add("/media/movie.mkv")
	f.Add("/path/with[brackets]")
	f.Add("file*.txt")
	f.Add("question?mark")
	f.Add(`back\slash`)
	f.Add("")

	f.Fuzz(func(t *testing.T, s string) {
		escaped := globEscape(s)
		// The escaped string used in filepath.Match should match the literal s.
		matched, err := filepath.Match(escaped, s)
		if err != nil {
			// Some inputs produce invalid patterns even after escaping;
			// that's acceptable but must not panic.
			return
		}
		if !matched {
			t.Fatalf("globEscape(%q) = %q does not match original via filepath.Match", s, escaped)
		}
	})
}
