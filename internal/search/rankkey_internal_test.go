package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

// judgedAt is a candidate of provider id at priority, of class c and
// closeness, fitting fit with facts f.
func judgedAt(id provider.ProviderID, priority int, c language.Class, closeness language.Closeness, fit rank.Fit, f rank.Facts) *judged {
	b := &bound{entry: provider.Entry{ID: id}, config: provider.Config{ID: id, Priority: priority}}
	g := language.Grade{Class: c, Closeness: closeness}
	return &judged{
		offer:    &offer{from: b, cand: &provider.Candidate{}, facts: f, fit: fit},
		grade:    g,
		standing: standingOf(g, false, language.Flags{}, language.Standard, fit),
	}
}

func TestRankKeyIgnoresEpisodeByNumber(t *testing.T) {
	a := judgedAt("p", 0, language.Exact, language.Identical, rank.SameRelease, rank.Facts{Group: rank.Same})
	b := judgedAt("p", 0, language.Exact, language.Identical, rank.SameRelease, rank.Facts{Group: rank.Same, EpisodeByNumber: true})
	if c := compareRank(a, b); c != 0 {
		t.Errorf("compareRank = %d, want the two equal: EpisodeByNumber is no rank fact", c)
	}
}

func TestManualOnlyNeverOutranksSubstitute(t *testing.T) {
	manual := judgedAt("a", 0, language.ManualOnly, language.OtherScript, rank.Exact, rank.Facts{Hash: true})
	substitute := judgedAt("b", 100, language.Substitute, language.Intelligible, rank.Other, rank.Facts{Rate: rank.Differs})
	if compareRank(substitute, manual) >= 0 {
		t.Error("a manual-only exact fit ranked before a substitute")
	}
}

func TestRankTieBreaks(t *testing.T) {
	base := func() *judged {
		return judgedAt("b", 10, language.Exact, language.Identical, rank.Other, rank.Facts{})
	}
	cases := []struct {
		better func(*judged)
		name   string
	}{
		{func(j *judged) { j.facts.Rate = rank.Same }, "a rate not differing over one differing"},
		{func(j *judged) { j.facts.Service = rank.Same }, "the same service"},
		{func(j *judged) { j.facts.Cut = rank.Same }, "the same cut"},
		{func(j *judged) { j.facts.SeasonPack = false }, "one episode over a pack"},
		{func(j *judged) { j.from = &bound{entry: provider.Entry{ID: "b"}, config: provider.Config{Priority: 5}} }, "a lower priority"},
		{func(j *judged) {
			j.from = &bound{entry: provider.Entry{ID: "a"}, config: provider.Config{Priority: 10}}
		}, "the lower id"},
		{func(j *judged) { j.order = 0 }, "the provider's own order"},
	}
	for _, c := range cases {
		worse, better := base(), base()
		worse.facts.Rate, better.facts.Rate = rank.Differs, rank.Differs
		worse.facts.SeasonPack, better.facts.SeasonPack = true, true
		worse.order, better.order = 1, 1
		c.better(better)
		if compareRank(better, worse) >= 0 || compareRank(worse, better) <= 0 {
			t.Errorf("%s did not rank first", c.name)
		}
		better.standing.fit, worse.standing.fit = rank.Other, rank.SameSource
		if compareRank(better, worse) <= 0 {
			t.Errorf("%s moved a candidate past a better fit", c.name)
		}
	}
}

func TestStandingOrder(t *testing.T) {
	g := language.Grade{Class: language.Exact, Closeness: language.Identical}
	top := standingOf(g, false, language.Flags{}, language.Standard, rank.Other)
	cases := []struct {
		s    standing
		name string
	}{
		{standingOf(language.Grade{Class: language.Substitute, Closeness: language.Identical}, false, language.Flags{}, language.Standard, rank.Exact), "a worse class"},
		{standingOf(g, true, language.Flags{}, language.Standard, rank.Exact), "a machine translation"},
		{standingOf(language.Grade{Class: language.Exact, Closeness: language.SameLanguage}, false, language.Flags{}, language.Standard, rank.Exact), "a farther closeness"},
		{standingOf(g, false, language.Flags{HI: true, Forced: true}, language.Forced, rank.Exact), "flags beyond the variant's"},
	}
	for _, c := range cases {
		if compareStanding(top, c.s) >= 0 {
			t.Errorf("%s ranked with or before the human, identical, own-flag file of a lower fit", c.name)
		}
	}
	forced := standingOf(g, false, language.Flags{Forced: true}, language.Forced, rank.Other)
	if compareStanding(forced, standingOf(g, false, language.Flags{Forced: true}, language.Forced, rank.SameSource)) <= 0 {
		t.Error("a lower fit ranked before a higher one of equal standing")
	}
}
