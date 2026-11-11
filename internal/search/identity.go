package search

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/episodematch"
	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// identity is what one candidate's names say about the instance it is
// offered for.
type identity uint8

const (
	// identityOK: nothing contradicts the instance's media.
	identityOK identity = iota
	// identityContradicted: a title, year or episode the candidate claims
	// is not the instance's; the candidate is dropped.
	identityContradicted
	// identityUnconfirmed: nothing contradicts it, but nothing places it
	// either; it is offered for manual search only.
	identityUnconfirmed
	// identityPartial: it is the instance's media but covers only some of
	// its items; manual search only.
	identityPartial
)

// subject is what identity checks a candidate against: the instance's
// media, its titles, its year and its items' numberings in key order; for
// an episode, its series as 8.11 reads it, with each item's place in
// series.Items.
type subject struct {
	titles  []string
	numbers []media.Numbering
	index   []int
	series  episodematch.Series
	year    int
	kind    media.Kind
}

// identify checks c against s: its title and year when it names them, a
// title-matched movie needing both years, then for an episode the episodes
// it claims: its Covers when an id lookup set them (they place the file,
// whatever its name says), else its release name read by 8.11's steps over
// both numberings and the titles. A candidate its adapter marks
// Unconfirmed is unconfirmed whatever its name. byNumber reports a name
// only its numbers place on the item, on a series whose numbers may follow
// another order.
func identify(s *subject, c *provider.Candidate) (id identity, byNumber bool) {
	if namesOther(s, c) {
		return identityContradicted, false
	}
	if s.kind == media.Movie {
		if c.MatchedBy == rank.Title && (c.Year == 0 || s.year == 0) || c.Unconfirmed {
			return identityUnconfirmed, false
		}
		return identityOK, false
	}
	var fits, covered int
	if c.MatchedBy == rank.ID && len(c.Covers) > 0 {
		covered = coveredBy(s, c.Covers)
		fits = covered
	} else if !otherSeason(s, c.Release) {
		fits, covered, byNumber = s.readName(c)
	}
	switch {
	case fits == 0:
		return identityContradicted, false
	case c.Unconfirmed:
		return identityUnconfirmed, false
	case covered < len(s.numbers):
		return identityPartial, false
	}
	return identityOK, byNumber
}

// readName reads c's release name in c's language and English: how many of
// s's items it does not refute, how many it covers, and whether only its
// numbers place one. On an instance of several items a name covers an item
// only when it is titled for it or states a number.
func (s *subject) readName(c *provider.Candidate) (fits, covered int, byNumber bool) {
	ser := s.seriesIn(c.Lang)
	n := episodematch.ReadName(c.Release)
	_, absolute := epmarker.Absolute(filepath.Base(c.Release))
	states := len(n.Claims) > 0 || absolute
	for _, i := range s.index {
		id := episodematch.Identify(&ser, i, c.Release)
		if id.Refuted {
			continue
		}
		fits++
		if len(s.index) == 1 || id.Titled || states {
			covered++
			byNumber = byNumber || id.ByNumber
		}
	}
	return fits, covered, byNumber
}

// english is the title language every name is also read in.
const english = "eng"

// seriesIn is s's series with the title sets a name in lang is read
// against: lang's and English.
func (s *subject) seriesIn(lang language.Tag) episodematch.Series {
	ser := s.series
	ser.Languages = []string{english}
	if code, ok := language.ISO3(lang); ok && code != english {
		ser.Languages = []string{code, english}
	}
	return ser
}

// namesOther reports whether c's title, its release name standing for a
// title match's missing one, or its year is not s's: a year more than one
// off, both known.
func namesOther(s *subject, c *provider.Candidate) bool {
	switch {
	case c.Title != "":
		if !anyTitleMatches(s.titles, c.Title) {
			return true
		}
	case c.MatchedBy == rank.Title:
		if !anyReleaseNameMatches(s.titles, c.Release) {
			return true
		}
	}
	return c.Year != 0 && s.year != 0 && (c.Year-s.year > 1 || s.year-c.Year > 1)
}

// otherSeason reports whether name, claiming no episode, states a season
// none of s's items is in.
func otherSeason(s *subject, name string) bool {
	if len(epmarker.Claims(name)) > 0 || epmarker.StatesEpisode(name) {
		return false
	}
	season, ok := epmarker.Season(name)
	return ok && !slices.ContainsFunc(s.numbers, func(n media.Numbering) bool { return n.Season == season })
}

// coveredBy is how many of s's items claims hold.
func coveredBy(s *subject, claims []media.Numbering) int {
	n := 0
	for _, it := range s.numbers {
		if slices.ContainsFunc(claims, func(m media.Numbering) bool { return m.Season == it.Season && m.Episode == it.Episode }) {
			n++
		}
	}
	return n
}

// anyTitleMatches reports whether candidate is one of titles after
// normalization.
func anyTitleMatches(titles []string, candidate string) bool {
	return len(titles) == 0 || slices.ContainsFunc(titles, func(t string) bool { return titlesMatch(t, candidate) })
}

// anyReleaseNameMatches reports whether release names one of titles, its
// bracketed group tags dropped.
func anyReleaseNameMatches(titles []string, release string) bool {
	cleaned := normalizeTitle(groupTag.ReplaceAllString(release, ""))
	return len(titles) == 0 || slices.ContainsFunc(titles, func(t string) bool { return releaseNamesTitle(t, release, cleaned) })
}

// releaseNamesTitle reports whether release's title part is title: the
// text before its first episode marker when it has one, else title found
// at a word start of cleaned and not followed by a sequel indicator, so
// "Dragon Ball" never names "Dragon Ball Z".
func releaseNamesTitle(title, release, cleaned string) bool {
	if i := epmarker.FirstIndex(release); i >= 0 {
		return titlesMatch(title, release[:i])
	}
	t := normalizeTitle(title)
	if t == "" || cleaned == "" {
		return true
	}
	i := strings.Index(cleaned, t)
	if i < 0 || i > 0 && cleaned[i-1] != ' ' {
		return false
	}
	rest := cleaned[i+len(t):]
	if rest == "" {
		return true
	}
	if rest[0] != ' ' {
		return false
	}
	next, _, _ := strings.Cut(rest[1:], " ")
	return !sequelWords[next]
}

var groupTag = regexp.MustCompile(`\[[^\]]*\]`)

// sequelWords follow a title in a sequel's or spin-off's name.
var sequelWords = map[string]bool{
	"z": true, "gt": true, "super": true, "kai": true,
	"ii": true, "iii": true, "iv": true, "v": true,
	"zero": true, "next": true, "go": true,
}

// titlesMatch compares two titles after normalization; an empty side
// matches anything.
func titlesMatch(a, b string) bool {
	a, b = normalizeTitle(a), normalizeTitle(b)
	return a == "" || b == "" || a == b
}

var titleSeparators = strings.NewReplacer(".", " ", "-", " ", "_", " ", ":", " ")

// normalizeTitle lowercases s and folds its separators to single spaces.
func normalizeTitle(s string) string {
	return strings.Join(strings.Fields(titleSeparators.Replace(strings.ToLower(s))), " ")
}
