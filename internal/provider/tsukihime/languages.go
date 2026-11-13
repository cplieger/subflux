package tsukihime

import (
	"slices"
	"sync"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider/animemap"
)

// labels holds every sublangs and track label TsukiHime answered on
// 2026-11-12 (testdata/README.md), many with a region the pickable tags
// lack: listed, a target's query reaches "fr-FR" and "ar-SA" tracks.
var labels = []string{
	"ace-Latn", "ar", "ar-001", "ar-SA", "bn", "cs", "da", "de", "de-DE", "el", "en", "en-JP", "en-US", "enm",
	"es", "es-419", "es-ES", "fa", "fi", "fil", "fil-PH", "fr", "fr-CA", "fr-FR", "he", "hi", "hi-IN", "hi-Latn",
	"hr", "hu", "id", "id-ID", "it", "it-IT", "ja", "ja-JP", "ko", "ko-KR", "ms", "ms-MY", "nb", "nl", "no", "no-NO",
	"pl", "pl-PL", "pt", "pt-BR", "pt-PT", "ro", "ru", "ru-RU", "si", "sk", "sl", "sv", "ta", "te", "th", "th-TH",
	"tr", "uk", "vi", "vi-VN", "zh", "zh-CN", "zh-HK", "zh-Hans", "zh-Hant",
}

// languages lists every pickable tag and every recorded label's tag.
var languages = sync.OnceValue(func() language.ProviderMap { return animemap.Languages(labels...) })

// capOrder is the torrents a landing reads, in the order it spends the
// request cap: those whose sublangs label a tag in langs first, then the
// rest, absent or empty sublangs among them, each group in the answer's
// order. A torrent with no file is never read.
func capOrder(rows []row, langs []language.Tag) []row {
	var first, rest []row
	for _, r := range rows {
		switch {
		case r.Filecount <= 0:
		case labelsAny(r.Sublangs, langs):
			first = append(first, r)
		default:
			rest = append(rest, r)
		}
	}
	return append(first, rest...)
}

// labelsAny reports whether a label of sublangs reads, through
// language.Parse, as one of langs.
func labelsAny(sublangs []string, langs []language.Tag) bool {
	return slices.ContainsFunc(sublangs, func(s string) bool {
		t, ok := language.Parse(s)
		return ok && slices.Contains(langs, t)
	})
}
