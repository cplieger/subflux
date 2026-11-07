// Package betaseries is the BetaSeries provider: English and French TV
// subtitles from the BetaSeries API by the series' TVDB id, downloaded
// through www.betaseries.com's /srt links. It needs an API key.
package betaseries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// ID is BetaSeries' provider id.
const ID provider.ProviderID = "betaseries"

const (
	apiHost      = "api.betaseries.com"
	siteHost     = "www.betaseries.com"
	subtitleHost = "subtitles.betaseries.com"
	apiURL       = "https://" + apiHost
	srtURL       = "https://" + siteHost + "/srt/"

	keyToken  = "token"
	keyHeader = "X-BetaSeries-Key"

	// deadSource is the source whose links no longer answer.
	deadSource = "seriessub"

	// The error codes a 400 carries: an unknown series, a refused key.
	codeNotFound = 4001
	codeBadKey   = 1001

	maxAPIBody      = 5 << 20
	maxDownloadBody = 10 << 20
)

// languages are BetaSeries' labels: VO, the original English, and VF.
var languages = language.MustProviderMap(provider.WirePairs("VO", "en", "VF", "fr"))

// NewEntry is BetaSeries' row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:      ID,
		Summary: "English and French TV subtitles from BetaSeries; needs an API key.",
		Hosts:   []string{apiHost, siteHost, subtitleHost},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyToken, Label: "Token", Help: "From betaseries.com/en/account/api", Kind: formschema.Secret, Required: true},
		}},
		Languages: languages,
		Quirk:     quirk,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, key: v.Secret(keyToken)}, nil
		},
	}
}

type apiError struct {
	Text string `json:"text"`
	Code int    `json:"code"`
}

// quirk reads the code of a 400's first error: an unknown series is no such
// media and a refused key a credential failure.
func quirk(resp *http.Response, body []byte) error {
	if resp.StatusCode != http.StatusBadRequest {
		return nil
	}
	var r struct {
		Errors []apiError `json:"errors"`
	}
	if json.Unmarshal(body, &r) != nil || len(r.Errors) == 0 {
		return nil
	}
	switch r.Errors[0].Code {
	case codeNotFound:
		return provider.Fail(provider.NotFound, "BetaSeries knows no such series")
	case codeBadKey:
		return provider.Fail(provider.Auth, "BetaSeries refused the API key")
	}
	return nil
}

type adapter struct {
	c   provider.Client
	key string
}

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	switch {
	case q.Kind != media.Episode:
		return nil, &provider.Skipped{Cause: "BetaSeries has TV episodes only"}
	case q.External.TVDB == 0:
		return nil, &provider.Skipped{Cause: "no TVDB id"}
	}
	params := url.Values{
		"thetvdb_id": {strconv.FormatUint(q.External.TVDB, 10)},
		"season":     {strconv.Itoa(q.Numbering.Season)},
		"episode":    {strconv.Itoa(q.Numbering.Episode)},
		"subtitles":  {"1"},
		"v":          {"3.0"},
	}
	var r struct {
		Errors   []apiError `json:"errors"`
		Episodes []struct {
			Subtitles []entry `json:"subtitles"`
		} `json:"episodes"`
	}
	if err := a.getJSON(ctx, apiURL+"/shows/episodes?"+params.Encode(), &r); err != nil {
		return nil, err
	}
	if len(r.Errors) > 0 {
		return nil, provider.Fail(provider.Permanent, fmt.Sprintf("BetaSeries error %d: %s", r.Errors[0].Code, r.Errors[0].Text))
	}
	if len(r.Episodes) == 0 {
		return nil, nil
	}
	return candidates(r.Episodes[0].Subtitles, q.Langs), nil
}

// Test asks the API's status endpoint with the key.
func (a *adapter) Test(ctx context.Context) error {
	var r json.RawMessage
	return a.getJSON(ctx, apiURL+"/status", &r)
}

// Fetch GETs the subtitle's /srt link, which redirects to its file on
// subtitles.betaseries.com.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) (provider.Payload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srtURL+url.PathEscape(ref.Token()), http.NoBody)
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
	req.Header.Set(keyHeader, a.key)
	resp, err := a.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, maxAPIBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, "decode BetaSeries answer: "+err.Error())
	}
	return nil
}

type entry struct {
	Language string `json:"language"`
	Source   string `json:"source"`
	File     string `json:"file"`
	URL      string `json:"url"`
	ID       int    `json:"id"`
}

// candidates are the entries in an asked language whose link is the /srt
// link Fetch rebuilds from the id.
func candidates(entries []entry, langs []language.Tag) []provider.Candidate {
	var out []provider.Candidate
	for _, e := range entries {
		tag, ok := languages.Tag(strings.ToUpper(e.Language))
		if !ok || !slices.Contains(langs, tag) || e.Source == deadSource || e.ID <= 0 {
			continue
		}
		id := strconv.Itoa(e.ID)
		if e.URL != srtURL+id {
			continue
		}
		out = append(out, provider.Candidate{
			Ref:       provider.NewRef(id),
			Lang:      tag,
			Release:   e.File,
			Flags:     language.Flags{HI: provider.HearingImpaired(false, e.File)},
			MatchedBy: rank.ID,
		})
	}
	return out
}
