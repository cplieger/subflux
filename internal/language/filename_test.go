package language

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/langtag/v3"
)

func TestReadExceptionsOutsideParse(t *testing.T) {
	t.Run("Parse keeps the colliding tags", func(t *testing.T) {
		for raw, want := range map[string]string{"cht": "cht", "spl": "spl", "es-LA": "es-LA", "es-la": "es-LA"} {
			if got := mustParse(t, raw).String(); got != want {
				t.Errorf("Parse(%q) = %q; want %q", raw, got, want)
			}
		}
	})
	t.Run("Parse refuses pb", func(t *testing.T) {
		if tag, ok := Parse("pb"); ok {
			t.Errorf("Parse(\"pb\") = %q, true; want false", tag)
		}
	})
	t.Run("ReadFilename reads the colliding segments by their common meaning", func(t *testing.T) {
		for seg, want := range map[string]string{"cht": "zh-Hant", "spl": "es-419", "es-la": "es-419", "pb": "pt-BR"} {
			if got, ok := ReadFilename(seg); !ok || got.String() != want {
				t.Errorf("ReadFilename(%q) = %q, %v; want %q", seg, got, ok, want)
			}
		}
	})
	t.Run("no other canonical text is an exception key", func(t *testing.T) {
		for _, raw := range primaryCandidates() {
			tag, ok := Parse(raw)
			if !ok || slices.Contains(collidingTags, tag.String()) {
				continue
			}
			if _, isKey := readExceptions[strings.ToLower(tag.String())]; isKey {
				t.Errorf("Parse(%q) = %q, whose lowercased text is a read exception key", raw, tag)
			}
		}
	})
}

func TestExceptionRowsNeeded(t *testing.T) {
	for _, r := range exceptionRows {
		for _, k := range r.spellings {
			t.Run(k, func(t *testing.T) {
				read, _ := ReadFilename(k)
				if parsed, ok := Parse(k); ok && parsed == read {
					t.Errorf("row %q reads as %q, which Parse alone already answers", k, read)
				}
			})
		}
	}
}

func TestExceptionRows(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range exceptionRows {
		t.Run(r.tag, func(t *testing.T) {
			if want, got := mustParse(t, r.tag), readExceptions[r.spellings[0]].tag; got != want || want.String() != r.tag {
				t.Errorf("row %q reads as %q; want Parse's canonical %q", r.tag, got, want)
			}
			if !strings.Contains(r.evidence, "https://") {
				t.Errorf("row %q cites no evidence link: %q", r.tag, r.evidence)
			}
			for _, k := range r.spellings {
				if k != strings.ToLower(k) || seen[k] {
					t.Errorf("spelling %q is not lowercase or is listed twice", k)
				}
				seen[k] = true
			}
		})
	}
	if len(readExceptions) != len(seen) {
		t.Errorf("readExceptions holds %d keys; the rows list %d", len(readExceptions), len(seen))
	}
}

func TestAliasesAnyCase(t *testing.T) {
	for k, e := range readExceptions {
		want := e.tag
		for _, seg := range []string{k, strings.ToUpper(k), mixedCase(k)} {
			t.Run(seg, func(t *testing.T) {
				if got, ok := ReadFilename(seg); !ok || got != want {
					t.Errorf("ReadFilename(%q) = %q, %v; want %q", seg, got, ok, want)
				}
			})
		}
	}
}

func mixedCase(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i += 2 {
		if 'a' <= b[i] && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}

func TestFilenameTagSpellings(t *testing.T) {
	for raw, want := range map[string]string{
		"zh-Hans": "zh-CN", "zh-Hant": "zh-TW", "zh-Hant-HK": "zh-HK", "zh-Hans-SG": "zh-Hans-SG",
		"zh-MY": "zh-Hans-MY", "zh-Hant-US": "zh-Hant-US", "ku-IQ": "ku-Arab-IQ", "pt-BR": "pt-BR",
		"es-419": "es-419", "fr-CA": "fr-CA", "nb": "nb", "nn": "nn", "sr-Latn": "sr-Latn",
		"yue": "yue", "fil": "fil", "zh": "zh", "sr": "sr",
	} {
		t.Run(raw, func(t *testing.T) {
			tag := mustParse(t, raw)
			if got := FilenameTag(tag); got != want {
				t.Errorf("FilenameTag(%q) = %q; want %q", tag, got, want)
			}
			if back, ok := ReadFilename(want); !ok || back != tag {
				t.Errorf("ReadFilename(%q) = %q, %v; want %q", want, back, ok, tag)
			}
		})
	}
	for raw, want := range map[string]string{
		"zh-TW": "zh-Hant", "zho-TW": "zh-Hant", "cmn-TW": "zh-Hant", "zh-Hant-TW": "zh-Hant",
		"zh-CN": "zh-Hans", "zh-Hans-CN": "zh-Hans", "zh-HK": "zh-Hant-HK",
	} {
		t.Run("reads back "+raw, func(t *testing.T) {
			if got, ok := ReadFilename(raw); !ok || got.String() != want {
				t.Errorf("ReadFilename(%q) = %q, %v; want %q", raw, got, ok, want)
			}
		})
	}
}

func TestRegionTagsStateScript(t *testing.T) {
	exempt := map[string]string{}
	for _, tag := range corpusTags(t) {
		out := FilenameTag(tag)
		lt, ok := langtag.Parse(out)
		if !ok || !lt.MultiScript() || lt.Region() == "" || lt.StatedScript() != "" {
			continue
		}
		exempt[out] = tag.String()
	}
	want := map[string]string{"zh-CN": "zh-Hans", "zh-TW": "zh-Hant", "zh-HK": "zh-Hant-HK"}
	if fmt.Sprint(exempt) != fmt.Sprint(want) {
		t.Errorf("FilenameTag outputs stating a region and no script = %v; want exactly %v", exempt, want)
	}
	for out, tag := range want {
		if got, ok := ReadFilename(out); !ok || got.String() != tag {
			t.Errorf("ReadFilename(%q) = %q, %v; want %q", out, got, ok, tag)
		}
	}
}

func TestFilenameRoundTrip(t *testing.T) {
	for _, tag := range corpusTags(t) {
		if slices.Contains(collidingTags, tag.String()) {
			continue
		}
		t.Run(tag.String(), func(t *testing.T) {
			if got, ok := ReadFilename(FilenameTag(tag)); !ok || got != tag {
				t.Errorf("ReadFilename(FilenameTag(%q)) = %q, %v", tag, got, ok)
			}
		})
	}
}
