package coverage

import (
	"testing"

	"subflux/internal/api"
)

func FuzzExtractSeriesPrefix(f *testing.F) {
	f.Add("tvdb-12345-s01e01")
	f.Add("imdb-tt1234-s02e03")
	f.Add("")
	f.Add("no-season-episode")
	f.Add("-s")
	f.Add("abc-s1e1-s2e2")

	f.Fuzz(func(t *testing.T, epMediaID string) {
		result := ExtractSeriesPrefix(epMediaID)
		if result != "" && len(result) > len(epMediaID) {
			t.Errorf("prefix longer than input: %q > %q", result, epMediaID)
		}
	})
}

func FuzzResolveRuleName(f *testing.F) {
	f.Add("eng")
	f.Add("")
	f.Add("fra")

	f.Fuzz(func(t *testing.T, audioLang string) {
		r := ResolveRuleName(audioLang, nil)
		if r != RuleNoTargets {
			t.Errorf("nil targets should return RuleNoTargets, got %q", r)
		}
	})
}

func FuzzDeduplicateFileRows(f *testing.F) {
	f.Add("mid1", "eng", "normal", "opensubtitles", "mid2", "fra", "", "manual")

	f.Fuzz(func(t *testing.T, mid1, lang1, var1, src1, mid2, lang2, var2, src2 string) {
		rows := []api.SubtitleFileRow{
			{MediaID: mid1, Language: lang1, Variant: var1, Source: src1},
			{MediaID: mid2, Language: lang2, Variant: var2, Source: src2},
			{MediaID: mid1, Language: lang1, Variant: var1, Source: src1},
		}
		result := DeduplicateFileRows(rows)
		if len(result) > len(rows) {
			t.Errorf("dedup produced more rows than input")
		}
	})
}

func FuzzIndexSubStatus(f *testing.F) {
	f.Add("mid1", "eng", "", "opensubtitles", "srt", "mid1", "eng", "", "embedded", "hdmv_pgs_subtitle")

	f.Fuzz(func(t *testing.T, mid1, lang1, var1, src1, codec1, mid2, lang2, var2, src2, codec2 string) {
		rows := []api.SubtitleFileRow{
			{MediaID: mid1, Language: lang1, Variant: var1, Source: src1, Codec: codec1},
			{MediaID: mid2, Language: lang2, Variant: var2, Source: src2, Codec: codec2},
		}
		ignored := map[string]bool{codec2: true}
		idx := IndexSubStatus(rows, ignored)
		// Should not panic; result keys should exist in input
		for mediaID := range idx {
			if mediaID != mid1 && mediaID != mid2 {
				t.Errorf("unexpected mediaID in index: %q", mediaID)
			}
		}
	})
}
