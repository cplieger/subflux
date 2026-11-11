package search_test

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/release"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/searchtest"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

// fireflySeason is Firefly's first season in aired order (TVDB 78874): each
// episode's title and its DVD number.
var fireflySeason = []struct {
	title string
	disc  int
}{
	{"The Train Job", 2},
	{"Bushwhacked", 3},
	{"Our Mrs. Reynolds", 6},
	{"Jaynestown", 7},
	{"Out of Gas", 8},
	{"Shindig", 4},
	{"Safe", 5},
	{"Ariel", 9},
	{"War Stories", 10},
	{"Objects in Space", 14},
	{"Serenity", 1},
	{"Heart of Gold", 13},
	{"Trash", 11},
	{"The Message", 12},
}

// fireflySerenity is "Serenity"'s aired number.
const fireflySerenity = 11

// series is one rig root's episodes: each one's aired number, its number
// in the series' own order and its title.
type series struct {
	root   mediasource.Root
	name   string
	titles []string
	own    []int
	other  []int
	season int
}

// firefly14 is Firefly's season, its own order the DVD one when dvd is set,
// the other order the DVD one otherwise.
func firefly14(dvd bool) series {
	s := series{root: firefly, name: "Firefly", season: 1}
	if !dvd {
		s.root.Order = mediasource.Order{Type: "official", Name: "Aired", Aired: true}
	}
	for i, e := range fireflySeason {
		s.titles = append(s.titles, e.title)
		if dvd {
			s.own = append(s.own, e.disc)
		} else {
			s.own = append(s.own, i+1)
		}
		s.other = append(s.other, e.disc)
	}
	return s
}

// seedSeries seeds every episode of s, each at a video of its own number,
// with the reading SkyHook gives: the aired list titled in English and the
// DVD order s.other names (none when nil), and returns the instance of the
// episode aired as aired.
func (r *rig) seedSeries(s *series, aired int) statedb.InstanceID {
	r.t.Helper()
	var items []mediasource.Item
	var disc []media.Numbering
	for i, title := range s.titles {
		own := s.own[i]
		name := fmt.Sprintf("%s.S%02dE%02d.1080p.WEB-DL.DDP5.1.H.264-FLUX", s.name, s.season, own)
		items = append(items, mediasource.Item{
			Native: mediasource.NativeID(fmt.Sprintf("e%02d", i+1)), Title: title,
			VideoPath: filepath.Join(s.name, name+".mkv"), SceneName: name,
			Numbering:       media.Numbering{Scheme: media.TVDB, Season: s.season, Episode: i + 1},
			SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: s.season, Episode: own},
		})
		if s.other != nil {
			disc = append(disc, media.Numbering{Season: s.season, Episode: s.other[i]})
		}
	}
	orders := map[string][]media.Numbering{}
	if disc != nil {
		orders["dvd"] = disc
	}
	ev := airedReading(items, orders)
	r.readings[evidenceKey{src: "sonarr", root: s.root.Native}] = &ev
	return r.seed(s.root, textTrack, items...)[aired-1]
}

// packCand is a season pack candidate named release, matched by id.
func packCand(release string) provider.Candidate {
	return provider.Candidate{Ref: provider.NewRef(release), Lang: mustTag("en"), Release: release, MatchedBy: rank.ID}
}

// confident is an Align answer verified by the reference at confidence c.
func confident(t *testing.T, c float64) searchtest.Result {
	t.Helper()
	res := alignedAs(t, timing.Verified)
	res.Aligned.Outcome.Confidence = c
	return res
}

// noReference is an Align answer finding no reference track.
var noReference = searchtest.Result{Aligned: syncsched.Aligned{Outcome: timing.Outcome{Method: timing.MethodNone, Verdict: timing.Unverified}}}

// alignedMembers are the pack members al was asked to align, by the name
// each member's text carries.
func alignedMembers(al *searchtest.Aligner, names []string) []string {
	var out []string
	for _, p := range al.Payloads() {
		for _, n := range names {
			if bytes.Contains(p, []byte("\n"+n+"\n")) {
				out = append(out, n)
			}
		}
	}
	return out
}

// numberedPack names members prefix S<season>E01 to E<n> then suffix.
func numberedPack(prefix string, season, n int, suffix string) []string {
	out := make([]string, n)
	for e := range n {
		out[e] = fmt.Sprintf("%sS%02dE%02d%s", prefix, season, e+1, suffix)
	}
	return out
}

func TestSelectDVDPackForAiredItem(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		var pack []string
		for _, e := range fireflySeason {
			pack = append(pack, fmt.Sprintf("Firefly [S01 E%02d] - %s.eng.srt", e.disc, e.title))
		}
		slices.Sort(pack)
		const rel = "Firefly.S01.DVDRip.x264-SUBSOURCE"
		r.provider("p", "en").answer(nil, packCand(rel)).serve(rel, nil, zipOf(t, pack...))
		s := firefly14(false)
		inst := r.seedSeries(&s, fireflySerenity)
		rec := r.script(alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := alignedMembers(rec, pack); !slices.Equal(got, []string{"Firefly [S01 E01] - Serenity.eng.srt"}) {
			t.Errorf("aligned %q, want the DVD pack's Serenity for aired S01E11", got)
		}
		if subs := r.subtitles(inst); len(subs) != 1 {
			t.Errorf("subtitles %q, want Serenity delivered", subs)
		}
	})
}

func TestSelectAiredPackForDVDItem(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		var pack []string
		for i, e := range fireflySeason {
			pack = append(pack, fmt.Sprintf("Firefly.S01E%02d.%s.1080p.WEB-DL.DDP5.1.H.264-FLUX.srt", i+1, strings.ReplaceAll(e.title, " ", ".")))
		}
		const rel = "Firefly.S01.1080p.WEB-DL.DDP5.1.H.264-FLUX"
		r.provider("p", "en").answer(nil, packCand(rel)).serve(rel, nil, zipOf(t, pack...))
		s := firefly14(true)
		inst := r.seedSeries(&s, fireflySerenity)
		rec := r.script(alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := alignedMembers(rec, pack); !slices.Equal(got, []string{pack[fireflySerenity-1]}) {
			t.Errorf("aligned %q, want the aired pack's S01E11 for DVD S01E01", got)
		}
	})
}

// fireflyUntitled is Firefly's untitled 14-member pack in its numbers.
var fireflyUntitled = numberedPack("Firefly.", 1, 14, ".720p.HDTV.x264.srt")

func TestUntitledPackOrdersDisagree(t *testing.T) {
	greek := []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta"}
	sixDVD := series{
		root: mediasource.Root{
			Native: mediasource.NativeRef{ID: "74", Kind: media.Episode}, Title: "Six",
			External: media.ExternalIDs{TVDB: 74}, Year: 2020, Order: mediasource.Order{Type: "dvd", Name: "DVD Order"},
		},
		name: "Six", season: 1, titles: greek, own: []int{2, 3, 1, 4, 5, 6}, other: []int{2, 3, 1, 4, 5, 6},
	}
	sixPack := numberedPack("Six.", 1, 6, ".720p.HDTV.x264.srt")
	sixPack[3], sixPack[4] = "Six.S01E04.Delta.720p.HDTV.x264.srt", "Six.S01E05.Epsilon.720p.HDTV.x264.srt"
	cases := []struct {
		series  series
		name    string
		pack    []string
		dvd     string // the member the series' own order names
		aired   int
		release string
	}{
		{
			name: "an untitled pack", series: firefly14(true), pack: fireflyUntitled, aired: fireflySerenity,
			dvd: fireflyUntitled[0], release: "Firefly.S01.1080p.WEB-DL.DDP5.1.H.264-FLUX",
		},
		{
			name: "titled members leaving both orders compatible", series: sixDVD, pack: sixPack, aired: 3,
			dvd: sixPack[0], release: "Six.S01.1080p.WEB-DL.DDP5.1.H.264-FLUX",
		},
	}
	for _, c := range cases {
		airedMember := c.pack[c.aired-1]
		single := fmt.Sprintf("%s.S01E%02d.720p.HDTV.x264-OTHER", c.series.name, c.series.own[c.aired-1])
		t.Run(c.name+", aligned", func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.provider("p", "en").answer(nil, packCand(c.release)).serve(c.release, nil, zipOf(t, c.pack...))
				inst := r.seedSeries(&c.series, c.aired)
				rec := r.script()
				rec.Member("\n"+c.dvd+"\n", confident(t, 0.70)).Member("\n"+airedMember+"\n", confident(t, 0.33))
				r.mustRun(inst, statedb.Scan)
				if got := alignedMembers(rec, c.pack); !slices.Equal(got, []string{c.dvd, airedMember}) {
					t.Errorf("aligned %q, want both orders' members", got)
				}
				for _, d := range rec.Decisions() {
					if d != (timing.Decision{Action: timing.ActionReference}) {
						t.Errorf("compared with %+v, want the reference", d)
					}
				}
				if got := r.record(inst).Provenance.Timing.Confidence; got != 0.70 {
					t.Errorf("delivered at confidence %v, want the 0.70 member", got)
				}
			})
		})
		t.Run(c.name+", no reference", func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				p := r.provider("p", "en").answer(nil, packCand(c.release), packCand(single))
				p.serve(c.release, nil, zipOf(t, c.pack...)).serve(single, nil, srtBytes)
				inst := r.seedSeries(&c.series, c.aired)
				rec := r.script(alignedAs(t, timing.Verified))
				rec.Member("\n"+c.dvd+"\n", noReference)
				r.mustRun(inst, statedb.Scan)
				key := keyOf(t, c.series.root, mediasource.Item{Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: c.aired}})
				hist := r.history(key)
				if got := rowOf(t, hist, "p", c.release); got.Refusal != search.RefusalOrderUndetermined {
					t.Errorf("pack row %q, want order_undetermined", got.Refusal)
				}
				if got := rowOf(t, hist, "p", single); !got.Delivered {
					t.Errorf("the next candidate's row %+v, want it delivered", got)
				}
				got := r.candidates(r.engine(), inst, "en")
				i := slices.IndexFunc(got.Candidates, func(a search.Annotated) bool { return a.Release == c.release })
				if i < 0 || got.Candidates[i].WhyNot != search.OrderUndetermined || got.Candidates[i].Automatic {
					t.Errorf("candidates %+v, want the pack listed with OrderUndetermined", got.Candidates)
				}
			})
		})
	}
}

// futurama is Futurama's root, its second season (TVDB 73871) aired:
var futurama = mediasource.Root{
	Native: mediasource.NativeRef{ID: "73871", Kind: media.Episode}, Title: "Futurama",
	External: media.ExternalIDs{TVDB: 73871}, Year: 1999, Order: mediasource.Order{Type: "official", Name: "Aired", Aired: true},
}

var futuramaSeason2 = []string{
	"A Flight to Remember", "Mars University", "When Aliens Attack", "Fry & the Slurm Factory", "I Second That Emotion",
	"Brannigan, Begin Again", "A Head in the Polls", "Xmas Story", "Why Must I Be a Crustacean in Love",
	"Put Your Head on My Shoulder", "Lesser of Two Evils", "Raging Bender", "A Bicyclops Built for Two",
	"How Hermes Requisitioned His Groove Back", "A Clone of My Own", "The Deep South", "Bender Gets Made",
	"The Problem With Popplers", "Mother's Day", "Anthology of Interest I",
}

// seedFuturama seeds Futurama's 20 season-2 episodes with no reading, so the
// series' own order and its titles are the only evidence, and returns the
// instance of S02E<n>.
func (r *rig) seedFuturama(n int) statedb.InstanceID {
	r.t.Helper()
	s := series{root: futurama, name: "Futurama", season: 2, titles: futuramaSeason2}
	for i := range futuramaSeason2 {
		s.own = append(s.own, i+1)
	}
	inst := r.seedSeries(&s, n)
	r.readings[evidenceKey{src: "sonarr", root: futurama.Native}] = nil
	return inst
}

// addic7ed is Addic7ed's Futurama season 2: 19 members against TVDB's 20.
func addic7ed() []string {
	out := make([]string, 19)
	for e := range out {
		out[e] = fmt.Sprintf("Futurama - 2x%02d.srt", e+1)
	}
	return out
}

func TestPackCountRefused(t *testing.T) {
	const rel, single = "Futurama.S02.1080p.WEB-DL.DDP5.1.H.264-FLUX", "Futurama.S02E05.720p.HDTV.x264-OTHER"
	titled := addic7ed()
	titled[4] = "Futurama - 2x05 - I Second That Emotion.srt"
	for _, c := range []struct {
		name      string
		pack      []string
		refusal   string
		delivered string
	}{
		{name: "an untitled pack short of the season", pack: addic7ed(), refusal: search.RefusalEpisodeCount, delivered: single},
		{name: "a member titled for the item", pack: titled, delivered: rel},
	} {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				p := r.provider("p", "en").answer(nil, packCand(rel), packCand(single))
				p.serve(rel, nil, zipOf(t, c.pack...)).serve(single, nil, srtBytes)
				inst := r.seedFuturama(5)
				rec := r.script(alignedAs(t, timing.Verified))
				r.mustRun(inst, statedb.Scan)
				hist := r.history(keyOf(t, futurama, mediasource.Item{Numbering: media.Numbering{Scheme: media.TVDB, Season: 2, Episode: 5}}))
				if got := rowOf(t, hist, "p", rel); got.Refusal != c.refusal {
					t.Errorf("pack row %q, want %q", got.Refusal, c.refusal)
				}
				if got := rowOf(t, hist, "p", c.delivered); !got.Delivered {
					t.Errorf("row of %s %+v, want it delivered", c.delivered, got)
				}
				if c.refusal == "" {
					if got := alignedMembers(rec, c.pack); !slices.Equal(got, []string{titled[4]}) {
						t.Errorf("aligned %q, want the titled member", got)
					}
				}
			})
		})
	}
}

func TestNumberChosenNeverSkipsSameRelease(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.timing = timing.Config{Sync: true}
		const rel = "Show.S01.1080p.WEB-DL.x264-GRP"
		pack := numberedPack("Show.", 1, 3, ".1080p.WEB-DL.x264-GRP.srt")
		r.provider("p", "en").answer(nil, packCand(rel)).serve(rel, nil, zipOf(t, pack...))
		inst := r.seed(show, textTrack, episode(1), episode(2), episode(3))[1]
		rec := r.script(alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := alignedMembers(rec, pack); !slices.Equal(got, []string{pack[1]}) {
			t.Fatalf("aligned %q, want S01E02", got)
		}
		if got := rec.Decisions(); got[0] != (timing.Decision{Action: timing.ActionReference}) {
			t.Errorf("aligned with %+v, want the reference a member chosen by number is owed", got[0])
		}
		if got := r.record(inst).Provenance.Facts; !got.EpisodeByNumber {
			t.Errorf("delivered facts %+v, want EpisodeByNumber", got)
		}
	})
}

func TestNumberChosenRefusalIsDifferentEpisode(t *testing.T) {
	t.Run("a member chosen by number", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p := r.provider("p", "en")
			var cands []provider.Candidate
			for _, rel := range []string{"Show.S01.720p.HDTV.x264-A", "Show.S01.720p.HDTV.x264-B"} {
				cands = append(cands, packCand(rel))
				p.serve(rel, nil, zipOf(t, numberedPack("Show.", 1, 3, ".720p.HDTV.x264-"+rel[len(rel)-1:]+".srt")...))
			}
			p.answer(nil, cands...)
			inst := r.seed(show, nil, episode(1), episode(2), episode(3))[1]
			r.script(refusedCut, refusedCut)
			r.mustRun(inst, statedb.Scan)
			k := keyOf(t, show, episode(2))
			if got := spellings(r.history(k)); !slices.Equal(got, []string{"different_episode", "different_episode"}) {
				t.Errorf("rows %q, want both refused different_episode", got)
			}
			if got := r.lastRefusal(k, "en", language.Standard); got != search.RefusalDifferentEpisode {
				t.Errorf("noted %q, want different_episode", got)
			}
		})
	})
	t.Run("a member confirmed by its title", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			var pack []string
			for _, e := range fireflySeason {
				pack = append(pack, fmt.Sprintf("Firefly [S01 E%02d] - %s.eng.srt", e.disc, e.title))
			}
			const rel = "Firefly.S01.DVDRip.x264-SUBSOURCE"
			r.provider("p", "en").answer(nil, packCand(rel)).serve(rel, nil, zipOf(t, pack...))
			s := firefly14(false)
			inst := r.seedSeries(&s, fireflySerenity)
			r.script(refusedCut)
			r.mustRun(inst, statedb.Scan)
			k := keyOf(t, s.root, mediasource.Item{Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: fireflySerenity}})
			if got := spellings(r.history(k)); !slices.Equal(got, []string{"different_cut"}) {
				t.Errorf("rows %q, want different_cut", got)
			}
		})
	})
}

func TestComparisonAlignsEveryMember(t *testing.T) {
	a1, a2 := "Firefly.S01E01.720p.HDTV.x264-A1.srt", "Firefly.S01E01.720p.HDTV.x264-A2.srt"
	b1 := fireflyUntitled[fireflySerenity-1]
	for _, reversed := range []bool{false, true} {
		t.Run(fmt.Sprintf("reversed %t", reversed), func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				pack := append([]string{a1, a2}, fireflyUntitled[1:]...)
				if reversed {
					pack[0], pack[1] = a2, a1
				}
				const rel = "Firefly.S01.720p.HDTV.x264"
				r.provider("p", "en").answer(nil, packCand(rel)).serve(rel, nil, zipOf(t, pack...))
				s := firefly14(true)
				inst := r.seedSeries(&s, fireflySerenity)
				rec := r.script()
				rec.Member(a1, refusedCut).Member(a2, confident(t, 0.80)).Member(b1, confident(t, 0.60))
				r.mustRun(inst, statedb.Scan)
				if got := alignedMembers(rec, pack); len(got) != 3 {
					t.Errorf("aligned %q, want every member of both groups", got)
				}
				if got := r.record(inst).Provenance.Timing.Confidence; got != 0.80 {
					t.Errorf("delivered at confidence %v, want A2's 0.80", got)
				}
			})
		})
	}
}

func TestComparisonUsesOneTrial(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		const rel = "Firefly.S01.1080p.WEB-DL.DDP5.1.H.264-FLUX"
		pack := append([]string{"Firefly.S01E01.720p.HDTV.x264-A1.srt", "Firefly.S01E01.720p.HDTV.x264-A2.srt"}, fireflyUntitled[1:]...)
		p := r.provider("p", "en")
		cands := []provider.Candidate{packCand(rel)}
		p.serve(rel, nil, zipOf(t, pack...))
		for _, grp := range []string{"B", "C", "D"} {
			single := "Firefly.S01E01.Serenity.720p.HDTV.x264-" + grp
			cands = append(cands, packCand(single))
			p.serve(single, nil, srtBytes)
		}
		p.answer(nil, cands...)
		s := firefly14(true)
		inst := r.seedSeries(&s, fireflySerenity)
		rec := r.script(refusedCut, refusedCut, refusedCut, refusedCut, refusedCut)
		r.mustRun(inst, statedb.Scan)
		if got := len(rec.Payloads()); got != 5 {
			t.Errorf("%d Align calls, want the pack's three members and two more candidates", got)
		}
		if got := p.fetches(); len(got) != 3 || got[0] != rel {
			t.Errorf("fetches %q, want the pack then two candidates: the comparison is one of the run's three", got)
		}
		k := keyOf(t, s.root, mediasource.Item{Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: fireflySerenity}})
		if got := spellings(r.history(k)); !slices.Equal(got, []string{"different_episode", "different_cut", "different_cut"}) {
			t.Errorf("rows %q, want one row for the compared pack", got)
		}
	})
}

func TestPackMemberServesItsVariant(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		const rel = "Show.S01.720p.HDTV.x264-A"
		pack := []string{"Show.S01E01.srt", "Show.S01E02.sdh.srt", "Show.S01E02.srt", "Show.S01E03.srt"}
		r.provider("p", "en").answer(nil, packCand(rel)).serve(rel, nil, zipOf(t, pack...))
		inst := r.seed(show, nil, episode(1), episode(2), episode(3))[1]
		rec := r.script(alignedAs(t, timing.Verified))
		r.mustRun(inst, statedb.Scan)
		if got := alignedMembers(rec, pack); !slices.Equal(got, []string{"Show.S01E02.srt"}) {
			t.Errorf("aligned %q, want the Standard member, not the HI one before it", got)
		}
		if got := r.record(inst).Flags; got != (language.Flags{}) {
			t.Errorf("delivered flags %+v, want Standard", got)
		}
	})
}

func TestPackMemberParsesBaseName(t *testing.T) {
	long := strings.Repeat("x", release.MaxNameLen) + ".S01E02.srt"
	for _, c := range []struct {
		name    string
		pack    []string
		want    string
		refusal string
	}{
		{
			name: "folders are not read", want: "Show.S01E01-E03/Show.S01E02.srt",
			pack: []string{"Show.S01E01-E03/Show.S01E01.srt", "Show.S01E01-E03/Show.S01E02.srt", "Show.S01E01-E03/Show.S01E03.srt"},
		},
		{
			name: "a name is read to its bound", refusal: search.RefusalEpisodeCount,
			pack: []string{"Show.S01E01.srt", long, "Show.S01E03.srt"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				const rel = "Show.S01.720p.HDTV.x264-A"
				r.provider("p", "en").answer(nil, packCand(rel)).serve(rel, nil, zipOf(t, c.pack...))
				inst := r.seed(show, nil, episode(1), episode(2), episode(3))[1]
				rec := r.script(alignedAs(t, timing.Verified))
				r.mustRun(inst, statedb.Scan)
				got := alignedMembers(rec, c.pack)
				row := rowOf(t, r.history(keyOf(t, show, episode(2))), "p", rel)
				if c.want != "" && !slices.Equal(got, []string{c.want}) {
					t.Errorf("aligned %q, want %s", got, c.want)
				}
				if row.Refusal != c.refusal {
					t.Errorf("row %q, want %q", row.Refusal, c.refusal)
				}
			})
		})
	}
}
