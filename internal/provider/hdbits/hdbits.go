// Package hdbits is the HDBits.org provider: its JSON API finds a title's
// torrents by TVDB season or IMDb id and lists each torrent's subtitles, and
// getdox.php serves a subtitle or season-pack archive. Every call carries the
// account's username and passkey; the API answers 200 with its verdict in a
// status field.
package hdbits

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/go/jsonx"
	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/subtitleext"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

// ID is HDBits' provider id.
const ID provider.ProviderID = "hdbits"

const (
	host   = "hdbits.org"
	apiURL = "https://" + host + "/api"

	keyUsername = "username"
	keyPasskey  = "passkey"

	// A search reads at most 50 torrents' subtitle lists, three at a time,
	// sending one every half second; a cached list waits for nothing.
	maxTorrents  = 50
	listWorkers  = 3
	listInterval = 500 * time.Millisecond

	// Downloads kept for a season's other episodes: 16 bodies of at most
	// 2 MiB, each for an hour.
	downloadEntries = 16
	downloadItem    = 2 << 20
	downloadTTL     = time.Hour

	maxListBody = 2 << 20
)

// The API's status codes: success, and the two that refuse the account. The
// rest (failure, ssl required, malformed json, missing or invalid
// parameters) describe the request. https://hdbits.org/wiki/API
const (
	statusSuccess         = 0
	statusAuthDataMissing = 4
	statusAuthFailed      = 5
)

// languages are HDBits' subtitle flags, which are mostly country codes: uk is
// English (every recorded uk file is eng or en), se Swedish, si Slovenian, br
// Brazilian Portuguese, hk Traditional and cn Simplified Chinese.
var languages = language.MustProviderMap(provider.WirePairs(
	"uk", "en", "br", "pt-BR", "cn", "zh-Hans", "hk", "zh-Hant", "cz", "cs",
	"dk", "da", "gr", "el", "il", "he", "kr", "ko", "se", "sv", "si", "sl",
	"fr", "fr", "es", "es", "de", "de", "it", "it", "pt", "pt", "nl", "nl",
	"ru", "ru", "ar", "ar", "ja", "ja", "no", "no", "fi", "fi", "pl", "pl",
	"hu", "hu", "ro", "ro", "tr", "tr", "th", "th", "vi", "vi", "id", "id",
	"bg", "bg", "hr", "hr", "sr", "sr", "sk", "sk", "ca", "ca", "eu", "eu",
	"gl", "gl", "fa", "fa", "ms", "ms", "sq", "sq", "bs", "bs", "hy", "hy",
	"az", "az", "bn", "bn", "mk", "mk", "hi", "hi", "ta", "ta", "te", "te",
	"ml", "ml", "kn", "kn", "mr", "mr", "ur", "ur", "ne", "ne", "af", "af",
	"sw", "sw", "lt", "lt", "lv", "lv", "et", "et", "is", "is", "ga", "ga",
	"cy", "cy", "ka", "ka", "mn", "mn", "km", "km", "lo", "lo", "my", "my",
))

// NewEntry is HDBits' row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:      ID,
		Summary: "Subtitles of HDBits.org torrents by TVDB or IMDb id; needs an account and its passkey.",
		Hosts:   []string{host},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyUsername, Label: "Username", Help: "Your hdbits.org account", Kind: formschema.String, Required: true},
			{Key: keyPasskey, Label: "Passkey", Help: "From your hdbits.org user settings", Kind: formschema.Secret, Required: true},
		}},
		Languages: languages,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{
				c: c, user: v.String(keyUsername), passkey: v.Secret(keyPasskey),
				torrents:  cache.New(cache.NonEmpty[int]),
				lists:     cache.New(cache.NonEmpty[subtitle]),
				downloads: newDownloadCache(downloadEntries, downloadItem),
			}, nil
		},
	}
}

type adapter struct {
	c         provider.Client
	torrents  *cache.Cache[[]int]
	lists     *cache.Cache[[]subtitle]
	downloads *downloadCache
	fetches   singleflight.Group
	user      string
	passkey   string
}

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	lookup, key, err := a.lookup(&q)
	if err != nil {
		return nil, err
	}
	ids, err := a.torrentIDs(ctx, lookup, key)
	if err != nil {
		return nil, err
	}
	return a.subtitles(ctx, ids, q.Langs)
}

// lookup is the torrent search's body and its cache key: an episode by its
// series' TVDB id and season, a movie by its IMDb id.
func (a *adapter) lookup(q *provider.Query) (body map[string]any, cacheKey string, err error) {
	body = map[string]any{keyUsername: a.user, keyPasskey: a.passkey}
	switch {
	case q.Kind == media.Episode && q.External.TVDB != 0:
		body["tvdb"] = map[string]any{"id": q.External.TVDB, "season": q.Numbering.Season}
		return body, keyenc.Join("tvdb", strconv.FormatUint(q.External.TVDB, 10), strconv.Itoa(q.Numbering.Season)), nil
	case q.Kind == media.Episode:
		return nil, "", &provider.Skipped{Cause: "no TVDB id"}
	case q.External.IMDb != 0:
		body["imdb"] = map[string]any{"id": q.External.IMDb}
		return body, keyenc.Join("imdb", strconv.FormatUint(q.External.IMDb, 10)), nil
	}
	return nil, "", &provider.Skipped{Cause: "no IMDb id"}
}

// torrentIDs are the lookup's torrents, the first maxTorrents of them.
func (a *adapter) torrentIDs(ctx context.Context, lookup map[string]any, key string) ([]int, error) {
	ids, err := a.torrents.GetOrFetch(ctx, key, func(ctx context.Context) ([]int, error) {
		var r struct {
			Data []struct {
				ID int `json:"id"`
			} `json:"data"`
		}
		if err := a.post(ctx, "/torrents", lookup, &r); err != nil {
			return nil, err
		}
		ids := make([]int, 0, len(r.Data))
		for _, d := range r.Data {
			ids = append(ids, d.ID)
		}
		return ids, nil
	})
	if err != nil {
		return nil, err
	}
	if len(ids) > maxTorrents {
		slog.Warn("hdbits: more torrents than a search reads", "found", len(ids), "read", maxTorrents)
		ids = ids[:maxTorrents]
	}
	return ids, nil
}

// subtitles reads every torrent's list. A refused account or a rate limit
// ends the search, since every later list would answer the same; any other
// failed list is returned, after the others are read, beside what they gave.
func (a *adapter) subtitles(ctx context.Context, ids []int, want []language.Tag) ([]provider.Candidate, error) {
	var (
		mu    sync.Mutex
		out   []provider.Candidate
		first error
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(listWorkers)
	pace := rate.NewLimiter(rate.Every(listInterval), 1)
	for _, id := range ids {
		g.Go(func() error {
			got, err := a.list(gctx, pace, id, want)
			mu.Lock()
			defer mu.Unlock()
			out = append(out, got...)
			if endsSearch(err) {
				return err
			}
			if err != nil && first == nil {
				first = err
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, first
}

// endsSearch reports whether err is a refused account or a rate limit,
// which every later list would answer too.
func endsSearch(err error) bool {
	f, ok := errors.AsType[*provider.Failure](err)
	return ok && (f.Kind == provider.Auth || f.Kind == provider.RateLimited)
}

type subtitle struct {
	Title    string     `json:"title"`
	Filename string     `json:"filename"`
	Language string     `json:"language"`
	ID       subtitleID `json:"id"`
}

// list is one torrent's subtitles in an asked language, commentary, extras,
// lyrics and files of another kind left out. A file whose title is empty
// takes its file name's stem as its release, since the recorded lists name
// each file after its release (551609's title plus ".ass").
func (a *adapter) list(ctx context.Context, pace *rate.Limiter, torrent int, want []language.Tag) ([]provider.Candidate, error) {
	subs, err := a.lists.GetOrFetch(ctx, strconv.Itoa(torrent), func(ctx context.Context) ([]subtitle, error) {
		if err := pace.Wait(ctx); err != nil {
			return nil, err
		}
		var r struct {
			Data []subtitle `json:"data"`
		}
		err := a.post(ctx, "/subtitles", map[string]any{keyUsername: a.user, keyPasskey: a.passkey, "torrent_id": torrent}, &r)
		return r.Data, err
	})
	if err != nil {
		return nil, err
	}
	var out []provider.Candidate
	for _, s := range subs {
		tag, ok := languages.Tag(s.Language)
		if !ok || !slices.Contains(want, tag) || !keep(s) {
			continue
		}
		release := s.Title
		if strings.TrimSpace(release) == "" {
			release = strings.TrimSuffix(s.Filename, path.Ext(s.Filename))
		}
		out = append(out, provider.Candidate{
			Ref:       provider.NewRef(token(s)),
			Lang:      tag,
			Release:   release,
			Flags:     provider.Flags(language.Flags{}, s.Title, s.Filename),
			MatchedBy: rank.ID,
		})
	}
	return out, nil
}

// token is the subtitle's id and the listed file's extension, "551609.ass",
// or the id alone when the extension is not 1 to 8 ASCII letters or digits;
// Fetch names the payload by it, since getdox names no file.
func token(s subtitle) string {
	id := strconv.Itoa(int(s.ID))
	if ext := strings.ToLower(strings.TrimPrefix(path.Ext(s.Filename), ".")); plainExt(ext) {
		return id + "." + ext
	}
	return id
}

func plainExt(ext string) bool {
	if len(ext) < 1 || len(ext) > 8 {
		return false
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

var excluded = []string{"commentary", "extras", "lyrics"}

// keep is whether a listed file is a subtitle or a season-pack archive, and
// not a commentary, extras or lyrics track.
func keep(s subtitle) bool {
	if ext := strings.ToLower(path.Ext(s.Filename)); ext != "" && ext != ".zip" && ext != ".rar" && !subtitleext.ArchiveInput(ext) {
		return false
	}
	text := strings.ToLower(s.Title + " " + s.Filename)
	for _, kw := range excluded {
		if strings.Contains(text, kw) {
			return false
		}
	}
	return true
}

// subtitleID reads the API's subtitle id, a number or a quoted number; a
// null, empty or non-positive id fails the decode, since id 0 would ask
// getdox for nothing.
type subtitleID int

var idPolicy = func() jsonx.Policy {
	p := jsonx.Strict()
	p.Null = jsonx.Reject
	p.EmptyString = jsonx.Reject
	p.MinValue = 1
	p.MaxValue = 1<<31 - 1
	return p
}()

func (id *subtitleID) UnmarshalJSON(data []byte) error {
	*id = 0
	n, err := jsonx.ParseInt64(data, idPolicy)
	if err != nil {
		return err
	}
	*id = subtitleID(n)
	return nil
}

// Test asks HDBits' own credential check, which reads the username and
// passkey and nothing else.
func (a *adapter) Test(ctx context.Context) error {
	var r struct{}
	return a.post(ctx, "/test", map[string]any{keyUsername: a.user, keyPasskey: a.passkey}, &r)
}

// Fetch answers the subtitle's getdox body, from the download cache when a
// season's earlier episode fetched it; concurrent fetches of one id share
// one download.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) (provider.Payload, error) {
	name := ref.Token()
	id, ext, dotted := strings.Cut(name, ".")
	if n, err := strconv.ParseUint(id, 10, 31); err != nil || n == 0 || strconv.FormatUint(n, 10) != id || dotted && !plainExt(ext) {
		return provider.Payload{}, provider.Fail(provider.Permanent, "the token is not a subtitle id")
	}
	if b, ok := a.downloads.get(id); ok {
		return provider.Payload{Name: name, Bytes: b}, nil
	}
	v, err, _ := a.fetches.Do(id, func() (any, error) {
		if b, ok := a.downloads.get(id); ok {
			return b, nil
		}
		return a.download(ctx, id)
	})
	if err != nil {
		return provider.Payload{}, err
	}
	b, _ := v.([]byte)
	return provider.Payload{Name: name, Bytes: b}, nil
}

func (a *adapter) download(ctx context.Context, id string) ([]byte, error) {
	u := "https://" + host + "/getdox.php?" + url.Values{"id": {id}, keyPasskey: {a.passkey}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := a.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return nil, err
	}
	if err := downloadVerdict(b); err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, provider.Fail(provider.Permanent, "HDBits answered an empty download")
	}
	a.downloads.put(id, b, func() {
		slog.Warn("hdbits: a download is over the cache's size; a season pack is fetched again for each episode", "bytes", len(b))
	})
	return b, nil
}

// downloadVerdict reads an API verdict out of a getdox body that is a JSON
// object carrying a status, which a subtitle or archive never is.
func downloadVerdict(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	var v struct {
		Status  *int   `json:"status"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &v) != nil || v.Status == nil {
		return nil
	}
	return verdict(*v.Status, v.Message)
}

// verdict is one API status: nil for success, Auth for a refused account,
// Permanent otherwise.
func verdict(status int, message string) error {
	switch status {
	case statusSuccess:
		return nil
	case statusAuthDataMissing, statusAuthFailed:
		return provider.Fail(provider.Auth, "HDBits refused the username and passkey (status "+strconv.Itoa(status)+": "+message+")")
	}
	return provider.Fail(provider.Permanent, "HDBits answered status "+strconv.Itoa(status)+": "+message)
}

// post sends one API call; HDBits answers 200 with its verdict in status,
// read before the data.
func (a *adapter) post(ctx context.Context, endpoint string, body map[string]any, v any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	answer, err := provider.ReadAll(resp, maxListBody)
	if err != nil {
		return err
	}
	var status struct {
		Status  *int   `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(answer, &status); err != nil || status.Status == nil {
		return provider.Fail(provider.Permanent, "HDBits answered no status")
	}
	if err := verdict(*status.Status, status.Message); err != nil {
		return err
	}
	if err := json.Unmarshal(answer, v); err != nil {
		return provider.Fail(provider.Permanent, "decode HDBits answer: "+err.Error())
	}
	return nil
}
