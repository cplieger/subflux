// Package subdl is the SubDL provider: its REST API at api.subdl.com, by
// IMDb or TMDB id or by title, with downloads from dl.subdl.com. It needs an
// API key; the key's quota is 2,000 calls a day.
package subdl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// ID is SubDL's provider id.
const ID provider.ProviderID = "subdl"

const (
	apiHost = "api.subdl.com"
	dlHost  = "dl.subdl.com"
	apiURL  = "https://" + apiHost + "/api/v1"

	keyAPIKey = "api_key"

	maxListBody     = 2 << 20
	maxDownloadBody = 10 << 20

	// limitBody is the most a 500 carrying SubDL's download-limit answer
	// holds.
	limitBody = 100
)

// languages are SubDL's upper-case language codes.
var languages = language.MustProviderMap(provider.WirePairs(
	"EN", "en", "FR", "fr", "ES", "es", "DE", "de",
	"IT", "it", "PT", "pt", "BR_PT", "pt-BR", "NL", "nl", "RU", "ru",
	"AR", "ar", "JA", "ja", "ZH", "zh", "KO", "ko",
	"SV", "sv", "NO", "no", "DA", "da", "FI", "fi",
	"PL", "pl", "CS", "cs", "HU", "hu", "RO", "ro",
	"TR", "tr", "EL", "el", "HE", "he", "TH", "th",
	"VI", "vi", "ID", "id", "BG", "bg",
	"HR", "hr", "SR", "sr", "SL", "sl",
	"SK", "sk", "UK", "uk", "FA", "fa",
	"MS", "ms", "HI", "hi", "BN", "bn",
	"TA", "ta", "TE", "te", "UR", "ur",
	"IS", "is", "LT", "lt", "LV", "lv",
	"ET", "et", "SQ", "sq", "BS", "bs",
	"MK", "mk", "CA", "ca",
))

// NewEntry is SubDL's row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:      ID,
		Summary: "Movie and TV subtitles from SubDL by IMDb or TMDB id or title; needs an API key.",
		Hosts:   []string{apiHost, dlHost},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyAPIKey, Label: "API key", Help: "From subdl.com API registration", Kind: formschema.Secret, Required: true},
		}},
		Languages: languages,
		Quirk:     quirk,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, key: v.Secret(keyAPIKey)}, nil
		},
	}
}

// quirk reads a 500 with a body under 100 bytes, SubDL's answer once the
// key's download limit is spent, as no such file.
func quirk(resp *http.Response, body []byte) error {
	if resp.StatusCode == http.StatusInternalServerError && len(body) < limitBody {
		return provider.Fail(provider.NotFound, "SubDL's download limit answer")
	}
	return nil
}

type adapter struct {
	c   provider.Client
	key string
}

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	var wires []string
	for _, t := range q.Langs {
		if w, ok := languages.Wire(t); ok {
			wires = append(wires, w)
		}
	}
	if len(wires) == 0 {
		return nil, nil
	}
	params, by, ok := searchParams(&q, wires)
	if !ok {
		return nil, &provider.Skipped{Cause: "no IMDb id, TMDB id or title"}
	}
	params.Set(keyAPIKey, a.key)
	var r answer
	if err := a.getJSON(ctx, apiURL+"/subtitles?"+params.Encode(), &r); err != nil {
		return nil, err
	}
	if !r.Status {
		return nil, refusal(r.Error)
	}
	return candidates(r.Subtitles, q.Kind == media.Episode, by), nil
}

// Test asks SubDL's account endpoint, which proves the key without touching
// the subtitle index.
func (a *adapter) Test(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/me?"+url.Values{keyAPIKey: {a.key}}.Encode(), http.NoBody)
	if err != nil {
		return err
	}
	resp, err := a.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = provider.ReadAll(resp, maxListBody)
	return err
}

// Fetch GETs the download path on dl.subdl.com with the key, as SubDL's
// answer links it.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) (provider.Payload, error) {
	u := url.URL{Scheme: "https", Host: dlHost, Path: ref.Token(), RawQuery: url.Values{keyAPIKey: {a.key}}.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return provider.Payload{}, err
	}
	resp, err := a.c.Do(req)
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

func (a *adapter) getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := a.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, maxListBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, "decode SubDL answer: "+err.Error())
	}
	return nil
}

// searchParams is the search's query, the key aside: an IMDb id first, then
// a TMDB id (a series' for an episode), then the first title; ok is false
// with none. hi is left out, since hi=1 answers only hearing-impaired files.
func searchParams(q *provider.Query, wires []string) (params url.Values, by rank.MatchedBy, ok bool) {
	params = url.Values{
		"languages":     {strings.Join(wires, ",")},
		"subs_per_page": {"30"},
		"comment":       {"1"},
		"releases":      {"1"},
	}
	title := ""
	if len(q.Titles) > 0 {
		title = q.Titles[0]
	}
	if q.Kind == media.Episode {
		params.Set("type", "tv")
		params.Set("season_number", strconv.Itoa(q.Numbering.Season))
		params.Set("episode_number", strconv.Itoa(q.Numbering.Episode))
	} else {
		params.Set("type", "movie")
	}
	switch {
	case q.External.IMDb != 0:
		params.Set("imdb_id", fmt.Sprintf("tt%07d", q.External.IMDb))
		return params, rank.ID, true
	case q.External.TMDB != 0:
		params.Set("tmdb_id", strconv.FormatUint(q.External.TMDB, 10))
		return params, rank.ID, true
	case title != "":
		params.Set("film_name", title)
		return params, rank.Title, true
	}
	return nil, 0, false
}

// The status:false texts that refuse the key, and those that mean no such
// media, matched case-insensitively.
var (
	authRefusals = []string{"not authorized", "api key", "invalid key"}
	notFound     = []string{"can't find", "cannot find", "not found"}
)

// refusal is a 200 answer's status:false error: Auth for a refused key, nil
// for unknown media, else Permanent.
func refusal(msg string) error {
	lower := strings.ToLower(msg)
	contains := func(ps []string) bool {
		for _, p := range ps {
			if strings.Contains(lower, p) {
				return true
			}
		}
		return false
	}
	switch {
	case contains(authRefusals):
		return provider.Fail(provider.Auth, "SubDL refused the key: "+msg)
	case contains(notFound), msg == "":
		return nil
	}
	return provider.Fail(provider.Permanent, "SubDL: "+msg)
}

type answer struct {
	Error     string `json:"error"`
	Subtitles []item `json:"subtitles"`
	Status    bool   `json:"status"`
}

type item struct {
	Name        string   `json:"name"`
	ReleaseName string   `json:"release_name"`
	Language    string   `json:"language"`
	URL         string   `json:"url"`
	Comment     string   `json:"comment"`
	Releases    []string `json:"releases"`
	EpisodeFrom int      `json:"episode_from"`
	EpisodeEnd  int      `json:"episode_end"`
	HI          bool     `json:"hi"`
	FullSeason  bool     `json:"full_season"`
}

// candidates are one per release name (else the file's release_name) of
// each answered file in a mapped language with a download path on
// dl.subdl.com, less packs for an episode: a pack answers full_season, or a
// range of episodes.
func candidates(items []item, episode bool, by rank.MatchedBy) []provider.Candidate {
	var out []provider.Candidate
	for i := range items {
		it := &items[i]
		if episode && (it.FullSeason || it.EpisodeFrom != it.EpisodeEnd) {
			continue
		}
		tag, ok := languages.Tag(strings.ToUpper(it.Language))
		if !ok {
			continue
		}
		path, ok := downloadPath(it.URL)
		if !ok {
			continue
		}
		releases := it.Releases
		if len(releases) == 0 {
			releases = []string{it.ReleaseName}
		}
		for _, release := range releases {
			out = append(out, provider.Candidate{
				Ref:       provider.NewRef(path),
				Lang:      tag,
				Release:   release,
				Flags:     provider.Flags(language.Flags{HI: it.HI}, it.Comment, it.Name, release),
				MatchedBy: by,
			})
		}
	}
	return out
}

// downloadPath is the path of an answer's relative download link, its query
// (which echoes the key) dropped, since Fetch adds the key itself.
func downloadPath(link string) (string, bool) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "", false
	}
	return u.Path, true
}
