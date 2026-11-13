package animetosho

import (
	"log/slog"
	"slices"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/rank"
)

// placer places an id search's files on the landings it searched.
type placer struct {
	x        *mapread.Index
	q        *provider.Query
	landings []mapread.Landing
}

// place is where one file goes: placed as the landings refs, offered
// manually on a landing of its kind, or dropped.
type place struct {
	refs    []mapread.Ref
	offered bool
	ok      bool
}

// candidates are an id answer's subtitles in an asked language, on the files
// it places or offers; a file neither placed nor offered is dropped, which is
// how the rest of a fansub pack leaves. The one file of a ?eid= entry is that
// landing's episode whatever its kind, since AniDB's episode id names it.
func (p *placer) candidates(an answer, fq *feedQuery) []provider.Candidate {
	places := make([]place, len(an.files))
	if fq.eid != nil && len(an.files) == 1 {
		places[0] = place{refs: []mapread.Ref{fq.eid.Ref}, ok: true}
	} else {
		for i, c := range animemap.ReadEntry(fileNames(an.files), true) {
			places[i] = p.place(fq.anime, c)
		}
	}
	var out []provider.Candidate
	for i := range an.files {
		pl := places[i]
		if !pl.ok {
			continue
		}
		for j := range an.files[i].Attachments {
			c, ok := subtitle(&an.files[i].Attachments[j], an.title, p.q.Langs)
			if !ok {
				continue
			}
			c.MatchedBy, c.Unconfirmed, c.Covers = rank.ID, pl.offered, p.covers(pl.refs)
			out = append(out, c)
		}
	}
	return out
}

// place reads claim c of a file of anime's answer: placed on each searched
// landing of anime it Lands on, since AnimeTosho files later seasons under
// the first season's anime ("S02E05" lands through TVDB) and names the rest
// in AniDB's numbering ("2nd Season - 05"). On a one-episode anime a missing
// or one-number claim is episode 1 ("Show Movie 02" is a title). Credits and
// promotions match while the anime has a searched landing of that kind; a
// file claiming nothing is offered while it has a searched parody or other
// one, kinds AniDB numbers as one unreadable sequence.
func (p *placer) place(anime int, c animemap.Claim) place {
	single := animemap.Single(p.x, anime)
	if c.Step == animemap.ByAbsolute && single {
		c = animemap.Claim{}
	}
	switch c.Kind {
	case mapread.Regular, mapread.Special:
		refs := p.landed(anime, &c)
		return place{refs: refs, ok: len(refs) > 0}
	case mapread.Credit, mapread.Promotion:
		return place{offered: true, ok: p.has(anime, c.Kind)}
	}
	if single {
		if refs := p.landed(anime, &animemap.Claim{Kind: mapread.Regular, Episodes: []int{1}}); len(refs) > 0 {
			return place{refs: refs, ok: true}
		}
	}
	return place{offered: true, ok: p.has(anime, mapread.Parody) || p.has(anime, mapread.Other)}
}

// landed are the searched landings of anime claim c Lands on.
func (p *placer) landed(anime int, c *animemap.Claim) []mapread.Ref {
	var out []mapread.Ref
	for i := range p.landings {
		if l := &p.landings[i]; l.AniDB == anime && c.Lands(p.x, p.q, l) {
			out = append(out, l.Ref)
		}
	}
	return out
}

func (p *placer) has(anime int, kind mapread.Kind) bool {
	return slices.ContainsFunc(p.landings, func(l mapread.Landing) bool { return l.AniDB == anime && l.Kind == kind })
}

// covers are the TVDB episodes refs land on, which search reads instead of
// the name since AnimeTosho names follow AniDB's numbering; a film's file
// covers none.
func (p *placer) covers(refs []mapread.Ref) []media.Numbering {
	var out []media.Numbering
	for _, r := range refs {
		for _, n := range animemap.Covers(p.x, r, p.q) {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

// titleCandidates are a title answer's subtitles in an asked language, on
// the files titleFiles keeps.
func titleCandidates(an answer, q *provider.Query) []provider.Candidate {
	keep := titleFiles(fileNames(an.files), q)
	if len(an.files) > 1 && !slices.Contains(keep, true) {
		slog.Debug("animetosho: no file of the entry claims the episode",
			"episode", epmarker.Marker{Season: q.Numbering.Season, Episode: q.Numbering.Episode}, "files", len(an.files))
	}
	var out []provider.Candidate
	for i := range an.files {
		if !keep[i] {
			continue
		}
		for j := range an.files[i].Attachments {
			if c, ok := subtitle(&an.files[i].Attachments[j], an.title, q.Langs); ok {
				c.MatchedBy = rank.Title
				out = append(out, c)
			}
		}
	}
	return out
}

// titleFiles reports which files of a title answer's entry are the query's.
// A single file is kept. Of more, a movie keeps each claiming no episode, a
// one-number form counting as none. An episode keeps those whose one-number
// form is its Absolute when Absolute is above 0 and any file has it, since
// a batch names its files in one numbering; else those whose marker names its
// season and episode, or whose one-number form is its episode. A title
// search knows no season of the entry's own, so no bare leading number is
// read.
func titleFiles(names []string, q *provider.Query) []bool {
	keep := make([]bool, len(names))
	if len(names) == 1 {
		keep[0] = true
		return keep
	}
	claims := animemap.ReadEntry(names, false)
	if q.Kind == media.Movie {
		for i, c := range claims {
			keep[i] = c.Step == animemap.NoClaim || c.Step == animemap.ByAbsolute
		}
		return keep
	}
	numbered := func(c animemap.Claim, n int) bool { return c.Step == animemap.ByAbsolute && c.Episodes[0] == n }
	if q.Absolute > 0 && slices.ContainsFunc(claims, func(c animemap.Claim) bool { return numbered(c, q.Absolute) }) {
		for i, c := range claims {
			keep[i] = numbered(c, q.Absolute)
		}
		return keep
	}
	want := epmarker.Marker{Season: q.Numbering.Season, Episode: q.Numbering.Episode}
	for i, c := range claims {
		keep[i] = slices.Contains(c.Markers, want) || numbered(c, q.Numbering.Episode)
	}
	return keep
}

func fileNames(fs []file) []string {
	out := make([]string, len(fs))
	for i := range fs {
		out[i] = fs[i].Filename
	}
	return out
}

// subtitle is at as a candidate when it is a subtitle on AnimeTosho's
// storage in one of langs. Its language is its ISO 639-2 code, English when
// empty (AnimeTosho's default), refined by its track name; any other
// unreadable code is dropped.
func subtitle(at *attachment, release string, langs []language.Tag) (provider.Candidate, bool) {
	if at.Type != "subtitle" {
		return provider.Candidate{}, false
	}
	if _, ok := storageURL(at.URL); !ok {
		return provider.Candidate{}, false
	}
	tag, ok := language.Parse(at.Info.Lang)
	if !ok {
		return provider.Candidate{}, false
	}
	if tag == (language.Tag{}) {
		tag = english
	}
	refined := language.RefineFromTitle(tag, at.Info.Name)
	if !slices.Contains(langs, tag) && !slices.Contains(langs, refined) {
		return provider.Candidate{}, false
	}
	return provider.Candidate{
		Ref:     provider.NewRef(at.URL),
		Lang:    refined,
		Release: release,
		Flags:   provider.Flags(language.Flags{Forced: at.Info.Forced}, at.Info.Name),
	}, true
}

var english = func() language.Tag {
	t, _ := language.Parse("en")
	return t
}()
