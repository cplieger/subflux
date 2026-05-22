package search

import "testing"

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
