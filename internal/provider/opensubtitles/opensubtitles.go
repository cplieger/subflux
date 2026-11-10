// Package opensubtitles is the OpenSubtitles.com provider. Its REST API needs
// an account and an API key: a login answers a token and, for a VIP account,
// a faster search host; a search asks by TMDB or IMDb id, then by title when
// the id finds nothing; a download POST spends one unit of the account's
// daily quota and answers a one-time link on www.opensubtitles.com.
package opensubtitles

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"golang.org/x/time/rate"
)

// ID is OpenSubtitles' provider id.
const ID provider.ProviderID = "opensubtitles"

const (
	apiHost  = "api.opensubtitles.com"
	vipHost  = "vip-api.opensubtitles.com"
	linkHost = "www.opensubtitles.com"
	apiPath  = "/api/v1"

	keyUsername  = "username"
	keyPassword  = "password"
	keyAPIKey    = "api_key"
	keyUseHash   = "use_hash"
	keyIncludeAI = "include_ai_translated"
	keyIncludeMT = "include_machine_translated"

	// A search stops after three pages or once 60 candidates are read.
	maxPages      = 3
	enoughResults = 60

	maxListBody   = 2 << 20
	maxAnswerBody = 64 << 10
)

// The account's request rates: a free account is allowed one request a
// second, a VIP five.
var (
	freeRate = rate.Every(time.Second)
	vipRate  = rate.Every(200 * time.Millisecond)
)

// languages are the codes of OpenSubtitles' /infos/languages, lower case as
// it lists them; a search answer's label is lower-cased before the lookup.
// Codes language.Parse reads as another language (zh-ca, Cantonese) or not
// at all are left out, so their files are dropped.
var languages = language.MustProviderMap(provider.WirePairs(
	"ab", "ab", "af", "af", "sq", "sq", "am", "am", "ar", "ar", "an", "an",
	"hy", "hy", "as", "as", "az-az", "az", "eu", "eu", "be", "be", "bn", "bn",
	"bs", "bs", "br", "br", "bg", "bg", "my", "my", "ca", "ca",
	"zh-cn", "zh-Hans", "zh-tw", "zh-Hant", "hr", "hr", "cs", "cs", "da", "da",
	"nl", "nl", "en", "en", "eo", "eo", "et", "et", "fi", "fi", "fr", "fr",
	"gd", "gd", "gl", "gl", "ka", "ka", "de", "de", "el", "el", "he", "he",
	"hi", "hi", "hu", "hu", "is", "is", "ig", "ig", "id", "id", "ia", "ia",
	"ga", "ga", "it", "it", "ja", "ja", "kn", "kn", "kk", "kk", "km", "km",
	"ko", "ko", "ku", "ku", "lv", "lv", "lt", "lt", "lb", "lb", "mk", "mk",
	"ms", "ms", "ml", "ml", "mr", "mr", "mn", "mn", "nv", "nv", "ne", "ne",
	"se", "se", "no", "no", "oc", "oc", "or", "or", "fa", "fa", "pl", "pl",
	"pt-pt", "pt-PT", "pt-br", "pt-BR", "ps", "ps", "ro", "ro", "ru", "ru",
	"sr", "sr", "sd", "sd", "si", "si", "sk", "sk", "sl", "sl", "so", "so",
	"es", "es", "ea", "es-419", "sw", "sw", "sv", "sv", "tl", "tl", "ta", "ta",
	"tt", "tt", "te", "te", "th", "th", "tr", "tr", "tk", "tk", "uk", "uk",
	"ur", "ur", "uz", "uz", "vi", "vi", "cy", "cy",
))

// NewEntry is OpenSubtitles' row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:      ID,
		Summary: "Movie and TV subtitles from OpenSubtitles.com; needs an account and an API key.",
		Hosts:   []string{apiHost, vipHost, linkHost},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyUsername, Label: "Username", Help: "Your opensubtitles.com account", Kind: formschema.String, Required: true},
			{Key: keyPassword, Label: "Password", Help: "Your opensubtitles.com password", Kind: formschema.Secret, Required: true},
			{Key: keyAPIKey, Label: "API key", Help: "From opensubtitles.com/consumers", Kind: formschema.Secret, Required: true},
			{Key: keyUseHash, Label: "Match by file hash", Help: "Fast and exact when OpenSubtitles knows the file", Kind: formschema.Bool, Default: "true"},
			{Key: keyIncludeAI, Label: "Include AI translations", Help: "Subtitles from OpenSubtitles' own AI translation pipeline", Kind: formschema.Bool, Default: "false"},
			{Key: keyIncludeMT, Label: "Include machine translations", Help: "Older machine-translated uploads", Kind: formschema.Bool, Default: "false"},
		}},
		Languages: languages,
		Quirk:     quirk,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{
				c: c, pace: rate.NewLimiter(freeRate, 1),
				user: v.String(keyUsername), password: v.Secret(keyPassword), key: v.Secret(keyAPIKey),
				useHash: v.Bool(keyUseHash), includeAI: v.Bool(keyIncludeAI), includeMT: v.Bool(keyIncludeMT),
			}, nil
		},
	}
}

// quirk reads a 406, OpenSubtitles' spent daily download quota, as a rate
// limit until its Retry-After or else the next UTC midnight, when the quota
// renews.
func quirk(resp *http.Response, _ []byte) error {
	if resp.StatusCode != http.StatusNotAcceptable {
		return nil
	}
	wait := httpclient.ParseRetryAfterResponse(resp)
	if wait <= 0 {
		wait = untilUTCMidnight(time.Now())
	}
	return &provider.Failure{Kind: provider.RateLimited, RetryAfter: wait, Msg: "the daily download quota is spent"}
}

func untilUTCMidnight(now time.Time) time.Duration {
	n := now.UTC()
	return max(time.Date(n.Year(), n.Month(), n.Day()+1, 0, 0, 0, 0, time.UTC).Sub(n), time.Second)
}

type adapter struct {
	c         provider.Client
	pace      *rate.Limiter
	sess      session
	user      string
	password  string
	key       string
	mu        sync.Mutex
	useHash   bool
	includeAI bool
	includeMT bool
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
	slices.Sort(wires)
	s, err := a.session(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.Candidate
	seen := map[string]bool{}
	for _, n := range numberings(&q) {
		got, err := a.searchNumbering(ctx, s, &q, wires, n)
		for i := range got {
			if tok := got[i].Ref.Token(); !seen[tok] {
				seen[tok] = true
				out = append(out, got[i])
			}
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// numbering is one season and episode a search asks.
type numbering struct{ season, episode int }

// numberings are the aired numbering, then, outside the specials, the
// absolute number as season 1.
func numberings(q *provider.Query) []numbering {
	if q.Kind != media.Episode {
		return []numbering{{}}
	}
	out := []numbering{{q.Numbering.Season, q.Numbering.Episode}}
	if abs := (numbering{1, q.Absolute}); q.Absolute > 0 && q.Numbering.Season != 0 && abs != out[0] {
		out = append(out, abs)
	}
	return out
}

// searchNumbering asks by id, then by the first title when the id found
// nothing or q has no id OpenSubtitles searches by.
func (a *adapter) searchNumbering(ctx context.Context, s session, q *provider.Query, wires []string, n numbering) ([]provider.Candidate, error) {
	if params, ok := a.idParams(q, wires, n); ok {
		got, err := a.pages(ctx, s, params, q.Langs, rank.ID)
		if err != nil || len(got) > 0 {
			return got, err
		}
	}
	if len(q.Titles) == 0 {
		return nil, nil
	}
	params := a.commonParams(q, wires, n)
	params.Set("query", q.Titles[0])
	return a.pages(ctx, s, params, q.Langs, rank.Title)
}

// commonParams are the parameters every search sends. The two translation
// opt-ins send only their non-default direction, since the API's defaults
// differ (AI translations included, machine translations excluded).
func (a *adapter) commonParams(q *provider.Query, wires []string, n numbering) url.Values {
	p := url.Values{"languages": {strings.Join(wires, ",")}}
	if q.Kind == media.Episode {
		if n.episode > 0 {
			p.Set("episode_number", strconv.Itoa(n.episode))
		}
		if n.season > 0 {
			p.Set("season_number", strconv.Itoa(n.season))
		}
	}
	if !a.includeAI {
		p.Set("ai_translated", "exclude")
	}
	if a.includeMT {
		p.Set("machine_translated", "include")
	}
	return p
}

// idParams are commonParams with the video's hash and the id OpenSubtitles
// searches q by: a movie's TMDB id, else its IMDb id, an episode's series
// IMDb id; false when q has none of them.
func (a *adapter) idParams(q *provider.Query, wires []string, n numbering) (url.Values, bool) {
	p := a.commonParams(q, wires, n)
	switch ids := q.External; {
	case q.Kind == media.Episode && ids.IMDb != 0:
		p.Set("parent_imdb_id", strconv.FormatUint(ids.IMDb, 10))
	case q.Kind == media.Movie && ids.TMDB != 0:
		p.Set("tmdb_id", strconv.FormatUint(ids.TMDB, 10))
	case q.Kind == media.Movie && ids.IMDb != 0:
		p.Set("imdb_id", strconv.FormatUint(ids.IMDb, 10))
	default:
		return nil, false
	}
	if a.useHash && q.Hash.OSHash != 0 {
		p.Set("moviehash", fmt.Sprintf("%016x", q.Hash.OSHash))
	}
	return p, true
}

type searchAnswer struct {
	Data []struct {
		Attributes attributes `json:"attributes"`
	} `json:"data"`
	TotalPages int `json:"total_pages"`
}

type attributes struct {
	Language       string `json:"language"`
	Release        string `json:"release"`
	FeatureDetails struct {
		Title       string `json:"title"`
		ParentTitle string `json:"parent_title"`
		Year        int    `json:"year"`
	} `json:"feature_details"`
	Files []struct {
		FileName string `json:"file_name"`
		FileID   int    `json:"file_id"`
	} `json:"files"`
	FPS               float64 `json:"fps"`
	HearingImpaired   bool    `json:"hearing_impaired"`
	ForeignPartsOnly  bool    `json:"foreign_parts_only"`
	AITranslated      bool    `json:"ai_translated"`
	MachineTranslated bool    `json:"machine_translated"`
	MoviehashMatch    bool    `json:"moviehash_match"`
}

// pages reads up to maxPages of a search, returning the pages read with a
// later page's failure.
func (a *adapter) pages(ctx context.Context, s session, params url.Values, want []language.Tag, by rank.MatchedBy) ([]provider.Candidate, error) {
	var out []provider.Candidate
	for page := 1; page <= maxPages; page++ {
		if page > 1 {
			params.Set("page", strconv.Itoa(page))
		}
		var r searchAnswer
		if err := a.call(ctx, http.MethodGet, s.host, "/subtitles", params, nil, s.token, maxListBody, &r); err != nil {
			return out, err
		}
		for i := range r.Data {
			if c, ok := a.candidate(&r.Data[i].Attributes, want, by); ok {
				out = append(out, c)
			}
		}
		if len(out) >= enoughResults || page >= r.TotalPages {
			break
		}
	}
	return out, nil
}

// candidate is one answered subtitle's first file, unless its language is
// not asked or it is a translation the user did not opt into, which is then
// marked MachineTranslated. A title match carries the feature's title and
// year, an episode's being its series'.
func (a *adapter) candidate(at *attributes, want []language.Tag, by rank.MatchedBy) (provider.Candidate, bool) {
	if len(at.Files) == 0 || at.Files[0].FileID <= 0 ||
		(at.AITranslated && !a.includeAI) || (at.MachineTranslated && !a.includeMT) {
		return provider.Candidate{}, false
	}
	tag, ok := languages.Tag(strings.ToLower(at.Language))
	if !ok || !slices.Contains(want, tag) {
		return provider.Candidate{}, false
	}
	c := provider.Candidate{
		Ref:               provider.NewRef(strconv.Itoa(at.Files[0].FileID)),
		Lang:              tag,
		Release:           at.Release,
		Flags:             provider.Flags(language.Flags{HI: at.HearingImpaired, Forced: at.ForeignPartsOnly}, at.Release, at.Files[0].FileName),
		MatchedBy:         by,
		MachineTranslated: at.AITranslated || at.MachineTranslated,
		FPS:               at.FPS,
	}
	if by == rank.Title {
		c.Title, c.Year = cmp.Or(at.FeatureDetails.ParentTitle, at.FeatureDetails.Title), at.FeatureDetails.Year
	}
	if at.MoviehashMatch {
		c.MatchedBy = rank.Hash
	}
	return c, true
}

// Test logs in, which proves the API key (refused with a 403) and the
// account (a 401) at once.
func (a *adapter) Test(ctx context.Context) error {
	_, err := a.login(ctx)
	return err
}

type downloadAnswer struct {
	Link      string `json:"link"`
	ResetUTC  string `json:"reset_time_utc"`
	Requests  int    `json:"requests"`
	Remaining int    `json:"remaining"`
}

// Fetch asks the API host for the file's link, which spends a quota unit and
// so is sent once, then GETs the link.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) (provider.Payload, error) {
	id, err := strconv.ParseUint(ref.Token(), 10, 31)
	if err != nil || id == 0 {
		return provider.Payload{}, provider.Fail(provider.Permanent, "the file id is not a number")
	}
	s, err := a.session(ctx)
	if err != nil {
		return provider.Payload{}, err
	}
	body, err := json.Marshal(struct {
		Format string `json:"sub_format"`
		FileID uint64 `json:"file_id"`
	}{"srt", id})
	if err != nil {
		return provider.Payload{}, err
	}
	// /download answers 503 on the VIP search host, so it always goes to
	// the API host.
	var r downloadAnswer
	err = a.call(ctx, http.MethodPost, apiHost, "/download", nil, body, s.token, maxAnswerBody, &r)
	if err != nil {
		return provider.Payload{}, err
	}
	if r.Remaining*10 < (r.Requests+r.Remaining)*3 {
		slog.Warn("opensubtitles: download quota low", "remaining", r.Remaining, "used", r.Requests)
	}
	link, err := url.Parse(r.Link)
	if err != nil || r.Link == "" {
		return provider.Payload{}, provider.Fail(provider.Permanent, "OpenSubtitles answered a download with no link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), http.NoBody)
	if err != nil {
		return provider.Payload{}, provider.Fail(provider.Permanent, "OpenSubtitles answered an unusable link")
	}
	resp, err := a.do(req, "")
	if err != nil {
		return provider.Payload{}, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return provider.Payload{}, err
	}
	if len(b) == 0 {
		return provider.Payload{}, provider.Fail(provider.Permanent, "OpenSubtitles answered an empty file")
	}
	return provider.Payload{Name: provider.PayloadName(resp), Bytes: b}, nil
}

// call sends one API request to host, carrying the key and, when set, the
// session's token, and decodes its JSON answer into v.
func (a *adapter) call(ctx context.Context, method, host, path string, params url.Values, body []byte, token string, limit int64, v any) error {
	u := "https://" + host + apiPath + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	var r io.Reader = http.NoBody
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Key", a.key)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.do(req, token)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, "decode OpenSubtitles answer: "+err.Error())
	}
	return nil
}

// do paces req to the account's rate and sends it; a refusal of token drops
// the session, so the next call logs in again.
func (a *adapter) do(req *http.Request, token string) (*http.Response, error) {
	if err := a.pace.Wait(req.Context()); err != nil {
		return nil, err
	}
	resp, err := a.c.Do(req)
	if f, ok := errors.AsType[*provider.Failure](err); ok && f.Kind == provider.Auth && token != "" {
		a.forget(token)
	}
	return resp, err
}
