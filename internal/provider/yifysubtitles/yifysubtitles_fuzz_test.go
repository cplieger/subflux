package yifysubtitles

import (
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func FuzzParseRow(f *testing.F) {
	f.Add(`<td>5</td><td>English</td><td>subtitle <a href="/sub/test">Release.Name</a></td><td><span class="hi-subtitle"></span></td><td>x</td>`)
	f.Add(`<td></td><td></td><td></td><td></td><td></td>`)
	f.Add(``)
	f.Add(`<td>abc</td><td>Fran\xc3\xa7ais</td><td><a href="/dl">名前</a></td><td></td><td></td>`)
	f.Add(`<td><b><i>99</i></b></td><td><span>Spanish</span></td><td>subtitle <a href="/x">rel</a></td><td></td><td></td>`)

	p := &Provider{}
	languages := []string{"en", "fr", "es", "pt", "de", "it"}

	f.Fuzz(func(t *testing.T, rowHTML string) {
		sub, ok := p.parseRow(rowHTML, languages)
		if !ok {
			return
		}
		// A parsed result must be internally consistent and safe to act on:
		// the language is one that was requested, and the download URL stays
		// on the trusted host (it is fetched verbatim during Download).
		if sub.Provider != providerName {
			t.Errorf("parseRow Provider = %q, want %q", sub.Provider, providerName)
		}
		if !slices.Contains(languages, sub.Language) {
			t.Errorf("parseRow Language = %q, not in requested set %v", sub.Language, languages)
		}
		if !strings.HasPrefix(sub.DownloadURL, serverURL+"/") {
			t.Errorf("parseRow DownloadURL = %q, want prefix %q", sub.DownloadURL, serverURL+"/")
		}
		if sub.ID != sub.DownloadURL {
			t.Errorf("parseRow ID = %q, want it to equal DownloadURL %q", sub.ID, sub.DownloadURL)
		}
		if sub.MatchedBy != api.MatchByIMDB {
			t.Errorf("parseRow MatchedBy = %q, want %q", sub.MatchedBy, api.MatchByIMDB)
		}
	})
}
