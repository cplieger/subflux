package language

import (
	"fmt"
	"testing"

	"github.com/cplieger/langtag/v3"
)

// seedInputs are raw spellings the round-trip, fold and filename tests and the
// fuzz targets share: canonical tags, decision 16's spellings, the script
// fold's examples, macrolanguage and legacy codes, and junk.
var seedInputs = []string{
	"", "en", "EN", "en-GB", "en-US-u-ca-gregory", "de", "fr", "fr-CA", "fr-FR",
	"pt", "pt-BR", "pt-br", "pt-PT", "pt-MO", "es", "es-419", "es-ES", "es-MX", "es-LA",
	"zh", "chi", "zho", "cmn", "zh-CN", "zh-Hans-CN", "zh-TW", "zh-Hant-TW", "zho-TW", "cmn-TW",
	"zh-HK", "zh-Hant-HK", "zh-Hans", "zh-Hant", "zh-SG", "zh-Hans-SG", "zh-MY", "cmn-MY", "cmn-Hant",
	"zh-Hant-US", "zh-Hant-MO", "yue", "yue-HK", "yue-Hans",
	"ku", "ku-IQ", "kmr-IQ", "ku-Latn-IQ", "zsm-CC", "uzn-AF", "uz", "uz-Cyrl", "az-IR",
	"sr", "srp", "sr-ME", "sr-RU", "sr-Latn", "sr-Cyrl", "sr-Latn-ME", "sh", "hr", "bs",
	"nb", "nob", "nn", "no", "fil", "tl", "cnr", "cnr-Latn", "ber", "la", "lat", "ca",
	"cht", "spl", "pb", "pob", "ea", "spa-la", "mo", "iw", "hi", "sdh", "bih",
	"lang=a:b", "a:b", "x-foo", "und", "zxx", "qaa", "../etc", "en\n", " en ", "İd",
}

// foldExamples maps inputs to the representative Parse folds them to.
var foldExamples = map[string]string{
	"ku-IQ": "ku-Arab-IQ", "sr-ME": "sr-Latn-ME", "zh-MY": "zh-Hans-MY",
	"kmr-IQ": "kmr-Arab-IQ", "zsm-CC": "zsm-Arab-CC", "uzn-AF": "uzn-Arab-AF",
	"zh-SG": "zh-Hans-SG", "cmn-MY": "zh-Hans-MY", "cmn-Hant": "zh-Hant", "cmn": "zh",
	"yue-HK": "yue-Hant-HK", "sr": "sr", "pt-BR": "pt-BR", "zh-Hant-HK": "zh-Hant-HK",
}

// collidingTags are the real tags whose file name reads as another language.
var collidingTags = []string{"cht", "spl", "es-LA"}

func mustParse(t *testing.T, raw string) Tag {
	t.Helper()
	tag, ok := Parse(raw)
	if !ok || (raw != "" && tag == Tag{}) {
		t.Fatalf("Parse(%q) = %q, %v; want a tag", raw, tag, ok)
	}
	return tag
}

// corpusTags is every tag the seeds, the fold examples, the read exceptions
// and Names parse to, the zero tag included.
func corpusTags(t *testing.T) []Tag {
	t.Helper()
	seen := map[Tag]bool{}
	var out []Tag
	add := func(tag Tag) {
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	for _, raw := range seedInputs {
		if tag, ok := Parse(raw); ok {
			add(tag)
		}
	}
	for raw, want := range foldExamples {
		add(mustParse(t, raw))
		add(mustParse(t, want))
	}
	for _, e := range readExceptions {
		add(e.tag)
	}
	for _, n := range Names() {
		add(n.Tag)
	}
	return out
}

func TestZeroTagRoundTrip(t *testing.T) {
	t.Run("Parse of empty input", func(t *testing.T) {
		if tag, ok := Parse(""); !ok || tag != (Tag{}) {
			t.Errorf("Parse(\"\") = %q, %v; want the zero tag and true", tag, ok)
		}
	})
	t.Run("ReadFilename of empty input", func(t *testing.T) {
		if tag, ok := ReadFilename(""); !ok || tag != (Tag{}) {
			t.Errorf("ReadFilename(\"\") = %q, %v; want the zero tag and true", tag, ok)
		}
	})
	t.Run("FilenameTag of the zero tag", func(t *testing.T) {
		if s := FilenameTag(Tag{}); s != "" {
			t.Errorf("FilenameTag(Tag{}) = %q; want \"\"", s)
		}
	})
	t.Run("Parse of the zero tag's text", func(t *testing.T) {
		if tag, ok := Parse(Tag{}.String()); !ok || tag != (Tag{}) {
			t.Errorf("Parse(Tag{}.String()) = %q, %v; want the zero tag and true", tag, ok)
		}
	})
}

func TestScriptFold(t *testing.T) {
	for raw, want := range map[string]string{
		"ku-IQ": "ku-Arab-IQ", "sr-ME": "sr-Latn-ME", "zh-MY": "zh-Hans-MY",
		"kmr-IQ": "kmr-Arab-IQ", "zsm-CC": "zsm-Arab-CC", "uzn-AF": "uzn-Arab-AF",
		"sr": "sr", "pt-BR": "pt-BR", "ku-Latn-IQ": "ku-Latn-IQ",
	} {
		t.Run(raw, func(t *testing.T) {
			if got := mustParse(t, raw).String(); got != want {
				t.Errorf("Parse(%q) = %q; want %q", raw, got, want)
			}
		})
	}
}

func TestChineseFold(t *testing.T) {
	for raw, want := range map[string]string{
		"zh-TW": "zh-Hant", "zho-TW": "zh-Hant", "cmn-TW": "zh-Hant", "zh-Hant-TW": "zh-Hant",
		"zh-CN": "zh-Hans", "zh-Hans-CN": "zh-Hans", "cmn-CN": "zh-Hans", "zh-HK": "zh-Hant-HK",
		"cmn-Hant": "zh-Hant", "chi": "zh", "zho": "zh", "cmn": "zh", "zh-SG": "zh-Hans-SG",
		"cmn-MY": "zh-Hans-MY", "zh-Hans-TW": "zh-Hans-TW", "zh-Hant-HK": "zh-Hant-HK", "yue": "yue",
	} {
		t.Run(raw, func(t *testing.T) {
			if got := mustParse(t, raw).String(); got != want {
				t.Errorf("Parse(%q) = %q; want %q", raw, got, want)
			}
		})
	}
}

func TestParseRefuses(t *testing.T) {
	for _, raw := range []string{"und", "zxx", "qaa", "x-foo", "lang=a:b", "pb", "zz", "İd"} {
		t.Run(raw, func(t *testing.T) {
			if tag, ok := Parse(raw); ok || tag != (Tag{}) {
				t.Errorf("Parse(%q) = %q, %v; want the zero tag and false", raw, tag, ok)
			}
		})
	}
}

func TestParseIsIdempotent(t *testing.T) {
	for _, tag := range corpusTags(t) {
		t.Run(tag.String(), func(t *testing.T) {
			if got, ok := Parse(tag.String()); !ok || got != tag {
				t.Errorf("Parse(%q) = %q, %v; want the same tag", tag, got, ok)
			}
		})
	}
}

// TestNoMultiScriptRegionTagWithoutScript pins the script fold over every
// region langtag accepts, for every MultiScript seed language.
func TestNoMultiScriptRegionTagWithoutScript(t *testing.T) {
	for _, lang := range []string{"zh", "cmn", "sr", "ku", "kmr", "ms", "zsm", "uz", "uzn", "yue", "az", "pa"} {
		t.Run(lang, func(t *testing.T) {
			for _, region := range regionCandidates() {
				lt, ok := langtag.Parse(lang + "-" + region)
				if !ok || lt.Region() == "" {
					continue
				}
				tag := mustParse(t, lang+"-"+region)
				if tag.t.StatedScript() == "" && tag.t.Region() != "" {
					t.Errorf("Parse(%q) = %q states a region and no script", lang+"-"+region, tag)
				}
			}
		})
	}
}

// regionCandidates are AA to ZZ and 000 to 999.
func regionCandidates() []string {
	out := make([]string, 0, 26*26+1000)
	for a := 'A'; a <= 'Z'; a++ {
		for b := 'A'; b <= 'Z'; b++ {
			out = append(out, string([]rune{a, b}))
		}
	}
	for n := range 1000 {
		out = append(out, fmt.Sprintf("%03d", n))
	}
	return out
}
