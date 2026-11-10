// Package subsource is the SubSource provider: its REST API at
// api.subsource.net, which finds a title by IMDb id (falling back to its
// text search) and lists that title's subtitles per language. It needs an
// API key; its limits are 60 calls a minute, 1,800 an hour and 7,200 a day.
package subsource

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/go/jsonx"
	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// ID is SubSource's provider id.
const ID provider.ProviderID = "subsource"

const (
	apiHost = "api.subsource.net"
	apiURL  = "https://" + apiHost + "/api/v1"

	keyAPIKey = "api_key"

	// paramSearchType names a search's kind; byIMDb is the kind, and its
	// parameter, of an IMDb id search.
	paramSearchType = "searchType"
	byIMDb          = "imdb"

	// unmatchedIMDb is an id no title has, so the key test touches nothing.
	unmatchedIMDb = "tt0000000"

	maxSearchBody   = 1 << 20
	maxListBody     = 2 << 20
	maxDownloadBody = 10 << 20
)

// languages are SubSource's language labels as its API reads and answers
// them (testdata/languages-probe.json): lower case, words joined by "_", a
// variety in parentheses appended ("chinese_traditional"), its own spellings
// kept. A label filters exactly, so "french" never answers "french_france".
var languages = language.MustProviderMap(provider.WirePairs(
	"abkhazian", "ab", "afrikaans", "af", "albanian", "sq", "amharic", "am",
	"arabic", "ar", "aragonese", "an", "armenian", "hy", "assamese", "as",
	"azerbaijani", "az", "basque", "eu", "belarusian", "be", "bengali", "bn",
	"bosnian", "bs", "brazilian_portuguese", "pt-BR", "breton", "br",
	"bulgarian", "bg", "burmese", "my", "catalan", "ca",
	"chinese_bg_code", "zh", "chinese_traditional", "zh-Hant", "big_5_code", "zh-Hant",
	"chinese_cantonese", "yue",
	"croatian", "hr", "czech", "cs", "danish", "da", "dutch", "nl",
	"english", "en", "espranto", "eo", "estonian", "et", "farsi_persian", "fa",
	"finnish", "fi", "french", "fr", "french_france", "fr-FR", "french_canada", "fr-CA",
	"gaelic", "gd", "georgian", "ka", "german", "de", "greek", "el",
	"hebrew", "he", "hindi", "hi", "hungarian", "hu", "icelandic", "is",
	"igbo", "ig", "indonesian", "id", "interlingua", "ia", "irish", "ga",
	"italian", "it", "japanese", "ja", "kannada", "kn", "kazakh", "kk",
	"khmer", "km", "korean", "ko", "kurdish", "ku", "latvian", "lv",
	"lithuanian", "lt", "luxembourgish", "lb", "macedonian", "mk", "malay", "ms",
	"malayalam", "ml", "marathi", "mr", "mongolian", "mn", "navajo", "nv",
	"nepali", "ne", "northen_sami", "se", "norwegian", "no", "occitan", "oc", //nolint:misspell // SubSource's own label
	"polish", "pl", "portuguese", "pt", "pushto", "ps", "romanian", "ro",
	"russian", "ru", "serbian", "sr", "sindhi", "sd", "sinhala", "si",
	"slovak", "sk", "slovenian", "sl", "somali", "so", "spanish", "es",
	"spanish_latin_america", "es-419", "spanish_spain", "es-ES", "swahili", "sw",
	"swedish", "sv", "tagalog", "tl", "tamil", "ta", "tatar", "tt",
	"telugu", "te", "thai", "th", "turkish", "tr", "turkmen", "tk",
	"ukrainian", "uk", "urdu", "ur", "uzbek", "uz", "vietnamese", "vi",
	"welsh", "cy",
))

// NewEntry is SubSource's row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:      ID,
		Summary: "Movie and TV subtitles from SubSource by IMDb id; needs an API key.",
		Hosts:   []string{apiHost},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyAPIKey, Label: "API key", Help: "From subsource.net API registration", Kind: formschema.Secret, Required: true},
		}},
		Languages: languages,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, key: v.Secret(keyAPIKey), titles: cache.New(func(id int) bool { return id > 0 })}, nil
		},
	}
}

type adapter struct {
	titles *cache.Cache[int]
	c      provider.Client
	key    string
}

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	if q.External.IMDb == 0 {
		return nil, &provider.Skipped{Cause: "no IMDb id"}
	}
	movieID, err := a.title(ctx, &q)
	if err != nil || movieID == 0 {
		return nil, err
	}
	var out []provider.Candidate
	for _, tag := range q.Langs {
		seen := map[int]bool{}
		for _, wire := range languages.Wires(tag) {
			items, err := a.subtitles(ctx, movieID, wire, &q)
			if err != nil {
				return out, err
			}
			items = slices.DeleteFunc(items, func(it item) bool {
				dup := seen[it.SubtitleID]
				seen[it.SubtitleID] = true
				return dup
			})
			out = append(out, candidates(items, tag)...)
		}
	}
	return out, nil
}

// Test searches an IMDb id no title has; an answer of any kind but a
// refusal proves the key.
func (a *adapter) Test(ctx context.Context) error {
	_, err := a.search(ctx, url.Values{paramSearchType: {byIMDb}, byIMDb: {unmatchedIMDb}})
	if f, ok := errors.AsType[*provider.Failure](err); ok && f.Kind == provider.NotFound {
		return nil
	}
	return err
}

func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) (provider.Payload, error) {
	id, err := strconv.ParseUint(ref.Token(), 10, 63)
	if err != nil {
		return provider.Payload{}, provider.Fail(provider.Permanent, "the subtitle id is not a number")
	}
	resp, err := a.get(ctx, fmt.Sprintf("/subtitles/%d/download", id), url.Values{})
	if err != nil {
		return provider.Payload{}, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, maxDownloadBody)
	if err != nil {
		return provider.Payload{}, err
	}
	if len(b) == 0 {
		return provider.Payload{}, provider.Fail(provider.Permanent, "SubSource answered an empty download")
	}
	return provider.Payload{Name: provider.PayloadName(resp), Bytes: b}, nil
}

// get GETs path under the API with params and the key.
func (a *adapter) get(ctx context.Context, path string, params url.Values) (*http.Response, error) {
	params.Set(keyAPIKey, a.key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+path+"?"+params.Encode(), http.NoBody)
	if err != nil {
		return nil, err
	}
	return a.c.Do(req)
}

func (a *adapter) getJSON(ctx context.Context, path string, params url.Values, limit int64, v any) error {
	resp, err := a.get(ctx, path, params)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, "decode SubSource answer: "+err.Error())
	}
	return nil
}

type titleHit struct {
	Title       string  `json:"title"`
	ReleaseYear flexInt `json:"releaseYear"`
	MovieID     int     `json:"movieId"`
}

func (a *adapter) search(ctx context.Context, params url.Values) ([]titleHit, error) {
	var r struct {
		Success *bool      `json:"success"`
		Error   string     `json:"error"`
		Data    []titleHit `json:"data"`
	}
	if err := a.getJSON(ctx, "/movies/search", params, maxSearchBody, &r); err != nil {
		return nil, err
	}
	if r.Success != nil && !*r.Success {
		return nil, refusal(r.Error)
	}
	return r.Data, nil
}

// title is SubSource's id for q's media (per season for an episode),
// cached by IMDb id and season: the IMDb search's first hit, taken without a
// title match, else the text search's first hit matching one of q's titles.
func (a *adapter) title(ctx context.Context, q *provider.Query) (int, error) {
	imdb := fmt.Sprintf("tt%07d", q.External.IMDb)
	season := 0
	if q.Kind == media.Episode {
		season = q.Numbering.Season
	}
	return a.titles.GetOrFetch(ctx, titleKey(imdb, season), func(ctx context.Context) (int, error) {
		params := url.Values{paramSearchType: {byIMDb}, byIMDb: {imdb}}
		if season > 0 {
			params.Set("season", strconv.Itoa(season))
		}
		hits, err := a.search(ctx, params)
		if err != nil {
			return 0, err
		}
		for _, h := range hits {
			if h.MovieID > 0 {
				return h.MovieID, nil
			}
		}
		return a.textTitle(ctx, q.Titles, season, q.Year)
	})
}

// titleKey is the title cache's key: SubSource answers per-season title ids.
func titleKey(imdb string, season int) string {
	return keyenc.Join("title", imdb, strconv.Itoa(season))
}

// textTitle tries each title's text search in turn; a refused key or a rate
// limit ends the turn, and another failure is returned when no title matched.
func (a *adapter) textTitle(ctx context.Context, titles []string, season, year int) (int, error) {
	var firstErr error
	for _, title := range titles {
		if title == "" {
			continue
		}
		params := url.Values{paramSearchType: {"text"}, "q": {strings.ToLower(title)}}
		if season > 0 {
			params.Set("season", strconv.Itoa(season))
		}
		hits, err := a.search(ctx, params)
		switch f, _ := errors.AsType[*provider.Failure](err); {
		case f != nil && (f.Kind == provider.Auth || f.Kind == provider.RateLimited):
			return 0, err
		case err != nil:
			firstErr = cmp.Or(firstErr, err)
		default:
			if id := matchTitle(hits, title, year); id > 0 {
				return id, nil
			}
		}
	}
	return 0, firstErr
}

// matchTitle is the first hit whose title contains title, in that year when
// year is known; 0 for none.
func matchTitle(hits []titleHit, title string, year int) int {
	lower := strings.ToLower(title)
	for _, h := range hits {
		if strings.Contains(strings.ToLower(h.Title), lower) && (year == 0 || int(h.ReleaseYear) == year) {
			return h.MovieID
		}
	}
	return 0
}

type item struct {
	Commentary      string   `json:"commentary"`
	ReleaseInfo     []string `json:"releaseInfo"`
	SubtitleID      int      `json:"subtitleId"`
	HearingImpaired bool     `json:"hearingImpaired"`
	ForeignParts    bool     `json:"foreignParts"`
}

func (a *adapter) subtitles(ctx context.Context, movieID int, wire string, q *provider.Query) ([]item, error) {
	params := url.Values{"language": {wire}, "limit": {"100"}, "movieId": {strconv.Itoa(movieID)}}
	if q.Kind == media.Episode {
		params.Set("seasonNumber", strconv.Itoa(q.Numbering.Season))
		params.Set("episodeNumber", strconv.Itoa(q.Numbering.Episode))
	}
	var r struct {
		Error   string `json:"error"`
		Data    []item `json:"data"`
		Success bool   `json:"success"`
	}
	if err := a.getJSON(ctx, "/subtitles", params, maxListBody, &r); err != nil {
		return nil, err
	}
	if !r.Success {
		return nil, refusal(r.Error)
	}
	return r.Data, nil
}

// The success:false texts that refuse the key, and those that mean no
// results, matched case-insensitively.
var (
	keyRefusals = []string{"api key", "api_key", "unauthorized", "not authorized"}
	noResults   = []string{"not found", "no subtitles", "no results"}
)

// refusal is a 200 answer's success:false error: Auth for a refused key, nil
// for no results, else Permanent.
func refusal(msg string) error {
	lower := strings.ToLower(msg)
	has := func(ps []string) bool {
		for _, p := range ps {
			if strings.Contains(lower, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has(keyRefusals):
		return provider.Fail(provider.Auth, "SubSource refused the API key: "+msg)
	case has(noResults):
		return nil
	}
	return provider.Fail(provider.Permanent, "SubSource: "+msg)
}

// candidates are one per release name of each file.
func candidates(items []item, tag language.Tag) []provider.Candidate {
	var out []provider.Candidate
	for _, it := range items {
		if it.SubtitleID <= 0 {
			continue
		}
		releases := it.ReleaseInfo
		if len(releases) == 0 {
			releases = []string{""}
		}
		for _, rel := range releases {
			out = append(out, provider.Candidate{
				Ref:       provider.NewRef(strconv.Itoa(it.SubtitleID)),
				Lang:      tag,
				Release:   rel,
				Flags:     provider.Flags(language.Flags{HI: it.HearingImpaired, Forced: it.ForeignParts}, it.Commentary, rel),
				MatchedBy: rank.ID,
			})
		}
	}
	return out
}

// flexInt reads SubSource's releaseYear, which comes as a number or a
// string; any other shape is 0, an unknown year.
type flexInt int

var yearPolicy = jsonx.Policy{
	MinValue: math.MinInt64, MaxValue: math.MaxInt64,
	Null: jsonx.Zero, EmptyInput: jsonx.Zero, EmptyString: jsonx.Zero, MalformedString: jsonx.Zero,
	NonNumericString: jsonx.Zero, OtherShape: jsonx.Zero, PaddedString: jsonx.Zero,
	FloatForm: jsonx.Zero, Fractional: jsonx.Zero, OutOfRange: jsonx.Zero,
}

func (f *flexInt) UnmarshalJSON(data []byte) error {
	n, err := jsonx.ParseInt64(data, yearPolicy)
	if err != nil {
		n = 0
	}
	*f = flexInt(n)
	return nil
}
