package coverage

import (
	"testing"

	"subflux/internal/api"
)

func FuzzExtractSeriesPrefix(f *testing.F) {
	f.Add("tvdb-12345-s01e01")
	f.Add("imdb-tt1234567-s02e03")
	f.Add("s01e01")
	f.Add("")
	f.Add("-s")
	f.Add("abc-s")

	f.Fuzz(func(t *testing.T, id string) {
		prefix := ExtractSeriesPrefix(id)
		if prefix != "" && len(prefix) > len(id) {
			t.Errorf("prefix longer than input")
		}
	})
}

func FuzzIndexSubStatus(f *testing.F) {
	f.Add("media-1", "en", "standard", "opensubtitles", "srt", false)
	f.Add("media-1", "en", "forced", "embedded", "hdmv_pgs", true)
	f.Add("", "", "", "", "", false)

	f.Fuzz(func(t *testing.T, mediaID, lang, variant, source, codec string, ignoreCodec bool) {
		rows := []api.SubtitleFileRow{{
			MediaID:  mediaID,
			Language: lang,
			Variant:  variant,
			Source:   source,
			Codec:    codec,
		}}
		ignored := map[string]bool{}
		if ignoreCodec && codec != "" {
			ignored[codec] = true
		}
		result := IndexSubStatus(rows, ignored)
		if mediaID != "" {
			if _, ok := result[mediaID]; !ok {
				t.Error("expected media ID in result")
			}
		}
	})
}

func FuzzDeduplicateFileRows(f *testing.F) {
	f.Add("m1", "en", "standard", "ext", "m1", "en", "standard", "ext")
	f.Add("m1", "en", "standard", "ext", "m2", "fr", "forced", "embedded")

	f.Fuzz(func(t *testing.T, id1, lang1, var1, src1, id2, lang2, var2, src2 string) {
		rows := []api.SubtitleFileRow{
			{MediaID: id1, Language: lang1, Variant: var1, Source: src1},
			{MediaID: id2, Language: lang2, Variant: var2, Source: src2},
			{MediaID: id1, Language: lang1, Variant: var1, Source: src1},
		}
		result := DeduplicateFileRows(rows)
		if len(result) > len(rows) {
			t.Error("dedup produced more rows than input")
		}
		if len(result) < 1 {
			t.Error("dedup produced empty result from non-empty input")
		}
	})
}
