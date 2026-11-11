// Package gestdown is the Gestdown provider: Addic7ed's TV subtitles through
// Gestdown's REST API (https://gestdown.readme.io/reference), found by the
// series' TVDB id, one season and language per request.
package gestdown

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// ID is Gestdown's provider id.
const ID provider.ProviderID = "gestdown"

const (
	apiHost = "api.gestdown.info"
	baseURL = "https://" + apiHost

	maxListBody     = 1 << 20
	maxSeasonBody   = 5 << 20
	maxSubtitleBody = 5 << 20
)

// languages are the codes Gestdown's language path segment accepts, each
// its own wire label: it answers 400 to an Addic7ed name such as "Brazilian
// Portuguese" and to a code it does not know (testdata/languages-probe.json).
var languages = language.MustProviderMap(provider.WirePairs(codePairs(
	"en", "fr", "es", "es-419", "es-ES", "de", "it", "pt", "pt-BR", "nl",
	"ru", "ar", "ja", "zh-Hans", "zh-Hant", "ko", "sv", "no", "da", "fi",
	"pl", "cs", "hu", "ro", "tr", "el", "he", "th", "vi", "id", "bg", "hr",
	"sr", "sl", "sk", "uk", "ca", "eu", "gl", "fa", "ms", "sq", "bs", "hy",
	"az", "bn", "mk", "hi", "ta", "te", "ml", "kn", "mr", "ur", "ne", "si",
	"af", "sw", "lt", "lv", "et", "is", "ga", "cy", "ka", "mn", "km", "lo",
	"my",
)...))

func codePairs(codes ...string) []string {
	out := make([]string, 0, 2*len(codes))
	for _, c := range codes {
		out = append(out, c, c)
	}
	return out
}

// NewEntry is Gestdown's row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:        ID,
		Summary:   "TV subtitles from Addic7ed through the Gestdown API; needs no account.",
		Hosts:     []string{apiHost},
		Languages: languages,
		Quirk:     quirk,
		New: func(c provider.Client, _ formschema.Values) (provider.Adapter, error) {
			return &adapter{
				c:       c,
				shows:   cache.New(cache.NonEmpty[string]),
				seasons: cache.New(cache.NonEmpty[seasonEpisode]),
			}, nil
		},
	}
}

// quirk reads 423, Gestdown's answer while Addic7ed throttles it, as a rate
// limit.
func quirk(resp *http.Response, _ []byte) error {
	if resp.StatusCode != http.StatusLocked {
		return nil
	}
	return &provider.Failure{
		Kind: provider.RateLimited, Msg: "Addic7ed is throttling Gestdown",
		RetryAfter: max(httpclient.ParseRetryAfterResponse(resp), 0),
	}
}

type adapter struct {
	shows   *cache.Cache[[]string]
	seasons *cache.Cache[[]seasonEpisode]
	c       provider.Client
}

// Search asks each of q's languages in turn for the episode's season; a
// failed language returns what the earlier ones found with its failure.
func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	switch {
	case q.Kind != media.Episode:
		return nil, &provider.Skipped{Cause: "Gestdown has TV episodes only"}
	case q.External.TVDB == 0:
		return nil, &provider.Skipped{Cause: "no TVDB id"}
	case q.Numbering.Scheme != media.TVDB:
		return nil, &provider.Skipped{Cause: "the episode is not numbered in TVDB's order"}
	}
	shows, err := a.shows.GetOrFetch(ctx, keyenc.Join("show", strconv.FormatUint(q.External.TVDB, 10)), func(ctx context.Context) ([]string, error) {
		return a.findShows(ctx, q.External.TVDB)
	})
	if err != nil {
		return nil, err
	}
	var out []provider.Candidate
	for _, tag := range q.Langs {
		for _, wire := range languages.Wires(tag) {
			found, err := a.firstShowWith(ctx, shows, &q, wire, tag)
			out = append(out, found...)
			if err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

func (a *adapter) firstShowWith(ctx context.Context, shows []string, q *provider.Query, wire string, tag language.Tag) ([]provider.Candidate, error) {
	for _, show := range shows {
		eps, err := a.seasons.GetOrFetch(ctx, keyenc.Join("season", show, strconv.Itoa(q.Numbering.Season), wire), func(ctx context.Context) ([]seasonEpisode, error) {
			return a.season(ctx, show, q.Numbering.Season, wire)
		})
		if err != nil {
			return nil, err
		}
		if found := candidates(eps, q.Numbering.Episode, tag); len(found) > 0 {
			return found, nil
		}
	}
	return nil, nil
}

func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/subtitles/download/"+url.PathEscape(ref.Token()), http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := a.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, maxSubtitleBody)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// getJSON decodes a GET's answer into v; a 404 leaves v untouched.
func (a *adapter) getJSON(ctx context.Context, u string, limit int64, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := a.c.Do(req)
	if f, ok := errors.AsType[*provider.Failure](err); ok && f.Kind == provider.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, fmt.Sprintf("decode %s answer: %v", resp.Request.URL.Path, err))
	}
	return nil
}

// findShows is Gestdown's show ids for a TVDB series; one series can map to
// several Addic7ed shows.
func (a *adapter) findShows(ctx context.Context, tvdb uint64) ([]string, error) {
	var r struct {
		Shows []struct {
			ID string `json:"id"`
		} `json:"shows"`
	}
	if err := a.getJSON(ctx, fmt.Sprintf("%s/shows/external/tvdb/%d", baseURL, tvdb), maxListBody, &r); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(r.Shows))
	for _, s := range r.Shows {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	return ids, nil
}

type subtitle struct {
	SubtitleID      string `json:"subtitleId"`
	Version         string `json:"version"`
	DownloadURI     string `json:"downloadUri"`
	Completed       bool   `json:"completed"`
	HearingImpaired bool   `json:"hearingImpaired"`
}

type seasonEpisode struct {
	Subtitles []subtitle `json:"subtitles"`
	Number    int        `json:"number"`
}

func (a *adapter) season(ctx context.Context, show string, season int, wire string) ([]seasonEpisode, error) {
	var r struct {
		Episodes []seasonEpisode `json:"episodes"`
	}
	u := fmt.Sprintf("%s/shows/%s/%d/%s", baseURL, url.PathEscape(show), season, url.PathEscape(wire))
	if err := a.getJSON(ctx, u, maxSeasonBody, &r); err != nil {
		return nil, err
	}
	return r.Episodes, nil
}

// candidates are episode's completed subtitles whose download path is the
// one Fetch rebuilds from the subtitle id.
func candidates(eps []seasonEpisode, episode int, tag language.Tag) []provider.Candidate {
	var out []provider.Candidate
	for _, ep := range eps {
		if ep.Number != episode {
			continue
		}
		for _, s := range ep.Subtitles {
			if !s.Completed || s.SubtitleID == "" || s.DownloadURI != "/subtitles/download/"+s.SubtitleID {
				continue
			}
			versions := strings.Split(s.Version, ",")
			for i := range versions {
				versions[i] = strings.TrimSpace(versions[i])
			}
			release := strings.Join(versions, " ")
			out = append(out, provider.Candidate{
				Ref:       provider.NewRef(s.SubtitleID),
				Lang:      tag,
				Release:   release,
				Flags:     provider.Flags(language.Flags{HI: s.HearingImpaired}, release),
				MatchedBy: rank.ID,
			})
		}
	}
	return out
}
