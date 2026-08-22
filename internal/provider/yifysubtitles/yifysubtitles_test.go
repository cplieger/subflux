package yifysubtitles

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

// --- Factory ---

func TestFactory(t *testing.T) {
	t.Parallel()

	p, err := Factory(t.Context(), nil)
	if err != nil {
		t.Fatalf("Factory(t.Context(), nil) unexpected error: %v", err)
	}
	if p.Name() != subflux.ProviderNameYifySubtitles {
		t.Errorf("Name() = %q, want %q", p.Name(), subflux.ProviderNameYifySubtitles)
	}
}

// --- Language Mapping ---

func TestYifyLangToISO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"English", "English", "en"},
		{"French", "French", "fr"},
		{"Spanish", "Spanish", "es"},
		{"German", "German", "de"},
		{"Italian", "Italian", "it"},
		{"Portuguese", "Portuguese", "pt"},
		{"Brazilian Portuguese", "Brazilian Portuguese", "pb"},
		{"Dutch", "Dutch", "nl"},
		{"Russian", "Russian", "ru"},
		{"Arabic", "Arabic", "ar"},
		{"Japanese", "Japanese", "ja"},
		{"Chinese", "Chinese", "zh"},
		{"Korean", "Korean", "ko"},
		{"Swedish", "Swedish", "sv"},
		{"Norwegian", "Norwegian", "no"},
		{"Danish", "Danish", "da"},
		{"Finnish", "Finnish", "fi"},
		{"Polish", "Polish", "pl"},
		{"Czech", "Czech", "cs"},
		{"Hungarian", "Hungarian", "hu"},
		{"Romanian", "Romanian", "ro"},
		{"Turkish", "Turkish", "tr"},
		{"Greek", "Greek", "el"},
		{"Hebrew", "Hebrew", "he"},
		{"Thai", "Thai", "th"},
		{"Vietnamese", "Vietnamese", "vi"},
		{"Indonesian", "Indonesian", "id"},
		{"Bulgarian", "Bulgarian", "bg"},
		{"Croatian", "Croatian", "hr"},
		{"Serbian", "Serbian", "sr"},
		{"Slovenian", "Slovenian", "sl"},
		{"unknown language", "Klingon", ""},
		{"empty string", "", ""},
		{"case sensitive", "english", ""},
		{"whitespace not trimmed", " English ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := yifyLangToISO(tt.input)
			if got != tt.want {
				t.Errorf("yifyLangToISO(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- IMDB ID Validation ---

func TestIsValidImdbID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"standard ID", "tt1234567", true},
		{"short ID", "tt1", true},
		{"long ID", "tt1234567890", true},
		{"missing prefix", "1234567", false},
		{"empty string", "", false},
		{"only prefix", "tt", false},
		{"letters after tt", "ttabcdef", false},
		{"path traversal attempt", "tt1234/../etc/passwd", false},
		{"URL encoded", "tt1234%2F", false},
		{"too many digits", "tt12345678901", false},
		{"uppercase prefix", "TT1234567", false},
		{"whitespace padding", " tt1234567 ", false},
		{"newline injection", "tt1234567\n", false},
		{"mixed prefix", "Tt1234567", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isValidImdbID(tt.input)
			if got != tt.want {
				t.Errorf("isValidImdbID(%q) = %v, want %v",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- Download Link Extraction ---

func TestExtractDownloadLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		html string
		want string
	}{
		{
			"valid download link",
			`<a class="download-subtitle" href="/subtitle/12345.zip">Download</a>`,
			"/subtitle/12345.zip",
		},
		{
			"multi-class download link",
			`<a class="btn-icon download-subtitle" href="/subtitle/99.zip"><span>DL</span></a>`,
			"/subtitle/99.zip",
		},
		{
			"extra attributes between class and href",
			`<a class="download-subtitle" data-id="1" href="/subtitle/99.zip">DL</a>`,
			"/subtitle/99.zip",
		},
		{
			"link with query params",
			`<a class="download-subtitle" href="/subtitle/12345.zip?v=2">Download</a>`,
			"/subtitle/12345.zip?v=2",
		},
		{
			"class present but no href",
			`<a class="download-subtitle">Download</a>`,
			"",
		},
		{
			"no download link",
			`<a href="/other">Other</a>`,
			"",
		},
		{"empty html", "", ""},
		{
			"multiple links returns first",
			`<a class="download-subtitle" href="/first.zip">A</a>` +
				`<a class="download-subtitle" href="/second.zip">B</a>`,
			"/first.zip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractDownloadLink(tt.html)
			if got != tt.want {
				t.Errorf("extractDownloadLink(%q) = %q, want %q",
					tt.html, got, tt.want)
			}
		})
	}
}

// --- HTML Parsing ---

func TestParseResults(t *testing.T) {
	t.Parallel()

	t.Run("empty html returns nil", func(t *testing.T) {
		t.Parallel()
		got := parseResults("", []string{"en"})
		if got != nil {
			t.Errorf("parseResults(\"\") = %v, want nil", got)
		}
	})

	t.Run("nil languages returns no results", func(t *testing.T) {
		t.Parallel()
		html := makeRow("5", "English", `/subtitle/tt1`, "release", false)
		got := parseResults(html, nil)
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("empty languages slice returns no results", func(t *testing.T) {
		t.Parallel()
		html := makeRow("5", "English", `/subtitle/tt1`, "release", false)
		got := parseResults(html, []string{})
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("html without table rows returns no results", func(t *testing.T) {
		t.Parallel()
		got := parseResults("<div>no rows here</div>", []string{"en"})
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("row with fewer than 5 tds skipped", func(t *testing.T) {
		t.Parallel()
		html := `<tr><td>1</td><td>English</td><td>release</td></tr>`
		got := parseResults(html, []string{"en"})
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("unknown language skipped", func(t *testing.T) {
		t.Parallel()
		html := makeRow("5", "Klingon", `/sub/1`, "release-name", false)
		got := parseResults(html, []string{"en"})
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("language not in requested list skipped", func(t *testing.T) {
		t.Parallel()
		html := makeRow("5", "French", `/sub/1`, "release-name", false)
		got := parseResults(html, []string{"en"})
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("valid row parsed correctly", func(t *testing.T) {
		t.Parallel()
		html := makeRow("8", "English", `/subtitle/tt123`, "Test.Release", false)
		got := parseResults(html, []string{"en"})
		if len(got) != 1 {
			t.Fatalf("parseResults() = %d results, want 1", len(got))
		}
		sub := got[0]
		if sub.Provider != "yifysubtitles" {
			t.Errorf("Provider = %q, want %q", sub.Provider, "yifysubtitles")
		}
		wantURL := "https://yifysubtitles.ch/subtitle/tt123"
		if sub.ID != wantURL {
			t.Errorf("ID = %q, want %q", sub.ID, wantURL)
		}
		if sub.Language != "en" {
			t.Errorf("Language = %q, want %q", sub.Language, "en")
		}
		if sub.ReleaseName != "Test.Release" {
			t.Errorf("ReleaseName = %q, want %q", sub.ReleaseName, "Test.Release")
		}
		if sub.DownloadURL != wantURL {
			t.Errorf("DownloadURL = %q, want %q", sub.DownloadURL, wantURL)
		}
		if sub.MatchedBy != "imdb" {
			t.Errorf("MatchedBy = %q, want %q", sub.MatchedBy, "imdb")
		}
		if sub.HearingImp {
			t.Error("HearingImp = true, want false")
		}
		if sub.Forced {
			t.Error("Forced = true, want false")
		}
	})

	t.Run("hearing impaired detected", func(t *testing.T) {
		t.Parallel()
		html := makeRow("3", "English", `/subtitle/tt456`, "release", true)
		got := parseResults(html, []string{"en"})
		if len(got) != 1 {
			t.Fatalf("parseResults() = %d results, want 1", len(got))
		}
		if !got[0].HearingImp {
			t.Error("HearingImp = false, want true")
		}
	})

	t.Run("non-numeric rating cell tolerated", func(t *testing.T) {
		t.Parallel()
		// The rating column is ignored (the scorer never consumed provider
		// scores); a non-numeric cell must not break row parsing.
		html := makeRow("bad", "English", `/subtitle/tt789`, "release", false)
		got := parseResults(html, []string{"en"})
		if len(got) != 1 {
			t.Fatalf("parseResults() = %d results, want 1", len(got))
		}
	})

	t.Run("html tags stripped from language", func(t *testing.T) {
		t.Parallel()
		// Language wrapped in a flag span.
		html := `<tr>` +
			`<td><span class="rating">7</span></td>` +
			`<td><span class="flag-en"></span>English</td>` +
			`<td><a href="/subtitle/tt500">subtitle Tagged.Release</a></td>` +
			`<td></td>` +
			`<td>uploader</td>` +
			`</tr>`
		got := parseResults(html, []string{"en"})
		if len(got) != 1 {
			t.Fatalf("parseResults() = %d results, want 1", len(got))
		}
		if got[0].ReleaseName != "Tagged.Release" {
			t.Errorf("ReleaseName = %q, want %q", got[0].ReleaseName, "Tagged.Release")
		}
	})

	t.Run("no href in release td skipped", func(t *testing.T) {
		t.Parallel()
		// Row with 5 tds but no <a> tag in the release column.
		html := `<tr>` +
			`<td>5</td>` +
			`<td>English</td>` +
			`<td>subtitle plain-text</td>` +
			`<td></td>` +
			`<td>uploader</td>` +
			`</tr>`
		got := parseResults(html, []string{"en"})
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("empty href skipped", func(t *testing.T) {
		t.Parallel()
		html := makeRow("5", "English", ``, "release", false)
		got := parseResults(html, []string{"en"})
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("href without leading slash skipped", func(t *testing.T) {
		t.Parallel()
		html := makeRow("5", "English", `relative/path`, "release", false)
		got := parseResults(html, []string{"en"})
		if len(got) != 0 {
			t.Errorf("parseResults() = %d results, want 0", len(got))
		}
	})

	t.Run("multiple languages matched", func(t *testing.T) {
		t.Parallel()
		html := makeRow("5", "English", `/subtitle/tt1`, "rel-en", false) +
			makeRow("7", "French", `/subtitle/tt2`, "rel-fr", false)
		got := parseResults(html, []string{"en", "fr"})
		if len(got) != 2 {
			t.Fatalf("parseResults() = %d results, want 2", len(got))
		}
		if got[0].Language != "en" {
			t.Errorf("got[0].Language = %q, want %q", got[0].Language, "en")
		}
		if got[1].Language != "fr" {
			t.Errorf("got[1].Language = %q, want %q", got[1].Language, "fr")
		}
	})

	t.Run("Brazilian Portuguese maps to pb", func(t *testing.T) {
		t.Parallel()
		html := makeRow("5", "Brazilian Portuguese", `/subtitle/tt900`, "release", false)
		got := parseResults(html, []string{"pb"})
		if len(got) != 1 {
			t.Fatalf("parseResults() = %d results, want 1", len(got))
		}
		if got[0].Language != "pb" {
			t.Errorf("Language = %q, want %q", got[0].Language, "pb")
		}
	})
}

// makeRow builds a minimal YIFY HTML table row for testing parseResults.
// The release td includes an href link and the "subtitle " prefix that
// parseResults strips. The hi parameter controls the hearing-impaired marker.
func makeRow(rating, lang, href, release string, hi bool) string {
	hiClass := ""
	if hi {
		hiClass = `<span class="hi-subtitle"></span>`
	}
	return `<tr>` +
		`<td>` + rating + `</td>` +
		`<td>` + lang + `</td>` +
		`<td><a href="` + href + `">subtitle ` + release + `</a></td>` +
		`<td>` + hiClass + `</td>` +
		`<td>uploader</td>` +
		`</tr>`
}

// --- Download ---

// statusRoundTripper answers every request with a fixed status and body,
// standing in for the upstream without a network dial.
type statusRoundTripper struct {
	body   string
	status int
}

func (rt statusRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: rt.status,
		Body:       io.NopCloser(strings.NewReader(rt.body)),
		Header:     make(http.Header),
	}, nil
}

// TestFetchPage_bodyAndMissingPage asserts a served page comes back verbatim
// while a 404 is reported as an empty page with no error — the two callers read
// that differently, so the distinction is the whole contract.
func TestFetchPage_bodyAndMissingPage(t *testing.T) {
	t.Parallel()

	t.Run("served page returned verbatim", func(t *testing.T) {
		t.Parallel()
		const page = "<html><body>yify results</body></html>"
		p := &Provider{client: &http.Client{Transport: statusRoundTripper{status: http.StatusOK, body: page}}}

		got, err := p.fetchPage(t.Context(), serverURL+"/search/en-movie")
		if err != nil {
			t.Fatalf("fetchPage(200) unexpected error: %v", err)
		}
		if got != page {
			t.Errorf("fetchPage(200) = %q, want %q", got, page)
		}
	})

	t.Run("missing page is empty with no error", func(t *testing.T) {
		t.Parallel()
		p := &Provider{client: &http.Client{Transport: statusRoundTripper{status: http.StatusNotFound, body: "nope"}}}

		got, err := p.fetchPage(t.Context(), serverURL+"/search/en-movie")
		if err != nil {
			t.Fatalf("fetchPage(404) unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("fetchPage(404) = %q, want an empty page", got)
		}
	})
}

// A 404 on the subtitle detail page means the upstream no longer holds the
// file the search result named. fetchPage reports that as an empty body
// because the search path reads it as "no results"; the download path must
// turn it into a named absence instead of a zero-byte success.
func TestDownloadAbsentUpstream(t *testing.T) {
	t.Parallel()

	p := &Provider{client: &http.Client{Transport: statusRoundTripper{status: http.StatusNotFound}}}
	sub := subflux.Subtitle{ID: "sub-1", DownloadURL: serverURL + "/subtitles/sub-1"}

	data, err := p.Download(t.Context(), &sub)
	if !errors.Is(err, subflux.ErrSubtitleAbsent) {
		t.Errorf("Download on 404 error = %v, want one wrapping subflux.ErrSubtitleAbsent", err)
	}
	if data != nil {
		t.Errorf("Download on 404 data = %q, want nil", data)
	}
}
