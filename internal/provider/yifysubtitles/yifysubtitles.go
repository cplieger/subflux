// Package yifysubtitles is the YIFY Subtitles provider: movie subtitles read
// from yifysubtitles.ch's pages by IMDb id. It scrapes HTML, as the site
// publishes no API, so its entry is a named Exception (DECISIONS 41).
package yifysubtitles

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// ID is YIFY Subtitles' provider id.
const ID provider.ProviderID = "yifysubtitles"

const (
	siteHost = "yifysubtitles.ch"
	siteURL  = "https://" + siteHost

	// browserUA passes the site's bot check, which refuses other agents;
	// DECISIONS 41 (D-UA) keeps it for this exception only.
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

	maxPageBody     = 2 << 20
	maxDownloadBody = 10 << 20
)

// languages are the site's language names. Its other spelling of Brazilian
// Portuguese is normalized before the lookup.
var languages = language.MustProviderMap(provider.WirePairs(
	"English", "en", "French", "fr", "Spanish", "es", "German", "de",
	"Italian", "it", "Portuguese", "pt", "Brazilian Portuguese", "pt-BR", "Dutch", "nl",
	"Russian", "ru", "Arabic", "ar", "Japanese", "ja", "Korean", "ko",
	"Chinese", "zh", "Chinese BG code", "zh-Hans", "Big 5 code", "zh-Hant",
	"Swedish", "sv", "Norwegian", "no", "Danish", "da", "Finnish", "fi",
	"Polish", "pl", "Czech", "cs", "Hungarian", "hu", "Romanian", "ro",
	"Turkish", "tr", "Greek", "el", "Hebrew", "he", "Thai", "th",
	"Vietnamese", "vi", "Indonesian", "id", "Bulgarian", "bg",
	"Croatian", "hr", "Serbian", "sr", "Slovenian", "sl",
	"Slovak", "sk", "Ukrainian", "uk", "Catalan", "ca",
	"Farsi/Persian", "fa", "Malay", "ms", "Albanian", "sq", "Bosnian", "bs",
	"Bengali", "bn", "Macedonian", "mk", "Hindi", "hi", "Tamil", "ta",
	"Malayalam", "ml", "Urdu", "ur", "Lithuanian", "lt", "Latvian", "lv",
	"Estonian", "et", "Icelandic", "is", "Mongolian", "mn",
))

var labelSpellings = map[string]string{"Brazillian Portuguese": "Brazilian Portuguese"}

// NewEntry is YIFY Subtitles' row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:        ID,
		Summary:   "Movie subtitles read from the YIFY Subtitles site by IMDb id; needs no account.",
		Hosts:     []string{siteHost},
		Languages: languages,
		Exception: true,
		UserAgent: browserUA,
		New: func(c provider.Client, _ formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c}, nil
		},
	}
}

type adapter struct{ c provider.Client }

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	switch {
	case q.Kind != media.Movie:
		return nil, &provider.Skipped{Cause: "YIFY Subtitles has movies only"}
	case q.External.IMDb == 0:
		return nil, &provider.Skipped{Cause: "no IMDb id"}
	}
	page, err := a.page(ctx, fmt.Sprintf("%s/movie-imdb/tt%07d", siteURL, q.External.IMDb), siteURL)
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseResults(page, q.Langs), nil
}

// Fetch reads the subtitle's page for its download link, then GETs the file
// with the page as Referer, as a browser does.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) (provider.Payload, error) {
	pageURL := siteURL + ref.Token()
	page, err := a.page(ctx, pageURL, siteURL)
	if err != nil {
		return provider.Payload{}, err
	}
	link := downloadLink(page)
	if link == "" {
		return provider.Payload{}, provider.Fail(provider.Permanent, "the subtitle page has no download link")
	}
	resp, err := a.get(ctx, siteURL+link, pageURL)
	if err != nil {
		return provider.Payload{}, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, maxDownloadBody)
	if err != nil {
		return provider.Payload{}, err
	}
	return provider.Payload{Name: provider.PayloadName(resp), Bytes: b}, nil
}

func (a *adapter) get(ctx context.Context, u, referer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", referer)
	return a.c.Do(req)
}

func (a *adapter) page(ctx context.Context, u, referer string) (string, error) {
	resp, err := a.get(ctx, u, referer)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, maxPageBody)
	return string(b), err
}

func notFound(err error) bool {
	f, ok := errors.AsType[*provider.Failure](err)
	return ok && f.Kind == provider.NotFound
}

var (
	rowRe      = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	tdRe       = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)
	hrefRe     = regexp.MustCompile(`href="(/[^"]+)"`)
	hiRe       = regexp.MustCompile(`class="hi-subtitle"`)
	dlBtnRe    = regexp.MustCompile(`class="[^"]*download-subtitle[^"]*"[^>]*href="(/[^"/][^"]*)"`)
	tagStripRe = regexp.MustCompile(`<[^>]*>`)
	spaceRe    = regexp.MustCompile(`\s+`)
)

// parseResults reads the movie page's table: one candidate per row in an
// asked language, its Ref the subtitle page's path.
func parseResults(page string, langs []language.Tag) []provider.Candidate {
	var out []provider.Candidate
	for _, row := range rowRe.FindAllStringSubmatch(page, -1) {
		tds := tdRe.FindAllStringSubmatch(row[1], -1)
		if len(tds) < 5 {
			continue
		}
		tag, ok := languages.Tag(label(tds[1][1]))
		if !ok || !slices.Contains(langs, tag) {
			continue
		}
		link := hrefRe.FindStringSubmatch(tds[2][1])
		if len(link) < 2 || strings.HasPrefix(link[1], "//") {
			continue
		}
		release := strings.TrimSpace(tagStripRe.ReplaceAllString(tds[2][1], ""))
		release = strings.TrimSpace(strings.TrimPrefix(release, "subtitle"))
		release = spaceRe.ReplaceAllString(release, " ")
		out = append(out, provider.Candidate{
			Ref:       provider.NewRef(link[1]),
			Lang:      tag,
			Release:   release,
			Flags:     language.Flags{HI: provider.HearingImpaired(hiRe.MatchString(tds[3][1]), release)},
			MatchedBy: rank.ID,
		})
	}
	return out
}

func label(cell string) string {
	l := strings.TrimSpace(tagStripRe.ReplaceAllString(cell, ""))
	if s, ok := labelSpellings[l]; ok {
		return s
	}
	return l
}

func downloadLink(page string) string {
	m := dlBtnRe.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return m[1]
}
