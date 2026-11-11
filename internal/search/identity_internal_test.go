package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// episodes is a subject of the given items of a series no reading knows,
// whose own order is TVDB's.
func episodes(titles []string, year int, numbers ...media.Numbering) *subject {
	s := &subject{titles: titles, year: year, kind: media.Episode, numbers: numbers}
	for i, n := range numbers {
		s.series.Items = append(s.series.Items, mediasource.Item{Numbering: n, SeriesNumbering: n})
		s.index = append(s.index, i)
	}
	s.series.Order = mediasource.Order{Aired: true}
	return s
}

func TestIdentify(t *testing.T) {
	ep := episodes([]string{"Show", "The Show"}, 2020, media.Numbering{Season: 1, Episode: 1})
	double := episodes([]string{"Show"}, 0, media.Numbering{Season: 1, Episode: 1}, media.Numbering{Season: 1, Episode: 2})
	film := &subject{titles: []string{"Dune"}, year: 2021, kind: media.Movie}
	cases := []struct {
		s    *subject
		c    provider.Candidate
		name string
		want identity
	}{
		{ep, provider.Candidate{Release: "Show.S01E01.1080p", MatchedBy: rank.ID}, "its episode", identityOK},
		{ep, provider.Candidate{Release: "Show.S01E02.1080p", MatchedBy: rank.ID}, "another episode", identityContradicted},
		{ep, provider.Candidate{Release: "Show.S01.1080p", MatchedBy: rank.ID}, "its season's pack", identityOK},
		{ep, provider.Candidate{Release: "Show.S02.1080p", MatchedBy: rank.ID}, "another season's pack", identityContradicted},
		{ep, provider.Candidate{Release: "Show 1080p", MatchedBy: rank.ID}, "a name stating nothing", identityOK},
		{ep, provider.Candidate{Release: "The.Show.S01E01", MatchedBy: rank.Title}, "an alternative title in the name", identityOK},
		{ep, provider.Candidate{Release: "Show.Z.S01E01", MatchedBy: rank.Title}, "a sequel's name", identityContradicted},
		{ep, provider.Candidate{Release: "Shows.S01E01", MatchedBy: rank.Title}, "a longer word", identityContradicted},
		{ep, provider.Candidate{Release: "x", Title: "Other Show", MatchedBy: rank.Hash}, "a hash match naming another title", identityContradicted},
		{ep, provider.Candidate{Release: "x", Title: "the.show", MatchedBy: rank.Title}, "a title matching after normalization", identityOK},
		{ep, provider.Candidate{Release: "x", Year: 2018, MatchedBy: rank.ID}, "a year two off", identityContradicted},
		{ep, provider.Candidate{Release: "[G] Show - 05", MatchedBy: rank.ID, Covers: []media.Numbering{{Season: 1, Episode: 1}}}, "id Covers over its name", identityOK},
		{ep, provider.Candidate{Release: "Show.S01E01", MatchedBy: rank.ID, Covers: []media.Numbering{{Season: 2, Episode: 1}}}, "id Covers elsewhere", identityContradicted},
		{ep, provider.Candidate{Release: "Show.S01E02", MatchedBy: rank.Title, Covers: []media.Numbering{{Season: 1, Episode: 1}}}, "title-matched Covers are not read", identityContradicted},
		{ep, provider.Candidate{Release: "Show NCOP", MatchedBy: rank.ID, Unconfirmed: true}, "unconfirmed", identityUnconfirmed},
		{ep, provider.Candidate{Release: "Show.S01E02", MatchedBy: rank.ID, Unconfirmed: true}, "unconfirmed and contradicted", identityContradicted},
		{double, provider.Candidate{Release: "Show.S01E01E02", MatchedBy: rank.ID}, "both items", identityOK},
		{double, provider.Candidate{Release: "Show.S01E02", MatchedBy: rank.ID}, "one of two items", identityPartial},
		{double, provider.Candidate{Release: "Show.S01", MatchedBy: rank.ID}, "a pack for two items", identityPartial},
		{film, provider.Candidate{Release: "Dune.2021", Title: "Dune", Year: 2021, MatchedBy: rank.Title}, "a title match with its year", identityOK},
		{film, provider.Candidate{Release: "Dune.1984", Title: "Dune", Year: 1984, MatchedBy: rank.Title}, "a remake", identityContradicted},
		{film, provider.Candidate{Release: "Dune", Title: "Dune", MatchedBy: rank.Title}, "a title match with no year", identityUnconfirmed},
		{film, provider.Candidate{Release: "Dune", MatchedBy: rank.Hash}, "a hash match with no year", identityOK},
		{&subject{titles: []string{"Dune"}, kind: media.Movie}, provider.Candidate{Release: "Dune", Title: "Dune", Year: 2021, MatchedBy: rank.Title}, "a title match the root has no year for", identityUnconfirmed},
	}
	for _, c := range cases {
		if got, _ := identify(c.s, &c.c); got != c.want {
			t.Errorf("%s: identify = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestReleaseNamesTitle(t *testing.T) {
	cases := []struct {
		title, release string
		want           bool
	}{
		{"Dragon Ball", "[Group] Dragon Ball - 05 [1080p]", true},
		{"Dragon Ball", "[Group] Dragon Ball Z - 05 [1080p]", false},
		{"Dragon Ball", "Dragon.Ball.GT.1080p", false},
		{"Dragon Ball", "Dragon.Ball.1080p", true},
		{"Dragon Ball", "Dragon.Ballz.1080p", false},
		{"Ball", "Dragon.Ball.1080p", true},
		{"Ball", "Dragonball.1080p", false},
		{"", "Anything", true},
	}
	for _, c := range cases {
		if got := anyReleaseNameMatches([]string{c.title}, c.release); got != c.want {
			t.Errorf("%q names %q = %v, want %v", c.release, c.title, got, c.want)
		}
	}
}
