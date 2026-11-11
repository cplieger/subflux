// Package animetosho is the AnimeTosho provider: the subtitle attachments of
// the anime releases AnimeTosho's feed indexes, searched by AniDB episode or
// anime through the anime map, else by title.
package animetosho

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"golang.org/x/sync/errgroup"
)

const (
	feedHost    = "feed.animetosho.net"
	storageHost = "storage.animetosho.net"
	feedURL     = "https://" + feedHost + "/json"

	// A query reads its first six entries AnimeTosho did not skip, all at
	// once.
	maxEntries = 6
)

type adapter struct {
	m *animemap.Map
	c provider.Client
}

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	if q.Kind != media.Episode && q.Kind != media.Movie {
		return nil, &provider.Skipped{Cause: "not an episode or a movie"}
	}
	if animemap.Keyed(&q) {
		if x := a.m.Index(ctx, a.c); x != nil {
			if ls := animemap.Landings(x, &q); len(ls) > 0 {
				return a.searchLandings(ctx, &placer{x: x, q: &q, landings: ls})
			}
		}
	}
	if len(q.Titles) == 0 {
		return nil, &provider.Skipped{Cause: "no title and no AniDB episode"}
	}
	query := q.Titles[0]
	if q.Kind == media.Episode {
		query = fmt.Sprintf("%s S%02d", query, q.Numbering.Season)
	}
	answers, err := a.read(ctx, url.Values{"q": {query}})
	var out []provider.Candidate
	for _, an := range answers {
		out = append(out, titleCandidates(an, &q)...)
	}
	return dedupe(out), err
}

// feedQuery is one id query: a Listed landing's ?eid=, or one anime's ?aid=
// for all of its other landings.
type feedQuery struct {
	eid   *mapread.Landing // the ?eid= query's landing, nil for ?aid=
	param string
	id    int
	anime int
}

// searchLandings runs every landing's query in turn. A rate limit ends the
// search with what was read; any other failure is returned, after the other
// queries ran, beside what they gave.
func (a *adapter) searchLandings(ctx context.Context, p *placer) ([]provider.Candidate, error) {
	var (
		out   []provider.Candidate
		first error
	)
	for _, fq := range queries(p.x, p.landings) {
		answers, err := a.read(ctx, url.Values{fq.param: {strconv.Itoa(fq.id)}})
		for _, an := range answers {
			out = append(out, p.candidates(an, &fq)...)
		}
		if f, ok := errors.AsType[*provider.Failure](err); ok && f.Kind == provider.RateLimited {
			return dedupe(out), err
		}
		first = cmp.Or(first, err)
	}
	return dedupe(out), first
}

// queries are the landings' feed queries: one ?eid= per Listed landing, since
// animap publishes AniDB's episode ids, and one ?aid= per anime for its
// NotListed and ListingUnknown landings, whatever their kind.
func queries(x *mapread.Index, ls []mapread.Landing) []feedQuery {
	var out []feedQuery
	aids := map[int]bool{}
	for i := range ls {
		l := &ls[i]
		ep, ok := x.Episode(l.Ref)
		switch {
		case !ok:
		case ep.Listing == mapread.Listed:
			out = append(out, feedQuery{param: "eid", id: ep.ID, anime: l.AniDB, eid: l})
		case !aids[l.AniDB]:
			aids[l.AniDB] = true
			out = append(out, feedQuery{param: "aid", id: l.AniDB, anime: l.AniDB})
		}
	}
	return out
}

// dedupe keeps each attachment once, as first offered.
func dedupe(cs []provider.Candidate) []provider.Candidate {
	seen := map[string]bool{}
	out := cs[:0]
	for i := range cs {
		if tok := cs[i].Ref.Token(); !seen[tok] {
			seen[tok] = true
			out = append(out, cs[i])
		}
	}
	return out
}

type entry struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	ID     int    `json:"id"`
}

// answer is one entry of a feed answer with its files.
type answer struct {
	title string
	files []file
}

type detail struct {
	Files []file `json:"files"`
}

type file struct {
	Filename    string       `json:"filename"`
	Attachments []attachment `json:"attachments"`
}

type attachment struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	Info struct {
		Lang   string `json:"language_code"`
		Name   string `json:"name"`
		Forced bool   `json:"forced"`
	} `json:"info"`
}

// read runs one feed query and reads its first maxEntries entries' files at
// once. A rate limit ends it with what was read; any other failed entry is
// returned, after the rest are read, beside what they gave.
func (a *adapter) read(ctx context.Context, v url.Values) ([]answer, error) {
	var feed []entry
	if err := a.getJSON(ctx, feedURL+"?"+v.Encode(), &feed); err != nil {
		return nil, err
	}
	var es []entry
	for _, e := range feed {
		if e.ID > 0 && e.Status != "skipped" {
			es = append(es, e)
			if len(es) == maxEntries {
				break
			}
		}
	}
	return a.details(ctx, es)
}

// details reads each entry's files at once, as read says.
func (a *adapter) details(ctx context.Context, es []entry) ([]answer, error) {
	per := make([]answer, len(es))
	var (
		mu    sync.Mutex
		first error
	)
	g, gctx := errgroup.WithContext(ctx)
	for i, e := range es {
		g.Go(func() error {
			var d detail
			err := a.getJSON(gctx, feedURL+"?"+url.Values{"show": {"torrent"}, "id": {strconv.Itoa(e.ID)}}.Encode(), &d)
			if f, ok := errors.AsType[*provider.Failure](err); ok && f.Kind == provider.RateLimited {
				return err
			}
			if err != nil {
				mu.Lock()
				first = cmp.Or(first, err)
				mu.Unlock()
				return nil
			}
			per[i] = answer{title: e.Title, files: d.Files}
			return nil
		})
	}
	err := g.Wait()
	return per, cmp.Or(err, first)
}

// Fetch downloads an attachment from AnimeTosho's storage as stored, xz
// compressed, named as the link names it less its ".xz".
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) ([]byte, error) {
	u, ok := storageURL(ref.Token())
	if !ok {
		return nil, provider.Fail(provider.Permanent, "not an AnimeTosho storage link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
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
	if len(b) == 0 {
		return nil, provider.Fail(provider.Permanent, "AnimeTosho answered an empty attachment")
	}
	return b, nil
}

// storageURL is an attachment link on AnimeTosho's storage host, which is
// the only host a download may reach (S-AT1).
func storageURL(s string) (*url.URL, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host != storageHost || u.User != nil {
		return nil, false
	}
	return u, true
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
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, "decode AnimeTosho answer: "+err.Error())
	}
	return nil
}
