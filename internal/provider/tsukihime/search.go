package tsukihime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/rank"
)

// maxRequests is design 5.7's cap on one Search, well under the site's 25
// requests per 10 seconds per IP: a landing costs 2, then 1 per torrent.
const maxRequests = 8

// subtitleTrack is an attachment's type for a subtitle track; 0 is a font
// and 3 carries no info.
const subtitleTrack = 1

// errSpent ends a Search whose request cap is reached, with what it read.
var errSpent = errors.New("request cap reached")

// row is one torrent of an episode's or a film's list.
type row struct {
	Sublangs  []string `json:"sublangs"`
	ID        int      `json:"id"`
	Filecount int      `json:"filecount"`
}

type list struct {
	Results []row `json:"results"`
	Error   bool  `json:"error"`
}

type torrent struct {
	Files []file `json:"files"`
}

type file struct {
	Filename    string       `json:"filename"`
	Attachments []attachment `json:"attachments"`
}

type attachment struct {
	Info *struct {
		Lang   string `json:"lang"`
		Name   string `json:"name"`
		Forced int    `json:"forced"`
	} `json:"info"`
	ID   int64 `json:"id"`
	Type int   `json:"type"`
}

// search is one Search's state: its request budget and what it has read.
type search struct {
	a      *adapter
	x      *mapread.Index
	q      *provider.Query
	animes map[int]int // an AniDB id's TsukiHime anime, 0 when TsukiHime lists none
	read   map[int]bool
	seen   map[int64]bool
	out    []provider.Candidate
	left   int
}

func (a *adapter) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) { //nolint:gocritic // hugeParam: provider.Adapter takes the Query by value, so concurrent adapters never share one
	if q.Kind != media.Episode && q.Kind != media.Movie {
		return nil, &provider.Skipped{Cause: "not an episode or a movie"}
	}
	if !animemap.Keyed(&q) {
		return nil, &provider.Skipped{Cause: "no TVDB episode or TMDB movie id"}
	}
	x := a.m.Index(ctx, a.c)
	if x == nil {
		return nil, &provider.Skipped{Cause: "the anime map is not loaded"}
	}
	ls := regular(animemap.Landings(x, &q))
	if len(ls) == 0 {
		return nil, &provider.Skipped{Cause: "no regular AniDB episode lands here"}
	}
	s := &search{a: a, x: x, q: &q, animes: map[int]int{}, read: map[int]bool{}, seen: map[int64]bool{}, left: maxRequests}
	for i := range ls {
		if err := s.landing(ctx, &ls[i]); errors.Is(err, errSpent) {
			break
		} else if err != nil {
			return s.out, err
		}
	}
	return s.out, nil
}

// regular keeps the regular landings: TsukiHime numbers only an anime's
// regular episodes, so a special's number would ask the regular episode of
// that number (testdata/specials.md).
func regular(ls []mapread.Landing) []mapread.Landing {
	var out []mapread.Landing
	for _, l := range ls {
		if l.Kind == mapread.Regular {
			out = append(out, l)
		}
	}
	return out
}

// landing reads l's torrents in cap order. An episode lists them by its
// number; a film's torrents carry no episode number, so it lists its
// anime's.
func (s *search) landing(ctx context.Context, l *mapread.Landing) error {
	id, err := s.anime(ctx, l.AniDB)
	if err != nil || id == 0 {
		return err
	}
	path := fmt.Sprintf("/animes/%d/episodes/%d", id, l.Number)
	if s.q.Kind == media.Movie {
		path = "/torrents?" + url.Values{"anime_id": {strconv.Itoa(id)}}.Encode()
	}
	var ts list
	if err := s.get(ctx, path, &ts); err != nil {
		return err
	}
	if ts.Error {
		return provider.Fail(provider.Permanent, "TsukiHime answered its torrent list with an error")
	}
	for _, t := range capOrder(ts.Results, s.q.Langs) {
		if s.read[t.ID] {
			continue
		}
		s.read[t.ID] = true
		var d torrent
		switch err := s.get(ctx, "/torrents/"+strconv.Itoa(t.ID), &d); {
		case notFound(err):
			continue
		case err != nil:
			return err
		}
		s.offer(l, d.Files)
	}
	return nil
}

// anime is TsukiHime's id of AniDB anime aid, asked once per Search; 0 when
// TsukiHime lists none.
func (s *search) anime(ctx context.Context, aid int) (int, error) {
	if id, ok := s.animes[aid]; ok {
		return id, nil
	}
	var a struct {
		ID int `json:"id"`
	}
	switch err := s.get(ctx, "/animes/anidb/"+strconv.Itoa(aid), &a); {
	case notFound(err):
		s.animes[aid] = 0
		return 0, nil
	case err != nil:
		return 0, err
	case a.ID <= 0:
		return 0, provider.Fail(provider.Permanent, "TsukiHime answered an anime with no id")
	}
	s.animes[aid] = a.ID
	return a.ID, nil
}

// offer adds the subtitles of the files of a torrent that are landing l's.
func (s *search) offer(l *mapread.Landing, fs []file) {
	for i, keep := range s.files(l, fs) {
		if !keep {
			continue
		}
		for j := range fs[i].Attachments {
			if c, ok := s.subtitle(l, &fs[i], &fs[i].Attachments[j]); ok {
				s.out = append(s.out, c)
			}
		}
	}
}

// files reports which of a torrent's files are landing l's. A single file
// is. Of several, an episode keeps each whose claim Lands on l and a film
// each claiming nothing, a one-number form counting as none, since a film's
// name carries its title's numbers (design 5.5).
func (s *search) files(l *mapread.Landing, fs []file) []bool {
	if len(fs) < 2 {
		return slices.Repeat([]bool{true}, len(fs))
	}
	keep := make([]bool, len(fs))
	names := make([]string, len(fs))
	for i := range fs {
		names[i] = fs[i].Filename
	}
	for i, c := range animemap.ReadEntry(names, true) {
		if s.q.Kind == media.Movie {
			keep[i] = c.Step == animemap.NoClaim || c.Step == animemap.ByAbsolute
		} else {
			keep[i] = c.Lands(s.x, s.q, l)
		}
	}
	if !slices.Contains(keep, true) {
		slog.Debug("tsukihime: no file of the torrent is the landing's episode", "anidb", l.AniDB, "episode", l.Number, "files", len(fs))
	}
	return keep
}

// subtitle is at as a candidate when it is a subtitle track in an asked
// language not offered yet: releases of one episode often share a track.
func (s *search) subtitle(l *mapread.Landing, f *file, at *attachment) (provider.Candidate, bool) {
	if at.Type != subtitleTrack || at.Info == nil || at.ID <= 0 || at.ID > math.MaxUint32 || s.seen[at.ID] {
		return provider.Candidate{}, false
	}
	tag, ok := language.Parse(at.Info.Lang)
	if !ok || !slices.Contains(s.q.Langs, tag) {
		return provider.Candidate{}, false
	}
	s.seen[at.ID] = true
	return provider.Candidate{
		Ref:       provider.NewRef(strconv.FormatInt(at.ID, 10)),
		Lang:      tag,
		Flags:     provider.Flags(language.Flags{Forced: at.Info.Forced == 1}, at.Info.Name),
		Release:   f.Filename[strings.LastIndexAny(f.Filename, `/\`)+1:],
		MatchedBy: rank.ID,
		Covers:    animemap.Covers(s.x, l.Ref, s.q),
	}, true
}

// get decodes the answer to path into v, one request of the Search's cap.
func (s *search) get(ctx context.Context, path string, v any) error {
	if s.left == 0 {
		return errSpent
	}
	s.left--
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+path, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := s.a.c.DoNoRedirect(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return provider.Fail(provider.Permanent, "decode TsukiHime answer: "+err.Error())
	}
	return nil
}

// notFound reports a 404 or 410: an anime or torrent TsukiHime does not list.
func notFound(err error) bool {
	f, ok := errors.AsType[*provider.Failure](err)
	return ok && f.Kind == provider.NotFound
}
