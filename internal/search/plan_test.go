package search_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

const day = 24 * time.Hour

// stem is episode n's video name less its extension.
func stem(n int) string { return episode(n).SceneName }

// The candidates' fits against episode 1's video, by their release names.
var (
	sameRelease = func(c *provider.Candidate) { c.Release = stem(1) }
	sameSource  = func(c *provider.Candidate) { c.Release = "Show.S01E01.1080p.WEB-DL.x264-OTHER" }
	otherFit    = func(c *provider.Candidate) { c.Release = "Show.S01E01.720p.HDTV.x264-OTHER" }
)

func withRef(token string) func(*provider.Candidate) {
	return func(c *provider.Candidate) { c.Ref = provider.NewRef(token) }
}

// unverified is a Provenance whose timing nothing verified, Method None
// when unreferenced.
func unverified(fit rank.Fit, unreferenced bool) statedb.Provenance {
	m := timing.MethodReference
	if unreferenced {
		m = timing.MethodNone
	}
	return statedb.Provenance{Provider: "p", Release: stem(1), Fit: fit, Timing: timing.Outcome{Verdict: timing.Unverified, Method: m}}
}

func verified(fit rank.Fit) statedb.Provenance {
	return statedb.Provenance{Provider: "p", Release: stem(1), Fit: fit, Timing: timing.Outcome{Verdict: timing.Verified, Method: timing.MethodReference}}
}

var textTrack = []statedb.EmbeddedTrack{{Codec: "srt", Index: 2, Tag: mustTag("ja")}}

// seedHeld seeds episode 1 with tracks and lands an English Standard file
// holding prov delivered at.
func (r *rig) seedHeld(tracks []statedb.EmbeddedTrack, prov statedb.Provenance, at time.Time) statedb.InstanceID {
	r.t.Helper()
	item := episode(1)
	inst := r.seed(show, tracks, item)[0]
	r.deliver(inst, keyOf(r.t, show, item), stem(1)+".en.srt", mustTag("en"), language.Flags{}, prov, language.Standard, at)
	return inst
}

func TestNoReferenceUnverifiedNotResearched(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en", sameRelease))
		inst := r.seedHeld(nil, unverified(rank.Other, true), time.Now())
		for _, cause := range []statedb.WorkCause{statedb.Scan, statedb.Import} {
			if got := r.pick(inst, cause); len(got) != 0 {
				t.Errorf("cause %d wants %+v, want nothing", cause, got)
			}
		}
		if p.calls() != 0 {
			t.Errorf("a scan and an import asked the provider %d times for a file no reference can verify", p.calls())
		}
		got := r.pick(inst, statedb.Item)
		if pk := pickOf(t, got, "en", language.Standard); !pk.Upgrade || len(pk.Ranked) != 1 {
			t.Errorf("a user's run picked %+v, want the same-release candidate over the held file", pk)
		}
	})
}

func TestNoReferenceUnverifiedUserSearchStrictOnly(t *testing.T) {
	answer := []provider.Candidate{
		cand(1, "en", otherFit, withRef("equal")),
		cand(1, "en", sameSource, withRef("gain")),
		cand(1, "en", sameRelease, withRef("close")),
	}
	t.Run("no reference", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			r.provider("p", "en").answer(nil, answer...)
			inst := r.seedHeld(nil, unverified(rank.Other, true), time.Now())
			got := pickOf(t, r.pick(inst, statedb.Item), "en", language.Standard)
			if want := []string{stem(1), "Show.S01E01.1080p.WEB-DL.x264-OTHER"}; !slices.Equal(releases(got), want) {
				t.Errorf("ranked %v, want the same-release candidate then the strict gain, never the equal one", releases(got))
			}
		})
	})
	t.Run("a text reference", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			r.provider("p", "en").answer(nil, answer...)
			inst := r.seedHeld(textTrack, unverified(rank.Other, true), time.Now())
			got := pickOf(t, r.pick(inst, statedb.Scan), "en", language.Standard)
			if len(got.Ranked) != 3 {
				t.Errorf("with a reference track ranked %v, want every untried candidate", releases(got))
			}
		})
	})
}

func TestMachineTranslatedUpgradableAnyAge(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil,
			cand(1, "en", otherFit, withRef("human")),
			cand(1, "en", otherFit, withRef("machine"), func(c *provider.Candidate) {
				c.MachineTranslated, c.Release = true, "Show.S01E01.720p.HDTV.x264-MT"
			}))
		prov := verified(rank.Other)
		prov.MachineTranslated = true
		inst := r.seedHeld(textTrack, prov, time.Now().Add(-400*day))
		got := pickOf(t, r.pick(inst, statedb.Scan), "en", language.Standard)
		if want := []string{"Show.S01E01.720p.HDTV.x264-OTHER"}; !got.Upgrade || !slices.Equal(releases(got), want) {
			t.Errorf("over a 400-day-old machine translation ranked %v, want only the equal human candidate", releases(got))
		}
	})
}

func TestUpgradeHumanTermPerSide(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil,
			cand(1, "en", otherFit, withRef("equal machine"), func(c *provider.Candidate) { c.MachineTranslated = true }),
			cand(1, "en", sameSource, withRef("better machine"), func(c *provider.Candidate) { c.MachineTranslated = true }),
			cand(1, "en", sameSource, withRef("better human")))
		inst := r.seedHeld(textTrack, verified(rank.Other), time.Now().Add(-day))
		got := pickOf(t, r.pick(inst, statedb.Scan), "en", language.Standard)
		if len(got.Ranked) != 1 || got.Ranked[0].Fit != rank.SameSource {
			t.Errorf("over a human file ranked %+v, want only the better human candidate: a machine translation never replaces a human file", got.Ranked)
		}
	})
}

func TestUpgradeNeedsBetterClass(t *testing.T) {
	t.Run("over a substitute", func(t *testing.T) {
		inRig(t, "close_varieties: true\ndefault: [{tag: pt-BR}]", func(t *testing.T, r *rig) {
			r.provider("p", "pt-BR", "pt-PT").answer(nil, cand(1, "pt-PT", sameRelease), cand(1, "pt-BR", otherFit))
			item := episode(1)
			inst := r.seed(show, textTrack, item)[0]
			r.deliver(inst, keyOf(t, show, item), stem(1)+".pt-PT.srt", mustTag("pt-PT"), language.Flags{}, verified(rank.SameRelease), language.Standard, time.Now().Add(-400*day))
			got := pickOf(t, r.pick(inst, statedb.Scan), "pt-BR", language.Standard)
			if !slices.Equal(releases(got), []string{"Show.S01E01.720p.HDTV.x264-OTHER"}) {
				t.Errorf("over a substitute ranked %v, want only the Exact candidate", releases(got))
			}
		})
	})
	t.Run("a tie-break is no gain", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			r.provider("p", "en").answer(nil, cand(1, "en", otherFit), cand(1, "en", sameRelease, withRef("pack"), func(c *provider.Candidate) {
				c.Release = "Show.S01.1080p.WEB-DL.x264-GRP"
			}))
			inst := r.seedHeld(textTrack, verified(rank.SameRelease), time.Now().Add(-day))
			got := pickOf(t, r.pick(inst, statedb.Scan), "en", language.Standard)
			if len(got.Ranked) != 0 {
				t.Errorf("over a same-release file ranked %v, want nothing", releases(got))
			}
		})
	})
}

func TestUnverifiedAndSubstituteUpgradeAfterWindow(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", otherFit))
		inst := r.seedHeld(textTrack, unverified(rank.Other, false), time.Now().Add(-30*day))
		got := pickOf(t, r.pick(inst, statedb.Scan), "en", language.Standard)
		if !got.Upgrade || len(got.Ranked) != 1 {
			t.Errorf("a 30-day-old unverified file: %+v, want upgradable by any untried candidate", got)
		}
	})
	inRig(t, "close_varieties: true\ndefault: [{tag: pt-BR}]", func(t *testing.T, r *rig) {
		r.provider("p", "pt-BR").answer(nil, cand(1, "pt-BR", otherFit))
		item := episode(1)
		inst := r.seed(show, textTrack, item)[0]
		r.deliver(inst, keyOf(t, show, item), stem(1)+".pt-PT.srt", mustTag("pt-PT"), language.Flags{}, verified(rank.Exact), language.Standard, time.Now().Add(-30*day))
		if got := pickOf(t, r.pick(inst, statedb.Scan), "pt-BR", language.Standard); len(got.Ranked) != 1 {
			t.Errorf("a 30-day-old substitute: %+v, want upgradable by an Exact candidate", got)
		}
	})
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en", sameRelease))
		inst := r.seedHeld(textTrack, verified(rank.Other), time.Now().Add(-30*day))
		if got := r.pick(inst, statedb.Scan); len(got) != 0 || p.calls() != 0 {
			t.Errorf("a verified human file past the window: %+v and %d calls, want nothing asked", got, p.calls())
		}
	})
}

func TestUserExactFileCoversPair(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en", sameRelease))
		inst := r.seedHeld(textTrack, unverified(rank.Other, false), time.Now())
		in, _, err := r.db.Instance(t.Context(), inst)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(in.VideoPath), stem(1)+".en-US.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := r.pick(inst, statedb.Scan); len(got) != 0 || p.calls() != 0 {
			t.Errorf("beside a user's Exact file: %+v and %d calls, want the pair covered", got, p.calls())
		}
	})
}

func TestLockedPairNotSearched(t *testing.T) {
	inRig(t, "default: [{tag: en}, {tag: fr}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en", "fr")
		item := episode(1)
		inst := r.seed(show, textTrack, item)[0]
		r.deliver(inst, keyOf(t, show, item), stem(1)+".fr.srt", mustTag("fr"), language.Flags{}, unverified(rank.Other, false), language.Standard, time.Now())
		if err := r.db.SetLock(t.Context(), inst, mustTag("fr"), language.Standard, true); err != nil {
			t.Fatal(err)
		}
		got := r.pick(inst, statedb.Item)
		if len(got) != 1 || got[0].Target != mustTag("en") {
			t.Errorf("wants %+v, want only the unlocked en pair", got)
		}
		if q := p.lastQuery().Langs; !slices.Equal(q, []language.Tag{mustTag("en")}) {
			t.Errorf("asked %v, want [en]", q)
		}
	})
}

func TestExcludedNotSearched(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en")
		item := episode(1)
		item.Excluded = true
		inst := r.seed(show, nil, item)[0]
		for _, cause := range []statedb.WorkCause{statedb.Scan, statedb.Import} {
			if got := r.pick(inst, cause); got != nil {
				t.Errorf("cause %d planned %+v for an excluded item", cause, got)
			}
		}
		if p.calls() != 0 {
			t.Errorf("an excluded item was searched %d times automatically", p.calls())
		}
		if got := r.pick(inst, statedb.Item); len(got) != 1 || p.calls() != 1 {
			t.Errorf("a user's run planned %+v with %d calls, want the item searched", got, p.calls())
		}
	})
}

func TestRootUnavailableNoProviderCall(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en")
		inst := r.seed(show, nil, episode(1))[0]
		if err := os.RemoveAll(r.dir); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour)
		_, err := search.PickFor(t.Context(), r.engine(), statedb.Work{Inst: inst, Gen: 1, Cause: statedb.Item})
		if !errors.Is(err, mediawrite.ErrRootUnavailable) {
			t.Errorf("PickFor = %v, want ErrRootUnavailable", err)
		}
		if p.calls() != 0 {
			t.Errorf("an unavailable root's instance asked the provider %d times", p.calls())
		}
	})
}

func TestInstanceGonePlansNothing(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en")
		got, err := search.PickFor(context.Background(), r.engine(), statedb.Work{Inst: 999, Gen: 1, Cause: statedb.Scan})
		if err != nil || got != nil {
			t.Errorf("PickFor of an unknown instance = %+v, %v; want nothing planned", got, err)
		}
	})
}

func TestVideoStringPathParsesBaseName(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", sameRelease))
		item := episode(1)
		item.SceneName = ""
		got := r.pick(r.seed(show, nil, item)[0], statedb.Scan)
		if len(got) != 1 || len(got[0].Ranked) != 1 || got[0].Ranked[0].Fit != rank.SameRelease {
			t.Errorf("picks = %+v, want the candidate named as the video's base name to fit same_release", got)
		}
	})
}

func TestOneSweepPerInstance(t *testing.T) {
	inRig(t, "default: [{tag: en}, {tag: fr}]", func(t *testing.T, r *rig) {
		a := r.provider("a", "en", "fr")
		b := r.provider("b", "fr")
		one, two := episode(1), episode(2)
		two.VideoPath = one.VideoPath
		inst := r.seed(show, nil, one, two)
		if len(inst) != 1 {
			t.Fatalf("instances %v, want the two episodes in one", inst)
		}
		r.pick(inst[0], statedb.Scan)
		if a.calls() != 1 || b.calls() != 1 {
			t.Fatalf("calls a %d, b %d; want each provider asked once for the instance", a.calls(), b.calls())
		}
		if got := a.lastQuery().Langs; !slices.Equal(got, []language.Tag{mustTag("en"), mustTag("fr")}) {
			t.Errorf("a was asked %v, want both targets in one query", got)
		}
		q := b.lastQuery()
		if !slices.Equal(q.Langs, []language.Tag{mustTag("fr")}) || q.Numbering != one.Numbering || q.Year != show.Year || q.OSHash == 0 {
			t.Errorf("b's query = %+v, want fr, the first item's numbering, the root's year and the video's hash", q)
		}
	})
}

func TestNoProviderForTargetSkips(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "fr")
		if got := r.pick(r.seed(show, nil, episode(1))[0], statedb.Scan); len(got) != 1 || got[0].Skip != search.NoCandidate {
			t.Errorf("with no provider for the target: %+v, want the pair skipped, no candidate", got)
		}
	})
}

// TestProbeFailurePlansWithStored runs outside a bubble: the failing probe
// waits on ffprobe, a child process.
func TestProbeFailurePlansWithStored(t *testing.T) {
	r := newRig(t, "default: [{tag: en}]")
	p := r.provider("p", "en")
	inst := r.seed(show, textTrack, episode(1))[0]
	if err := r.db.PutEmbedded(t.Context(), inst, statedb.VideoProbe{Tracks: textTrack, Stamp: statedb.VideoStamp{Size: 1, MtimeNs: 1}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	got, err := search.PickFor(ctx, r.engine(), statedb.Work{Inst: inst, Gen: 1, Cause: statedb.Scan})
	if err != nil || len(got) != 1 || p.calls() != 1 {
		t.Errorf("a run whose probe failed: %+v, %v, %d calls; want it planned with what is stored", got, err, p.calls())
	}
}
