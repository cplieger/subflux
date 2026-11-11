package search_test

import (
	"errors"
	"path/filepath"
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

// firefly is a DVD-order series root of the rig.
var firefly = mediasource.Root{
	Native: mediasource.NativeRef{ID: "78874", Kind: media.Episode}, Title: "Firefly",
	External: media.ExternalIDs{TVDB: 78874}, Year: 2002, Order: mediasource.Order{Type: "dvd", Name: "DVD Order"},
}

// fireflyEp is one of Firefly's first three episodes: its aired number, its
// DVD number and title.
type fireflyEp struct {
	title       string
	aired, disc int
}

var fireflyEps = []fireflyEp{{"The Train Job", 1, 2}, {"Bushwhacked", 2, 3}, {"Serenity", 3, 1}}

// fireflyItem is the DVD-order item of e at its video, named name.
func fireflyItem(e fireflyEp, name string) mediasource.Item {
	return mediasource.Item{
		Native: mediasource.NativeID("f" + two(e.aired)), Title: e.title,
		VideoPath: filepath.Join("Firefly", name+".mkv"), SceneName: name,
		Numbering:       media.Numbering{Scheme: media.TVDB, Season: 1, Episode: e.aired},
		SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: e.disc},
	}
}

// seedFirefly seeds the DVD item "Serenity" (aired S01E03, DVD S01E01),
// its video named video, with the series' reading: its aired list titled
// in English and its DVD order.
func (r *rig) seedFirefly(video string) statedb.InstanceID {
	r.t.Helper()
	var items []mediasource.Item
	var disc []media.Numbering
	for _, e := range fireflyEps {
		items = append(items, fireflyItem(e, "unused"))
		disc = append(disc, media.Numbering{Season: 1, Episode: e.disc})
	}
	ev := airedReading(items, map[string][]media.Numbering{"dvd": disc})
	r.readings[evidenceKey{src: "sonarr", root: firefly.Native}] = &ev
	return r.seed(firefly, textTrack, fireflyItem(fireflyEps[2], video))[0]
}

// named is a candidate for Firefly in en named release, matched by id.
func named(release string) provider.Candidate {
	return provider.Candidate{Ref: provider.NewRef(release), Lang: mustTag("en"), Release: release, MatchedBy: rank.ID}
}

func TestIdentityAcceptsAiredClaimOnDVDItem(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		aired := "Firefly.S01E03.Serenity.720p.HDTV.x264-A"
		disc := "Firefly.S01E01.Serenity.720p.HDTV.x264-B"
		untitled := "Firefly.S01E01.720p.HDTV.x264-C"
		r.provider("p", "en").answer(nil, named(aired), named(disc), named(untitled))
		inst := r.seedFirefly("Firefly.S01E01.1080p.BluRay.x264-GRP")
		got := releases(pickOf(t, r.pick(inst, statedb.Scan), "en", language.Standard))
		slices.Sort(got)
		if want := []string{untitled, disc, aired}; !slices.Equal(got, want) {
			t.Errorf("ranked %q, want the aired- and the DVD-numbered Serenity, and the untitled DVD number", got)
		}
	})
}

func TestIdentityRejectsTitleElsewhere(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, named("Firefly.S01E01.The.Train.Job.720p.HDTV.x264-A"))
		inst := r.seedFirefly("Firefly.S01E01.1080p.BluRay.x264-GRP")
		if got := pickOf(t, r.pick(inst, statedb.Scan), "en", language.Standard); len(got.Ranked) != 0 || got.Skip != search.NoCandidate {
			t.Errorf("picked %+v, want The Train Job dropped though its number fits the DVD order", got)
		}
	})
}

func TestIdentityEvidenceStoreErrorFailsRun(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, named("Firefly.S01E01.Serenity"))
		inst := r.seedFirefly("Firefly.S01E01.1080p.BluRay.x264-GRP")
		r.evidenceErr = errStoreDown
		_, err := search.PickFor(t.Context(), r.engine(), statedb.Work{Inst: inst, Gen: 1, Cause: statedb.Scan})
		if !errors.Is(err, errStoreDown) {
			t.Errorf("PickFor = %v, want the evidence's store error", err)
		}
	})
}

func TestSingleNameByNumberNeverSkipsSameRelease(t *testing.T) {
	const name = "Firefly.S01E01.1080p.BluRay.x264-GRP"
	cases := []struct {
		seed func(r *rig) statedb.InstanceID
		name string
		want timing.Decision
	}{
		{name: "a DVD-order series", want: timing.Decision{Action: timing.ActionReference}, seed: func(r *rig) statedb.InstanceID {
			return r.seedFirefly(name)
		}},
		{name: "a single-order series", want: timing.Decision{Action: timing.Skip, Skip: timing.SameRelease}, seed: func(r *rig) statedb.InstanceID {
			got := fireflyItem(fireflyEps[2], name)
			got.Numbering, got.SeriesNumbering = media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}, media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}
			root := firefly
			root.Order = mediasource.Order{Type: "official", Name: "Aired", Aired: true}
			return r.seed(root, textTrack, got)[0]
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.timing = timing.Config{Sync: true}
				r.provider("p", "en").answer(nil, named(name)).serve(name, nil, srtBytes)
				inst := c.seed(r)
				rec := r.script(alignedAs(t, timing.Verified))
				r.mustRun(inst, statedb.Scan)
				if got := rec.Decisions(); len(got) != 1 || got[0] != c.want {
					t.Errorf("aligned with %+v, want %+v", got, c.want)
				}
			})
		})
	}
}

func TestEvidenceFromItemRoot(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		rootA, rootB := show, show
		rootB.Native.ID = "b73"
		e1, e2 := episode(1), episode(2)
		e1.Title, e2.Title = "Alpha", "Gamma"
		r.readings[evidenceKey{src: "sonarr", root: rootA.Native}] = new(airedReading([]mediasource.Item{withTitle(e1, "Gamma"), withTitle(e2, "Delta")}, nil))
		r.readings[evidenceKey{src: "sonarr-b", root: rootB.Native}] = new(airedReading([]mediasource.Item{withTitle(e1, "Alpha"), withTitle(e2, "Gamma")}, nil))
		r.provider("p", "en").answer(nil, named("Show.S01E02.Gamma.720p.HDTV.x264-A"))
		instB := r.seedFrom("sonarr-b", rootB, nil, e2)[0]
		if got := pickOf(t, r.pick(instB, statedb.Scan), "en", language.Standard); len(got.Ranked) != 1 {
			t.Errorf("B's item picked %+v, want Gamma kept by B's reading", got)
		}
		if want := (evidenceKey{src: "sonarr-b", root: rootB.Native}); !slices.Contains(r.asked, want) {
			t.Errorf("asked %+v, want B's source and root", r.asked)
		}
	})
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		e1, e2 := episode(1), episode(2)
		r.readings[evidenceKey{src: "sonarr", root: show.Native}] = new(airedReading([]mediasource.Item{withTitle(e1, "Gamma"), withTitle(e2, "Delta")}, map[string][]media.Numbering{"dvd": {{Season: 1, Episode: 2}, {Season: 1, Episode: 1}}}))
		r.provider("p", "en").answer(nil, named("Show.S01E02.Gamma.720p.HDTV.x264-A"))
		instA := r.seed(show, nil, e2)[0]
		if got := pickOf(t, r.pick(instA, statedb.Scan), "en", language.Standard); len(got.Ranked) != 0 {
			t.Errorf("A's item picked %+v, want Gamma refused by A's reading, which titles episode 1 so", got)
		}
	})
}

func TestIdentitySingleOrderSkipsTitles(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		e1, e2 := episode(1), episode(2)
		r.readings[evidenceKey{src: "sonarr", root: show.Native}] = new(airedReading([]mediasource.Item{withTitle(e1, "Gamma"), withTitle(e2, "Delta")}, nil))
		r.provider("p", "en").answer(nil, named("Show.S01E02.Gamma.720p.HDTV.x264-A"))
		inst := r.seed(show, nil, e2)[0]
		if got := pickOf(t, r.pick(inst, statedb.Scan), "en", language.Standard); len(got.Ranked) != 1 {
			t.Errorf("picked %+v, want the number trusted on a series with one order", got)
		}
	})
}

func TestEvidenceMissingLanguageMatchedWithout(t *testing.T) {
	inRig(t, "default: [{tag: fr}]", func(t *testing.T, r *rig) {
		c := named("Firefly.S01E01.Serenity.720p.HDTV.x264-A")
		c.Lang = mustTag("fr")
		wrong := named("Firefly.S01E01.The.Train.Job.720p.HDTV.x264-A")
		wrong.Lang, wrong.Ref = mustTag("fr"), provider.NewRef("wrong")
		r.provider("p", "fr").answer(nil, c, wrong)
		inst := r.seedFirefly("Firefly.S01E01.1080p.BluRay.x264-GRP")
		got := releases(pickOf(t, r.pick(inst, statedb.Scan), "fr", language.Standard))
		if !slices.Equal(got, []string{c.Release}) {
			t.Errorf("ranked %q, want the French Serenity matched by its English title alone", got)
		}
		if len(r.asked) != 1 {
			t.Errorf("%d evidence reads, want one per run", len(r.asked))
		}
	})
}

func withTitle(it mediasource.Item, title string) mediasource.Item {
	it.Title = title
	return it
}

var _ search.Evidence = rigEvidence{}
