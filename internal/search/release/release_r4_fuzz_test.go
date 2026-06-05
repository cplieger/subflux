package release

import (
	"testing"

	"subflux/internal/api"
)

func FuzzCompareSource(f *testing.F) {
	f.Add("bluray", "bluray")
	f.Add("web-dl", "webrip")
	f.Add("hdtv", "sdtv")
	f.Add("", "bluray")
	f.Add("bluray", "")
	f.Add("", "")
	f.Add("unknown", "unknown")

	f.Fuzz(func(t *testing.T, a, b string) {
		var m api.MatchSet
		// Must not panic.
		CompareSource(&m, a, b)
		// Symmetry: CompareSource(a,b) == CompareSource(b,a).
		var m2 api.MatchSet
		CompareSource(&m2, b, a)
		if m.Source != m2.Source {
			t.Fatalf("CompareSource not symmetric: (%q,%q)=%v vs (%q,%q)=%v", a, b, m.Source, b, a, m2.Source)
		}
		// If either is empty, Source must be false.
		if (a == "" || b == "") && m.Source {
			t.Fatal("CompareSource set Source=true with empty input")
		}
	})
}

func FuzzParseReleaseGroup(f *testing.F) {
	f.Add("Movie.2024.BluRay.x264-GRP")
	f.Add("[SubGroup] Anime - 01")
	f.Add("Show.S01E01.720p.WEB-DL.DDP5.1.H.264-NTb")
	f.Add("")
	f.Add("no-group-here")

	f.Fuzz(func(t *testing.T, name string) {
		// Must not panic.
		_ = ParseReleaseGroup(name)
	})
}

func FuzzSourceOrFamily(f *testing.F) {
	f.Add("bluray")
	f.Add("web-dl")
	f.Add("webrip")
	f.Add("hdtv")
	f.Add("")
	f.Add("unknown_source")

	f.Fuzz(func(t *testing.T, src string) {
		result := SourceOrFamily(src)
		// Idempotent: SourceOrFamily(SourceOrFamily(x)) == SourceOrFamily(x)
		result2 := SourceOrFamily(result)
		if result != result2 {
			t.Fatalf("SourceOrFamily not idempotent: %q -> %q -> %q", src, result, result2)
		}
	})
}
