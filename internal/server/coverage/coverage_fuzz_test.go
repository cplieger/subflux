package coverage

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzExtractSeriesPrefix asserts the full postcondition: a non-empty result is
// always a prefix of the input that ends at a "-s" boundary (ends with "-",
// followed by 's' in the input).
func FuzzExtractSeriesPrefix(f *testing.F) {
	f.Add("tvdb-12345-s01e01")
	f.Add("imdb-tt1234-s02e03")
	f.Add("")
	f.Add("no-season-episode")
	f.Add("-s")
	f.Add("abc-s1e1-s2e2")

	f.Fuzz(func(t *testing.T, epMediaID string) {
		result := ExtractSeriesPrefix(epMediaID)
		if result == "" {
			return
		}
		if !strings.HasPrefix(epMediaID, result) {
			t.Fatalf("ExtractSeriesPrefix(%q) = %q, not a prefix of input", epMediaID, result)
		}
		if !strings.HasSuffix(result, "-") {
			t.Errorf("ExtractSeriesPrefix(%q) = %q, want trailing '-'", epMediaID, result)
		}
		if len(epMediaID) <= len(result) || epMediaID[len(result)] != 's' {
			t.Errorf("ExtractSeriesPrefix(%q) = %q, want input[%d] == 's'", epMediaID, result, len(result))
		}
	})
}

// FuzzResolveRuleName asserts the rule-name selector across all three branches
// using the fuzzed audio language as the oracle.
func FuzzResolveRuleName(f *testing.F) {
	f.Add("eng")
	f.Add("")
	f.Add("fra")

	f.Fuzz(func(t *testing.T, audioLang string) {
		if r := ResolveRuleName(audioLang, nil); r != RuleNoTargets {
			t.Errorf("ResolveRuleName(%q, nil) = %q, want %q", audioLang, r, RuleNoTargets)
		}
		targets := []api.SubtitleTarget{{Code: "en"}}
		got := ResolveRuleName(audioLang, targets)
		switch {
		case audioLang == "" && got != RuleDefault:
			t.Errorf("ResolveRuleName(\"\", targets) = %q, want %q", got, RuleDefault)
		case audioLang != "" && got != audioLang:
			t.Errorf("ResolveRuleName(%q, targets) = %q, want %q", audioLang, got, audioLang)
		}
	})
}

// FuzzDeduplicateFileRows asserts idempotence and that the output contains no
// duplicate (media, language, variant, source) keys.
func FuzzDeduplicateFileRows(f *testing.F) {
	f.Add("mid1", "eng", "normal", "opensubtitles", "mid2", "fra", "", "manual")

	f.Fuzz(func(t *testing.T, mid1, lang1, var1, src1, mid2, lang2, var2, src2 string) {
		rows := []api.SubtitleEntry{
			{MediaID: mid1, Language: lang1, Variant: var1, Source: src1},
			{MediaID: mid2, Language: lang2, Variant: var2, Source: src2},
			{MediaID: mid1, Language: lang1, Variant: var1, Source: src1},
		}
		result := DeduplicateFileRows(rows)
		if len(result) > len(rows) {
			t.Fatalf("dedup produced more rows than input: %d > %d", len(result), len(rows))
		}

		type key struct{ mediaID, lang, variant, source string }
		seen := make(map[key]bool, len(result))
		for _, r := range result {
			k := key{r.MediaID, r.Language, r.Variant, r.Source}
			if seen[k] {
				t.Errorf("dedup output contains duplicate key %+v", k)
			}
			seen[k] = true
		}

		// Idempotence: deduplicating an already-deduplicated slice is a no-op.
		if again := DeduplicateFileRows(result); !reflect.DeepEqual(again, result) {
			t.Errorf("dedup not idempotent: %+v != %+v", again, result)
		}
	})
}

// FuzzIndexSubStatus asserts that every indexed status belongs to an input
// media ID and has exactly one of Usable/IgnoredOnly set (never both, never
// neither).
func FuzzIndexSubStatus(f *testing.F) {
	f.Add("m1", "en", "", "external", "srt", "m2", "fr", "", "embedded", "pgs")
	f.Add("vid", "en", "", "embedded", "ass", "vid", "en", "", "embedded", "ass")
	f.Add("a", "en", "", "external", "srt", "a", "en", "", "embedded", "subrip")

	f.Fuzz(func(t *testing.T, mid1, lang1, var1, src1, codec1, mid2, lang2, var2, src2, codec2 string) {
		rows := []api.SubtitleEntry{
			{MediaID: mid1, Language: lang1, Variant: var1, Source: src1, Codec: codec1},
			{MediaID: mid2, Language: lang2, Variant: var2, Source: src2, Codec: codec2},
		}
		ignored := map[string]bool{codec2: true}
		idx := IndexSubStatus(rows, ignored)

		for mediaID, byKey := range idx {
			if mediaID != mid1 && mediaID != mid2 {
				t.Errorf("unexpected mediaID in index: %q", mediaID)
			}
			for k, st := range byKey {
				if st == nil {
					t.Fatalf("nil status for media %q key %+v", mediaID, k)
				}
				if st.Usable == st.IgnoredOnly {
					t.Errorf("media %q key %+v: Usable=%v IgnoredOnly=%v, want exactly one true",
						mediaID, k, st.Usable, st.IgnoredOnly)
				}
			}
		}
	})
}
