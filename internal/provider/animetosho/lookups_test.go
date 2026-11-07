package animetosho_test

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
)

const series = 100

// listed is a row of AniDB's episode list holding runs.
func listed(aid int, last mapread.Last, runs ...mapread.Run) mapread.Anime {
	return mapread.Anime{AniDBID: aid, Type: "TV", TVDBID: series, Last: last, EpisodesListed: true, Episodes: runs}
}

// unlisted is a row with no AniDB episode list, so its episodes are
// ListingUnknown.
func unlisted(aid int, last mapread.Last, runs ...mapread.Run) mapread.Anime {
	a := listed(aid, last, runs...)
	a.EpisodesListed = false
	return a
}

// one is a run of one episode of kind k, number n, landing on ts; ids holds
// its AniDB episode id when listed.
func one(k mapread.Kind, n int, ids []int, ts ...mapread.Target) mapread.Run {
	return mapread.Run{Kind: k, Start: n, End: n, AniDBEpisodeIDs: ids, TVDB: targets(ts...)}
}

func lookup(t *testing.T, d *mapread.Document, f *tosho, q provider.Query) ([]provider.Candidate, []string) {
	t.Helper()
	r := newRig(t, newGitHub(t, "v2026.11.01", d), f)
	cs := r.search(t, q)
	return cs, r.at.queries()
}

func TestAnimeToshoLookups(t *testing.T) {
	s01e01 := episodeQuery(t, series, 1, 1)
	reg1 := mapread.Last{Regular: 1}
	t.Run("two stated AniDB episodes on one TVDB episode", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			d := doc(listed(1, reg1, one(mapread.Regular, 1, []int{1101}, at(1, 1))), listed(2, reg1, one(mapread.Regular, 1, []int{2101}, at(1, 1))))
			if _, q := lookup(t, d, newTosho(), s01e01); !slices.Equal(q, []string{"eid=1101", "eid=2101"}) {
				t.Errorf("queries %q; want both episodes'", q)
			}
		})
	})
	t.Run("a stated landing drops a default one", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			dflt := mapread.Target{Season: 1, Episode: 1, Default: true}
			d := doc(listed(1, reg1, one(mapread.Regular, 1, []int{1101}, at(1, 1))), listed(2, reg1, one(mapread.Regular, 1, []int{2101}, dflt)))
			if _, q := lookup(t, d, newTosho(), s01e01); !slices.Equal(q, []string{"eid=1101"}) {
				t.Errorf("queries %q; want only the stated one's", q)
			}
			d = doc(listed(2, reg1, one(mapread.Regular, 1, []int{2101}, dflt)))
			if _, q := lookup(t, d, newTosho(), s01e01); !slices.Equal(q, []string{"eid=2101"}) {
				t.Errorf("default alone: queries %q; want it searched", q)
			}
		})
	})
	t.Run("six listed landings", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var anime []mapread.Anime
			var want []string
			for aid := 1; aid <= 6; aid++ {
				anime = append(anime, listed(aid, reg1, one(mapread.Regular, 1, []int{aid*1000 + 1}, at(1, 1))))
				want = append(want, "eid="+itoa(aid*1000+1))
			}
			if _, q := lookup(t, doc(anime...), newTosho(), s01e01); !slices.Equal(q, want) {
				t.Errorf("queries %q; want %q", q, want)
			}
		})
	})
	for _, k := range mapread.Kinds() {
		t.Run("a listed "+string(k)+" landing", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				last := mapread.Last{Regular: 12}
				if k != mapread.Regular {
					last = last.Set(k, 1)
				}
				d := doc(listed(1, last, one(k, 1, []int{1001}, at(0, 7))))
				f := newTosho()
				f.entry("eid=1001", 1, "[Grp] Show - 03", "[Grp] Show - 03.mkv")
				f.entry("eid=1001", 2, "[Grp] Show Extras", "[Grp] Show NCED1.mkv", "[Grp] Show - 03.mkv")
				cs, q := lookup(t, d, f, episodeQuery(t, series, 0, 7))
				want := []string{"[Grp] Show - 03.mkv S00E07"}
				if k == mapread.Credit {
					want = append(want, "[Grp] Show NCED1.mkv offered")
				}
				if !slices.Equal(q, []string{"eid=1001"}) || !slices.Equal(placed(cs), want) {
					t.Errorf("queries %q, candidates %q; want eid=1001 and %q", q, placed(cs), want)
				}
			})
		})
	}
	for _, list := range []bool{true, false} {
		name := map[bool]string{true: "NotListed", false: "ListingUnknown"}[list]
		t.Run(name+" regular and special ride one aid query", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				row := listed
				if !list {
					row = unlisted
				}
				d := doc(row(14444, mapread.Last{Regular: 5, Special: 2},
					one(mapread.Regular, 5, nil, at(3, 17)), one(mapread.Special, 2, nil, at(3, 17))))
				f := newTosho()
				f.entry("aid=14444", 1, "[Grp] Show 2nd Season [1080p]",
					"[Grp] Show 2nd Season - 04 [1080p].mkv", "[Grp] Show 2nd Season - 05 [1080p].mkv", "[Grp] Show 2nd Season - Special 02 [1080p].mkv")
				cs, q := lookup(t, d, f, episodeQuery(t, series, 3, 17))
				want := []string{"[Grp] Show 2nd Season - 05 [1080p].mkv S03E17", "[Grp] Show 2nd Season - Special 02 [1080p].mkv S03E17"}
				if !slices.Equal(q, []string{"aid=14444"}) || !slices.Equal(placed(cs), want) {
					t.Errorf("queries %q, candidates %q; want aid=14444 and %q", q, placed(cs), want)
				}
			})
		})
	}
	t.Run("unlisted credit, promotion, parody and other landings", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			d := doc(listed(7, mapread.Last{Regular: 12, Credit: 3, Promotion: 2, Parody: 1, Other: 1},
				one(mapread.Credit, 1, nil, at(0, 10)), one(mapread.Promotion, 2, nil, at(0, 10)),
				one(mapread.Parody, 1, nil, at(0, 10)), one(mapread.Other, 1, nil, at(0, 10))))
			f := newTosho()
			f.entry("aid=7", 1, "[Grp] Show BD Extras", "[Grp] Show NCED1.mkv", "[Grp] Show PV2.mkv", "[Grp] Show Making Of.mkv", "[Grp] Show - 05.mkv")
			cs, q := lookup(t, d, f, episodeQuery(t, series, 0, 10))
			want := []string{"[Grp] Show Making Of.mkv offered", "[Grp] Show NCED1.mkv offered", "[Grp] Show PV2.mkv offered"}
			if !slices.Equal(q, []string{"aid=7"}) || !slices.Equal(placed(cs), want) {
				t.Errorf("queries %q, candidates %q; want aid=7 and %q", q, placed(cs), want)
			}
		})
	})
	t.Run("an unlisted credit alone offers no unnumbered file", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			d := doc(listed(7, mapread.Last{Regular: 12, Credit: 3}, one(mapread.Credit, 1, nil, at(0, 10))))
			f := newTosho()
			f.entry("aid=7", 1, "[Grp] Show BD Extras", "[Grp] Show NCED1.mkv", "[Grp] Show Making Of.mkv")
			if cs, _ := lookup(t, d, f, episodeQuery(t, series, 0, 10)); !slices.Equal(placed(cs), []string{"[Grp] Show NCED1.mkv offered"}) {
				t.Errorf("candidates %q; want only the credit offered", placed(cs))
			}
		})
	})
	movie := func(tmdb uint64) provider.Query {
		q := episodeQuery(t, 0, 0, 0)
		q.Kind, q.Numbering, q.External = media.Movie, media.Numbering{}, media.ExternalIDs{TMDB: tmdb}
		return q
	}
	film := func(aid, regulars int, ids []int) mapread.Anime {
		return mapread.Anime{
			AniDBID: aid, Type: "MOVIE", Last: mapread.Last{Regular: regulars}, EpisodesListed: ids != nil,
			Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: regulars, AniDBEpisodeIDs: ids, TMDBMovies: []int{500}}},
		}
	}
	t.Run("an unlisted film's file", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newTosho()
			f.entry("aid=30", 1, "[Grp] Show Movie 02 [BD 1080p]", "[Grp] Show Movie 02 [BD 1080p].mkv", "[Grp] Show Movie NCED [BD 1080p].mkv")
			cs, q := lookup(t, doc(film(30, 1, nil)), f, movie(500))
			if !slices.Equal(q, []string{"aid=30"}) || !slices.Equal(placed(cs), []string{"[Grp] Show Movie 02 [BD 1080p].mkv"}) {
				t.Errorf("queries %q, candidates %q; want the film's file placed with no Covers", q, placed(cs))
			}
		})
	})
	t.Run("a listed film's file", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newTosho()
			f.entry("eid=3101", 1, "[Grp] Show Movie [BD]", "[Grp] Show Movie [BD].mkv")
			cs, q := lookup(t, doc(film(31, 1, []int{3101})), f, movie(500))
			if !slices.Equal(q, []string{"eid=3101"}) || !slices.Equal(placed(cs), []string{"[Grp] Show Movie [BD].mkv"}) {
				t.Errorf("queries %q, candidates %q; want the film's file", q, placed(cs))
			}
		})
	})
	t.Run("a movie on a six-episode anime searches by title", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			if _, q := lookup(t, doc(film(40, 6, nil)), newTosho(), movie(500)); !slices.Equal(q, []string{"q=Show"}) {
				t.Errorf("queries %q; want the title", q)
			}
		})
	})
	t.Run("an IMDb-only movie searches by title", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			q := movie(0)
			q.External.IMDb = 1375666
			r := newRig(t, newGitHub(t, "v2026.11.01", doc(film(30, 1, nil))), newTosho())
			r.search(t, q)
			if got := r.at.queries(); !slices.Equal(got, []string{"q=Show"}) || len(r.gh.requests()) != 0 {
				t.Errorf("queries %q, %d GitHub requests; want the title and none", got, len(r.gh.requests()))
			}
		})
	})
	t.Run("an AniDB episode on two TVDB episodes covers both", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newTosho()
			f.entry("eid=5101", 1, "[Grp] Show - 01", "[Grp] Show - 01.mkv")
			d := doc(listed(50, reg1, one(mapread.Regular, 1, []int{5101}, at(1, 1), at(1, 2))))
			if cs, _ := lookup(t, d, f, s01e01); !slices.Equal(placed(cs), []string{"[Grp] Show - 01.mkv S01E01 S01E02"}) {
				t.Errorf("candidates %q; want both episodes covered", placed(cs))
			}
		})
	})
	t.Run("a TMDB-numbered episode searches by title", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			q := s01e01
			q.Numbering.Scheme = media.TMDB
			d := doc(listed(1, reg1, one(mapread.Regular, 1, []int{1101}, at(1, 1))))
			if _, got := lookup(t, d, newTosho(), q); !slices.Equal(got, []string{"q=Show+S01"}) {
				t.Errorf("queries %q; want the title", got)
			}
		})
	})
	t.Run("a marker names TVDB's numbering", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			d := doc(unlisted(1, mapread.Last{Regular: 28}, mapread.Run{Kind: mapread.Regular, Start: 1, End: 28, TVDB: targets(at(1, 1))}))
			f := newTosho()
			f.entry("aid=1", 1, "[Grp] Show S01-S02", "[Grp] Show S01E05.mkv", "[Grp] Show S02E05.mkv", "[Grp] Show - 05.mkv")
			cs, _ := lookup(t, d, f, episodeQuery(t, series, 1, 5))
			want := []string{"[Grp] Show - 05.mkv S01E05", "[Grp] Show S01E05.mkv S01E05"}
			if !slices.Equal(placed(cs), want) {
				t.Errorf("candidates %q; want %q", placed(cs), want)
			}
		})
	})
}
