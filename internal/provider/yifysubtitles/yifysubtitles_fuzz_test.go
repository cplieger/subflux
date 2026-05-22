package yifysubtitles

import "testing"

func FuzzParseRow(f *testing.F) {
	f.Add(`<td>5</td><td>English</td><td>subtitle <a href="/sub/test">Release.Name</a></td><td><span class="hi-subtitle"></span></td><td>x</td>`)
	f.Add(`<td></td><td></td><td></td><td></td><td></td>`)
	f.Add(``)
	f.Add(`<td>abc</td><td>Fran\xc3\xa7ais</td><td><a href="/dl">名前</a></td><td></td><td></td>`)
	f.Add(`<td><b><i>99</i></b></td><td><span>Spanish</span></td><td>subtitle <a href="/x">rel</a></td><td></td><td></td>`)

	p := &Provider{}
	languages := []string{"en", "fr", "es", "pt", "de", "it"}

	f.Fuzz(func(t *testing.T, rowHTML string) {
		// Must not panic on any input.
		p.parseRow(rowHTML, languages)
	})
}
