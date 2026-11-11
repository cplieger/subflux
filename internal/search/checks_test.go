package search_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

func TestIdentityReadsIDCovers(t *testing.T) {
	anime := mediasource.Root{Native: mediasource.NativeRef{ID: "8", Kind: media.Episode}, Title: "Anime", External: media.ExternalIDs{TVDB: 267440}}
	item := mediasource.Item{
		Native: "s3e17", VideoPath: "Anime/Anime.S03E17.1080p.WEB-DL.x264-GRP.mkv", SceneName: "Anime.S03E17.1080p.WEB-DL.x264-GRP",
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 3, Episode: 17},
	}
	name := "[Group] Anime S2 - 05 [1080p]"
	cases := []struct {
		name   string
		covers []media.Numbering
		ranked bool
	}{
		{"the id lookup's Covers place it", []media.Numbering{{Scheme: media.TVDB, Season: 3, Episode: 17}}, true},
		{"an id match with no Covers is read by its name", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.provider("p", "en").answer(nil, provider.Candidate{
					Ref: provider.NewRef("at"), Lang: mustTag("en"), Release: name, MatchedBy: rank.ID, Covers: c.covers,
				})
				got := pickOf(t, r.pick(r.seed(anime, nil, item)[0], statedb.Scan), "en", language.Standard)
				if (len(got.Ranked) == 1) != c.ranked {
					t.Errorf("ranked %v, want ranked %v", releases(got), c.ranked)
				}
			})
		})
	}
}

func TestUnconfirmedManualOnly(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", func(c *provider.Candidate) {
			c.Release, c.Unconfirmed = "[Group] Show NCOP [1080p]", true
		}))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "en", language.Standard)
		if len(got.Ranked) != 0 || got.Skip != search.IdentityUnconfirmed {
			t.Errorf("an unconfirmed candidate: ranked %v, skip %d; want manual only, identity unconfirmed", releases(got), got.Skip)
		}
	})
}

func TestMachineTranslatedRanksAfterHuman(t *testing.T) {
	inRig(t, "close_varieties: true\ndefault: [{tag: pt-BR}]", func(t *testing.T, r *rig) {
		hashed := func(c *provider.Candidate) { c.MatchedBy = rank.Hash }
		machine := func(c *provider.Candidate) { c.MachineTranslated = true }
		r.provider("p", "pt-BR", "pt-PT").answer(nil,
			cand(1, "pt-BR", hashed, machine, withRef("machine exact"), func(c *provider.Candidate) { c.Release = "Show.S01E01.MT" }),
			cand(1, "pt-BR", otherFit, withRef("human other")),
			cand(1, "pt-PT", sameRelease, withRef("human substitute")))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "pt-BR", language.Standard)
		want := []string{"Show.S01E01.720p.HDTV.x264-OTHER", "Show.S01E01.MT", stem(1)}
		if !slices.Equal(releases(got), want) {
			t.Errorf("ranked %v, want %v: a human file first within its class, a machine Exact before a human Substitute", releases(got), want)
		}
		if got.Ranked[1].Fit != rank.Exact {
			t.Errorf("the hash-matched machine translation fits %d, want exact", got.Ranked[1].Fit)
		}
	})
}

func TestHIOnlyNeverServesStandard(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", func(c *provider.Candidate) { c.Flags.HI = true }))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "en", language.Standard)
		if len(got.Ranked) != 0 || got.Skip != search.NoCandidate {
			t.Errorf("an HI-only answer for a Standard pair: ranked %v, skip %d; want none, no candidate", releases(got), got.Skip)
		}
	})
}

func TestVariantOwnFlagsFirst(t *testing.T) {
	inRig(t, "default: [{tag: en, variants: [forced]}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil,
			cand(1, "en", withRef("hi forced"), func(c *provider.Candidate) {
				c.Flags = language.Flags{HI: true, Forced: true}
				c.Release = stem(1) + ".hi"
			}),
			cand(1, "en", withRef("forced"), func(c *provider.Candidate) { c.Flags.Forced = true; c.Release = "Show.S01E01.720p.HDTV.x264-OTHER" }))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "en", language.Forced)
		if want := []string{"Show.S01E01.720p.HDTV.x264-OTHER", stem(1) + ".hi"}; !slices.Equal(releases(got), want) {
			t.Errorf("ranked %v, want the forced-only file before the HI forced one, whatever its fit", releases(got))
		}
	})
}

func TestCompareFeedsPlan(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", func(c *provider.Candidate) { c.Release = "Show.S01.1080p.WEB-DL.x264-OTHER" }))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "en", language.Standard)
		if len(got.Ranked) != 1 {
			t.Fatalf("ranked %v, want the season pack", releases(got))
		}
		f := got.Ranked[0].Facts
		if !f.SeasonPack || f.Group != rank.Differs {
			t.Errorf("facts %+v, want a season pack of another group", f)
		}
		if d := timing.Plan(f, language.Standard, timing.Config{Sync: true}); d.Action == timing.Skip {
			t.Errorf("Plan over the pack's facts = %+v, want its timing checked, never skipped as the same release", d)
		}
	})
}

func TestEpisodeEditionNeverOutranksHash(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil,
			cand(1, "en", sameRelease, withRef("name")),
			cand(1, "en", withRef("hash"), func(c *provider.Candidate) { c.MatchedBy, c.Release = rank.Hash, "Show.S01E01" }))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "en", language.Standard)
		if len(got.Ranked) != 2 || got.Ranked[0].Release != "Show.S01E01" || got.Ranked[0].Fit != rank.Exact {
			t.Errorf("ranked %+v, want the hash match first, exact", got.Ranked)
		}
	})
}

func TestTitleMatchNeedsYear(t *testing.T) {
	cases := []struct {
		name   string
		skip   search.WhyNot
		year   int
		ranked bool
	}{
		{"the remake's year", search.NoCandidate, 1984, false},
		{"no year", search.IdentityUnconfirmed, 0, false},
		{"its year", search.None, 2021, true},
		{"a year off", search.None, 2022, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.provider("p", "en").answer(nil, movieCand("en", func(m *provider.Candidate) {
					m.MatchedBy, m.Title, m.Year = rank.Title, "Dune", c.year
				}))
				got := pickOf(t, r.pick(r.seed(film, nil, movie())[0], statedb.Scan), "en", language.Standard)
				if (len(got.Ranked) == 1) != c.ranked || got.Skip != c.skip {
					t.Errorf("a title match of %d: ranked %v, skip %d; want ranked %v, skip %d", c.year, releases(got), got.Skip, c.ranked, c.skip)
				}
			})
		})
	}
}

func TestCloserTierFirst(t *testing.T) {
	inRig(t, "close_varieties: true\ndefault: [{tag: pt-BR}]", func(t *testing.T, r *rig) {
		r.provider("p", "pt-PT", "pt").answer(nil, cand(1, "pt-PT", sameRelease), cand(1, "pt", otherFit))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "pt-BR", language.Standard)
		if want := []string{"Show.S01E01.720p.HDTV.x264-OTHER", stem(1)}; !slices.Equal(releases(got), want) {
			t.Errorf("ranked %v, want the closer pt before pt-PT, whatever the fit", releases(got))
		}
	})
}

func TestConflictNeverAutomatic(t *testing.T) {
	cases := []struct {
		video, sub string
		skip       search.WhyNot
	}{
		{"Dune.2021.1080p.WEB-DL.x264-GRP", "Dune.2021.HDCAM.x264-GRP", search.PreRelease},
		{"Dune.2021.Extended.1080p.WEB-DL.x264-GRP", "Dune.2021.Theatrical.1080p.WEB-DL.x264-GRP", search.DifferentCut},
	}
	for _, c := range cases {
		t.Run(c.sub, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.provider("p", "en").answer(nil, movieCand("en", func(m *provider.Candidate) { m.Release = c.sub }))
				item := movie()
				item.SceneName = c.video
				got := pickOf(t, r.pick(r.seed(film, nil, item)[0], statedb.Scan), "en", language.Standard)
				if len(got.Ranked) != 0 || got.Skip != c.skip {
					t.Errorf("only a conflict: ranked %v, skip %d; want none, skip %d", releases(got), got.Skip, c.skip)
				}
			})
		})
	}
}

func TestLanguageNotAskedDropped(t *testing.T) {
	inRig(t, "default: [{tag: pt-BR}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "pt-BR", "pt-PT").answer(nil, cand(1, "pt-PT", sameRelease))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "pt-BR", language.Standard)
		if q := p.lastQuery().Langs; !slices.Equal(q, []language.Tag{mustTag("pt-BR")}) {
			t.Errorf("asked %v, want only pt-BR with close varieties off", q)
		}
		if got.Skip != search.NoCandidate {
			t.Errorf("a pt-PT candidate never asked for: skip %d, want it dropped (no candidate)", got.Skip)
		}
	})
	inRig(t, "default: [{tag: pt-BR}, {tag: pt-PT}]", func(t *testing.T, r *rig) {
		r.provider("p", "pt-BR", "pt-PT").answer(nil, cand(1, "pt-PT", sameRelease))
		got := r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan)
		if br := pickOf(t, got, "pt-BR", language.Standard); br.Skip != search.LanguageManualOnly {
			t.Errorf("a pt-PT candidate asked for the pt-PT target, judged for pt-BR: skip %d, want language manual only", br.Skip)
		}
		if pt := pickOf(t, got, "pt-PT", language.Standard); len(pt.Ranked) != 1 {
			t.Errorf("the pt-PT pair ranked %v, want the candidate", releases(pt))
		}
	})
}

func TestPartialCoverageManualOnly(t *testing.T) {
	for _, c := range []struct {
		release string
		skip    search.WhyNot
		ranked  bool
	}{
		{"Show.S01E01.1080p.WEB-DL.x264-GRP", search.PartialCoverage, false},
		{"Show.S01E01E02.1080p.WEB-DL.x264-GRP", search.None, true},
	} {
		t.Run(c.release, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.provider("p", "en").answer(nil, cand(1, "en", func(m *provider.Candidate) { m.Release = c.release }))
				one, two := episode(1), episode(2)
				two.VideoPath = one.VideoPath
				got := pickOf(t, r.pick(r.seed(show, nil, one, two)[0], statedb.Scan), "en", language.Standard)
				if (len(got.Ranked) == 1) != c.ranked || got.Skip != c.skip {
					t.Errorf("ranked %v, skip %d; want ranked %v, skip %d", releases(got), got.Skip, c.ranked, c.skip)
				}
			})
		})
	}
}

func TestEpisodeContradictionDropped(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil,
			cand(2, "en"),
			cand(1, "en", withRef("season 2"), func(c *provider.Candidate) { c.Release = "Show.S02.1080p.WEB-DL.x264-GRP" }),
			cand(1, "en", withRef("sequel"), func(c *provider.Candidate) {
				c.MatchedBy, c.Release = rank.Title, "Show.Z.S01E01.1080p.WEB-DL.x264-GRP"
			}))
		got := pickOf(t, r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan), "en", language.Standard)
		if len(got.Ranked) != 0 || got.Skip != search.NoCandidate {
			t.Errorf("another episode, another season's pack and a sequel's name: ranked %v, skip %d; want every one dropped", releases(got), got.Skip)
		}
	})
}

func TestMinFitByTarget(t *testing.T) {
	inRig(t, "default: [{tag: en}, {tag: fr}]", func(t *testing.T, r *rig) {
		r.cfg.TargetMinFit = map[language.Tag]rank.Fit{mustTag("en"): rank.SameRelease}
		r.provider("p", "en", "fr").answer(nil, cand(1, "en", sameSource), cand(1, "fr", sameSource))
		got := r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan)
		if en := pickOf(t, got, "en", language.Standard); en.Skip != search.BelowMinFit {
			t.Errorf("en, minimum same_release: skip %d, want below min fit", en.Skip)
		}
		if fr := pickOf(t, got, "fr", language.Standard); len(fr.Ranked) != 1 {
			t.Errorf("fr, at the section's minimum: ranked %v, want the candidate", releases(fr))
		}
	})
}
