package jimaku

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/subtitleext"
)

// maxEntries bounds the entries one Search reads the files of. A TMDB id
// names one entry (none of Jimaku's 12,503 shared one on 2026-11-12), so it
// bounds only a malformed answer.
const maxEntries = 2

// codeUnauthorized is the ApiError code of a missing or refused key.
const codeUnauthorized = 7

type entry struct {
	ID int64 `json:"id"`
}

type file struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

// apiError is Jimaku's error body, {"error": "unauthorized", "code": 7}.
type apiError struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	var kind string
	switch q.Kind {
	case media.Episode:
		kind = "tv"
	case media.Movie:
		kind = "movie"
	default:
		return nil, &provider.Skipped{Cause: "not an episode or a movie"}
	}
	if q.External.TMDB == 0 {
		return nil, &provider.Skipped{Cause: "no TMDB id"}
	}
	id := kind + ":" + strconv.FormatUint(q.External.TMDB, 10)
	es, err := a.entries(ctx, id, false)
	if err == nil && len(es) == 0 {
		es, err = a.entries(ctx, id, true)
	}
	if err != nil {
		return nil, err
	}
	var out []provider.Candidate
	for _, e := range es[:min(len(es), maxEntries)] {
		var fs []file
		if err := a.get(ctx, apiURL+"/entries/"+strconv.FormatInt(e.ID, 10)+"/files", &fs); err != nil {
			return out, err
		}
		out = append(out, candidates(fs)...)
	}
	return out, nil
}

// entries are the entries Jimaku keys by TMDB id among its anime, or among
// its other entries: the search's anime filter, true by default, splits the
// two, and TMDB keys mostly the others (testdata/README.md).
func (a *adapter) entries(ctx context.Context, id string, anime bool) ([]entry, error) {
	v := url.Values{"anime": {strconv.FormatBool(anime)}, "tmdb_id": {id}}
	var es []entry
	err := a.get(ctx, apiURL+"/entries/search?"+v.Encode(), &es)
	return es, err
}

// get decodes the list Jimaku answers to u into v. A 200 carrying an
// ApiError object is a refusal: Auth for code 7, else Permanent.
func (a *adapter) get(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", a.key)
	resp, err := a.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return err
	}
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		var e apiError
		if err := json.Unmarshal(b, &e); err != nil {
			return provider.Fail(provider.Permanent, "decode Jimaku error: "+err.Error())
		}
		if e.Code == codeUnauthorized {
			return provider.Fail(provider.Auth, "Jimaku refused the key: "+e.Error)
		}
		return provider.Fail(provider.Permanent, "Jimaku error "+strconv.Itoa(e.Code)+": "+e.Error)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, "decode Jimaku answer: "+err.Error())
	}
	return nil
}

// candidates are the subtitle files and zip or rar packs of an entry's
// files whose URL Fetch accepts, each Japanese and matched by id; the engine
// chooses a pack's member (design 8.11).
func candidates(fs []file) []provider.Candidate {
	var out []provider.Candidate
	for _, f := range fs {
		if _, ok := fileURL(f.URL); !ok || !offered(f.Name) {
			continue
		}
		out = append(out, provider.Candidate{
			Ref:       provider.NewRef(f.URL),
			Lang:      japanese,
			Release:   f.Name,
			Flags:     provider.Flags(language.Flags{}, f.Name),
			MatchedBy: rank.ID,
		})
	}
	return out
}

// offered reports a file name the engine can read: a subtitle, or a zip or
// rar pack. Jimaku also lists 7z packs and image subtitles, which it cannot.
func offered(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".zip", ".rar":
		return true
	}
	return subtitleext.ArchiveInput(name)
}
