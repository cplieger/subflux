package subtitlefile_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subtitleext"
	"github.com/cplieger/subflux/internal/subtitlefile"
	"github.com/cplieger/subflux/internal/timing"
)

func tag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("language.Parse(%q) failed", s)
	}
	return tg
}

func mustBuild(t *testing.T, stem string, n subtitlefile.Name) string {
	t.Helper()
	name, err := subtitlefile.Build(stem, n)
	if err != nil {
		t.Fatalf("Build(%q, %+v) = %v", stem, n, err)
	}
	return name
}

func TestParseSDHAloneIsKurdish(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file string
		want       subtitlefile.Name
	}{
		{"sdh first is the language", "movie.sdh.srt", subtitlefile.Name{Tag: tag(t, "sdh"), Ext: subtitlefile.ExtSRT}},
		{"sdh after a language is HI", "movie.en.sdh.srt", subtitlefile.Name{Tag: tag(t, "en"), Flags: language.Flags{HI: true}, Ext: subtitlefile.ExtSRT}},
		{"hi first is Hindi", "movie.hi.srt", subtitlefile.Name{Tag: tag(t, "hi"), Ext: subtitlefile.ExtSRT}},
		{"Kurdish HI", "movie.sdh.sdh.srt", subtitlefile.Name{Tag: tag(t, "sdh"), Flags: language.Flags{HI: true}, Ext: subtitlefile.ExtSRT}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := subtitlefile.Parse("movie", tc.file)
			if !ok || got != tc.want {
				t.Errorf("Parse(movie, %q) = %+v, %v; want %+v, true", tc.file, got, ok, tc.want)
			}
		})
	}
}

func TestBuildWritesSDH(t *testing.T) {
	t.Parallel()
	en := tag(t, "en")
	both := subtitlefile.Name{Tag: en, Flags: language.Flags{HI: true, Forced: true}, Ext: subtitlefile.ExtSRT}
	if got := mustBuild(t, "movie", both); got != "movie.en.sdh.forced.srt" {
		t.Errorf("Build(HI and Forced) = %q, want movie.en.sdh.forced.srt", got)
	}
	read, ok := subtitlefile.Parse("movie", "movie.en.hi.srt")
	want := subtitlefile.Name{Tag: en, Flags: language.Flags{HI: true}, Ext: subtitlefile.ExtSRT}
	if !ok || read != want {
		t.Fatalf("Parse(movie.en.hi.srt) = %+v, %v; want %+v", read, ok, want)
	}
	if got := mustBuild(t, "movie", read); got != "movie.en.sdh.srt" {
		t.Errorf("Build(Parse(movie.en.hi.srt)) = %q, want movie.en.sdh.srt", got)
	}
}

func TestParseGrammar(t *testing.T) {
	t.Parallel()
	en := tag(t, "en")
	for _, tc := range []struct {
		name, stem, file string
		want             subtitlefile.Name
		ok               bool
	}{
		{"untagged", "Movie", "Movie.srt", subtitlefile.Name{Ext: subtitlefile.ExtSRT}, true},
		{"tagged ass", "Movie", "Movie.en.ass", subtitlefile.Name{Tag: en, Ext: subtitlefile.ExtASS}, true},
		{"ssa upper case", "Movie", "Movie.EN.SSA", subtitlefile.Name{Tag: en, Ext: subtitlefile.ExtSSA}, true},
		{"vobsub", "Movie", "Movie.en.sub", subtitlefile.Name{Tag: en, Ext: subtitlefile.ExtVobSub}, true},
		{"cc is HI", "Movie", "Movie.en.cc.srt", subtitlefile.Name{Tag: en, Flags: language.Flags{HI: true}, Ext: subtitlefile.ExtSRT}, true},
		{"foreign is Forced", "Movie", "Movie.en.foreign.srt", subtitlefile.Name{Tag: en, Flags: language.Flags{Forced: true}, Ext: subtitlefile.ExtSRT}, true},
		{"flags in either order", "Movie", "Movie.en.forced.SDH.2.srt", subtitlefile.Name{Tag: en, Flags: language.Flags{HI: true, Forced: true}, Ordinal: 2, Ext: subtitlefile.ExtSRT}, true},
		{"alias spelling", "Movie", "Movie.pb.srt", subtitlefile.Name{Tag: tag(t, "pt-BR"), Ext: subtitlefile.ExtSRT}, true},
		{"stem with dots", "Show.S01E01.1080p", "Show.S01E01.1080p.fr.srt", subtitlefile.Name{Tag: tag(t, "fr"), Ext: subtitlefile.ExtSRT}, true},
		{"other stem", "Movie", "Other.en.srt", subtitlefile.Name{}, false},
		{"stem without dot", "Movie", "Movies.en.srt", subtitlefile.Name{}, false},
		{"vtt is not on disk", "Movie", "Movie.en.vtt", subtitlefile.Name{}, false},
		{"not a subtitle", "Movie", "Movie.en.nfo", subtitlefile.Name{}, false},
		{"empty segment", "Movie", "Movie..srt", subtitlefile.Name{}, false},
		{"flag first", "Movie", "Movie.forced.srt", subtitlefile.Name{}, false},
		{"unknown segment", "Movie", "Movie.en.director.srt", subtitlefile.Name{}, false},
		{"repeated HI", "Movie", "Movie.en.hi.sdh.srt", subtitlefile.Name{}, false},
		{"repeated Forced", "Movie", "Movie.en.forced.foreign.srt", subtitlefile.Name{}, false},
		{"ordinal before a flag", "Movie", "Movie.en.2.forced.srt", subtitlefile.Name{}, false},
		{"too many segments", "Movie", "Movie.en.hi.forced.cc.srt", subtitlefile.Name{}, false},
		{"language name", "Movie", "Movie.English.srt", subtitlefile.Name{}, false},
		{"suffix after ext", "Movie", "Movie.en.srt.bak", subtitlefile.Name{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := subtitlefile.Parse(tc.stem, tc.file)
			if ok != tc.ok || got != tc.want {
				t.Errorf("Parse(%q, %q) = %+v, %v; want %+v, %v", tc.stem, tc.file, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestOrdinalFromOne(t *testing.T) {
	t.Parallel()
	en := tag(t, "en")
	for _, tc := range []struct {
		name, file string
		ordinal    int
		ok         bool
	}{
		{"unnumbered", "m.en.srt", 0, true},
		{"one", "m.en.1.srt", 1, true},
		{"twelve after flags", "m.en.sdh.forced.12.srt", 12, true},
		{"zero", "m.en.0.srt", 0, false},
		{"leading zero", "m.en.01.srt", 0, false},
		{"ten digits", "m.en.1234567890.srt", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := subtitlefile.Parse("m", tc.file)
			if ok != tc.ok || got.Ordinal != tc.ordinal {
				t.Errorf("Parse(m, %q) = %+v, %v; want ordinal %d, %v", tc.file, got, ok, tc.ordinal, tc.ok)
			}
		})
	}
	if got := mustBuild(t, "m", subtitlefile.Name{Tag: en, Ext: subtitlefile.ExtSRT}); got != "m.en.srt" {
		t.Errorf("Build(ordinal 0) = %q, want m.en.srt", got)
	}
	if got := mustBuild(t, "m", subtitlefile.Name{Tag: en, Ordinal: 3, Ext: subtitlefile.ExtSRT}); got != "m.en.3.srt" {
		t.Errorf("Build(ordinal 3) = %q, want m.en.3.srt", got)
	}
	top := subtitlefile.Name{Tag: en, Ordinal: 999_999_999, Ext: subtitlefile.ExtSRT}
	if got, ok := subtitlefile.Parse("m", mustBuild(t, "m", top)); !ok || got != top {
		t.Errorf("Parse(Build(ordinal 999,999,999)) = %+v, %v; want %+v", got, ok, top)
	}
}

func TestBuildRefusals(t *testing.T) {
	t.Parallel()
	en := tag(t, "en")
	for _, tc := range []struct {
		name string
		n    subtitlefile.Name
		want error
	}{
		{"zero tag", subtitlefile.Name{Ext: subtitlefile.ExtSRT}, subtitlefile.ErrZeroTag},
		{"tag read back as another", subtitlefile.Name{Tag: tag(t, "cht"), Ext: subtitlefile.ExtSRT}, subtitlefile.ErrTagUnreadable},
		{"negative ordinal", subtitlefile.Name{Tag: en, Ordinal: -1, Ext: subtitlefile.ExtSRT}, subtitlefile.ErrOrdinal},
		{"ten-digit ordinal", subtitlefile.Name{Tag: en, Ordinal: 1_000_000_000, Ext: subtitlefile.ExtSRT}, subtitlefile.ErrOrdinal},
		{"vobsub", subtitlefile.Name{Tag: en, Ext: subtitlefile.ExtVobSub}, subtitlefile.ErrNotWritable},
		{"zero ext", subtitlefile.Name{Tag: en}, subtitlefile.ErrNotWritable},
		{"unknown ext", subtitlefile.Name{Tag: en, Ext: subtitlefile.ExtVobSub + 1}, subtitlefile.ErrNotWritable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, err := subtitlefile.Build("m", tc.n); !errors.Is(err, tc.want) || got != "" {
				t.Errorf("Build(%+v) = %q, %v; want %v", tc.n, got, err, tc.want)
			}
		})
	}
	if got := mustBuild(t, "m", subtitlefile.Name{Tag: tag(t, "zh-Hans"), Ext: subtitlefile.ExtSRT}); got != "m.zh-CN.srt" {
		t.Errorf("Build(zh-Hans) = %q, want m.zh-CN.srt", got)
	}
}

func TestExtOfFormat(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		f    timing.Format
		want subtitlefile.Ext
		text string
	}{
		{timing.SRT, subtitlefile.ExtSRT, ".srt"},
		{timing.ASS, subtitlefile.ExtASS, ".ass"},
		{timing.SSA, subtitlefile.ExtSSA, ".ssa"},
		{0, 0, ""},
	} {
		got := subtitlefile.ExtOf(tc.f)
		if got != tc.want || got.String() != tc.text {
			t.Errorf("ExtOf(%d) = %d %q, want %d %q", tc.f, got, got.String(), tc.want, tc.text)
		}
		if tc.f != 0 && !subtitleext.WriterOutput("m"+got.String()) {
			t.Errorf("subtitleext.WriterOutput(%q) = false for a writer's extension", got.String())
		}
	}
	if subtitleext.WriterOutput("m" + subtitlefile.ExtVobSub.String()) {
		t.Error("subtitleext.WriterOutput(.sub) = true, but no writer produces it")
	}
	var parsed []string
	for _, e := range subtitleext.Extensions() {
		if n, ok := subtitlefile.Parse("m", "m"+e); ok {
			parsed = append(parsed, n.Ext.String())
		} else if subtitleext.OnDisk("m" + e) {
			t.Errorf("Parse(m%s) = false for an on-disk extension", e)
		}
	}
	if want := []string{".ass", ".srt", ".ssa", ".sub"}; !slices.Equal(parsed, want) {
		t.Errorf("parsed extensions = %v, want %v", parsed, want)
	}
	if got := subtitlefile.Ext(9).String(); got != "" {
		t.Errorf("Ext(9).String() = %q, want empty", got)
	}
}
