package release

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// normalizeSet collects the normalized labels a format table can produce, so a
// fuzz test can assert ParseReleaseName only ever emits known labels.
func normalizeSet(formats []Format) map[string]bool {
	m := make(map[string]bool, len(formats))
	for _, ft := range formats {
		m[ft.Normalize] = true
	}
	return m
}

func FuzzParseReleaseName(f *testing.F) {
	f.Add("Show.S01E05.720p.WEB-DL.DDP5.1.H.264-GROUP")
	f.Add("Movie.2024.2160p.UHD.BluRay.x265.HDR.DTS-HD.MA.7.1-RELEASE")
	f.Add("[SubGroup] Anime Title - 25 [1080p][HEVC]")
	f.Add("Show.S03.COMPLETE.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb")
	f.Add("")
	f.Add("...---...///\\\\")
	f.Add("A.Very.Long.Title.With.Many.Dots.And.Numbers.123.456.789.S01E01.720p")

	validSource := normalizeSet(CompiledSources)
	validCodec := normalizeSet(CompiledVideoCodecs)
	validHDR := normalizeSet(CompiledHDR)
	validStreaming := normalizeSet(CompiledStreaming)

	f.Fuzz(func(t *testing.T, name string) {
		info := ParseReleaseName(name)
		// Bounded output: every detected attribute is one of the known
		// normalized labels for its category (or empty when nothing matched).
		if info.Source != "" && !validSource[info.Source] {
			t.Errorf("ParseReleaseName(%q).Source = %q, not a known source label", name, info.Source)
		}
		if info.VideoCodec != "" && !validCodec[info.VideoCodec] {
			t.Errorf("ParseReleaseName(%q).VideoCodec = %q, not a known codec label", name, info.VideoCodec)
		}
		if info.HDR != "" && !validHDR[info.HDR] {
			t.Errorf("ParseReleaseName(%q).HDR = %q, not a known HDR label", name, info.HDR)
		}
		if info.StreamingService != "" && !validStreaming[info.StreamingService] {
			t.Errorf("ParseReleaseName(%q).StreamingService = %q, not a known streaming label", name, info.StreamingService)
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
		group := ParseReleaseGroup(name)
		if group == "" {
			return
		}
		// Provenance: an extracted group is always a literal substring of the
		// extension-stripped input, never fabricated or transformed.
		stripped := FileExtRe.ReplaceAllString(name, "")
		if !strings.Contains(stripped, group) {
			t.Errorf("ParseReleaseGroup(%q) = %q, not a substring of %q", name, group, stripped)
		}
	})
}

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
		CompareSource(&m, a, b)
		// Symmetry: CompareSource(a,b) agrees with CompareSource(b,a).
		var m2 api.MatchSet
		CompareSource(&m2, b, a)
		if m.Source != m2.Source {
			t.Fatalf("CompareSource not symmetric: (%q,%q)=%v vs (%q,%q)=%v", a, b, m.Source, b, a, m2.Source)
		}
		// An empty operand can never produce a source match.
		if (a == "" || b == "") && m.Source {
			t.Fatal("CompareSource set Source=true with an empty operand")
		}
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
		// Idempotent: a family label maps to itself.
		if result2 := SourceOrFamily(result); result != result2 {
			t.Fatalf("SourceOrFamily not idempotent: %q -> %q -> %q", src, result, result2)
		}
	})
}
