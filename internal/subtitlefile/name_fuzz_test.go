package subtitlefile_test

import (
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subtitlefile"
)

func FuzzBuildParseRoundTrip(f *testing.F) {
	f.Add("Movie", "en", false, false, uint32(0), uint8(0))
	f.Add("Show.S01E01", "sdh", true, true, uint32(2), uint8(1))
	f.Add("m", "zh-Hans", false, true, uint32(12), uint8(2))
	f.Add("m", "cht", false, false, uint32(0), uint8(0))
	f.Add("a.b", "hi", true, false, uint32(1), uint8(1))
	f.Add("m", "en", false, false, uint32(999_999_999), uint8(0))
	f.Add("m", "en", false, false, uint32(1_000_000_000), uint8(0))
	f.Fuzz(func(t *testing.T, stem, lang string, hi, forced bool, ord uint32, ext uint8) {
		tg, ok := language.Parse(lang)
		if !ok {
			return
		}
		n := subtitlefile.Name{
			Tag:     tg,
			Flags:   language.Flags{HI: hi, Forced: forced},
			Ordinal: int(ord),
			Ext:     subtitlefile.ExtSRT + subtitlefile.Ext(ext%3),
		}
		name, err := subtitlefile.Build(stem, n)
		if tg == (language.Tag{}) {
			if !errors.Is(err, subtitlefile.ErrZeroTag) {
				t.Fatalf("Build(zero tag) = %q, %v; want ErrZeroTag", name, err)
			}
			return
		}
		if errors.Is(err, subtitlefile.ErrTagUnreadable) {
			if back, ok := language.ReadFilename(language.FilenameTag(tg)); ok && back == tg {
				t.Fatalf("Build(%+v) = ErrTagUnreadable for a tag that reads back", n)
			}
			return
		}
		if n.Ordinal > 999_999_999 {
			if !errors.Is(err, subtitlefile.ErrOrdinal) {
				t.Fatalf("Build(%+v) = %q, %v; want ErrOrdinal", n, name, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("Build(%q, %+v) = %v", stem, n, err)
		}
		if got, ok := subtitlefile.Parse(stem, name); !ok || got != n {
			t.Fatalf("Parse(%q, Build = %q) = %+v, %v; want %+v", stem, name, got, ok, n)
		}
		if owner, ok := subtitlefile.Owner([]string{stem}, name); !ok || owner != stem {
			t.Fatalf("Owner([%q], %q) = %q, %v", stem, name, owner, ok)
		}
	})
}
