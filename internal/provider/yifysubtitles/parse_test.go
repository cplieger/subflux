package yifysubtitles

import (
	"testing"

	"github.com/cplieger/subflux/internal/language"
)

func tagOf(t testing.TB, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

func row(lang, href, release string, hi bool) string {
	hiCell := ""
	if hi {
		hiCell = `<span class="hi-subtitle"></span>`
	}
	return `<tr><td>5</td><td><span class="sub-lang">` + lang + `</span></td>` +
		`<td><a href="` + href + `"><span class="text-muted">subtitle</span> ` + release + `</a></td>` +
		`<td>` + hiCell + `</td><td>uploader</td></tr>`
}

func TestParseResults(t *testing.T) {
	en := []language.Tag{tagOf(t, "en")}
	cases := []struct {
		name, page string
		langs      []language.Tag
		ref        string
	}{
		{"a valid row", row("English", "/subtitles/x-1", "Test.Release", false), en, "/subtitles/x-1"},
		{"no languages asked", row("English", "/subtitles/x-1", "r", false), nil, ""},
		{"a language not asked", row("French", "/subtitles/x-1", "r", false), en, ""},
		{"an unknown language", row("Klingon", "/subtitles/x-1", "r", false), en, ""},
		{"fewer than five cells", `<tr><td>1</td><td>English</td><td><a href="/s">r</a></td></tr>`, en, ""},
		{"an absolute link", row("English", "https://elsewhere.example/x", "r", false), en, ""},
		{"a protocol-relative link", row("English", "//elsewhere.example/x", "r", false), en, ""},
		{"no rows", "<div>none</div>", en, ""},
	}
	for _, tc := range cases {
		got := parseResults(tc.page, tc.langs)
		if (tc.ref == "") != (len(got) == 0) || (tc.ref != "" && got[0].Ref.Token() != tc.ref) {
			t.Errorf("%s: %+v, want ref %q", tc.name, got, tc.ref)
		}
	}
	got := parseResults(row("Brazillian Portuguese", "/subtitles/b", "A.720p<br />\nB.1080p", true), []language.Tag{tagOf(t, "pt-BR")})
	if len(got) != 1 || got[0].Release != "A.720p B.1080p" || !got[0].Flags.HI || got[0].Lang != tagOf(t, "pt-BR") {
		t.Errorf("the misspelt Brazilian row: %+v", got)
	}
}

func TestDownloadLink(t *testing.T) {
	for page, want := range map[string]string{
		`<a class="download-subtitle" href="/subtitle/1.zip">`:                                   "/subtitle/1.zip",
		`<a class="btn-icon download-subtitle" href="/subtitle/99.zip">`:                         "/subtitle/99.zip",
		`<a class="download-subtitle" data-id="1" href="/subtitle/9.zip">`:                       "/subtitle/9.zip",
		`<a class="download-subtitle" href="/a.zip"><a class="download-subtitle" href="/b.zip">`: "/a.zip",
		`<a class="download-subtitle">`:                                                          "",
		`<a class="download-subtitle" href="https://elsewhere.example/x">`:                       "",
		`<a class="download-subtitle" href="//elsewhere.example/x">`:                             "",
		`<a href="/other">`: "",
		"":                  "",
	} {
		if got := downloadLink(page); got != want {
			t.Errorf("downloadLink(%q) = %q, want %q", page, got, want)
		}
	}
}

func FuzzParseResults(f *testing.F) {
	f.Add(row("English", "/subtitles/x", "r", true))
	f.Add("<tr><td></td></tr>")
	f.Fuzz(func(t *testing.T, page string) {
		for _, c := range parseResults(page, languages.Tags()) {
			tok := c.Ref.Token()
			if len(tok) < 2 || tok[0] != '/' || tok[1] == '/' {
				t.Fatalf("kept a link off the site: %q", tok)
			}
		}
	})
}
