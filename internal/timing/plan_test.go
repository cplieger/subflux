package timing_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/timing"
)

var (
	syncOn       = timing.Config{Sync: true}
	withAudio    = timing.Config{Sync: true, AudioFallback: true}
	reference    = timing.Decision{Action: timing.ActionReference}
	thenAudio    = timing.Decision{Action: timing.ReferenceThenAudio}
	rigid        = timing.Decision{Action: timing.RigidReference}
	sameRelease  = rank.Facts{Source: rank.Same, Group: rank.Same, Service: rank.Same, Cut: rank.Same, Rate: rank.Same}
	skipped      = func(r timing.SkipReason) timing.Decision { return timing.Decision{Action: timing.Skip, Skip: r} }
	withFacts    = func(edit func(*rank.Facts)) rank.Facts { f := sameRelease; edit(&f); return f }
	differentCut = withFacts(func(f *rank.Facts) { f.Cut = rank.Differs })
)

func TestPlanDifferentGroupSyncs(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]rank.Facts{
		"same source and service, another group": withFacts(func(f *rank.Facts) { f.Group = rank.Differs }),
		"the same release at another frame rate": withFacts(func(f *rank.Facts) { f.Rate = rank.Differs }),
	} {
		if got := timing.Plan(f, language.Standard, syncOn); got != reference {
			t.Errorf("%s: %+v, want %+v", name, got, reference)
		}
	}
}

func TestPlanPreReleaseIsReference(t *testing.T) {
	t.Parallel()
	pre := withFacts(func(f *rank.Facts) { f.PreRelease = true })
	if got := timing.Plan(pre, language.Standard, syncOn); got != reference {
		t.Errorf("pre-release: %+v, want %+v", got, reference)
	}
	if got := timing.Plan(pre, language.Standard, withAudio); got != thenAudio {
		t.Errorf("pre-release with audio_fallback: %+v, want %+v", got, thenAudio)
	}
	for _, cfg := range []timing.Config{syncOn, withAudio} {
		if got := timing.Plan(differentCut, language.Standard, cfg); got != rigid {
			t.Errorf("different cut, audio_fallback %v: %+v, want %+v", cfg.AudioFallback, got, rigid)
		}
	}
}

func TestPlanNumberChosenNeverSameRelease(t *testing.T) {
	t.Parallel()
	byNumber := withFacts(func(f *rank.Facts) { f.EpisodeByNumber = true })
	if got := timing.Plan(byNumber, language.Standard, syncOn); got != reference {
		t.Errorf("chosen by number: %+v, want %+v", got, reference)
	}
	if got := timing.Plan(byNumber, language.Standard, withAudio); got != thenAudio {
		t.Errorf("chosen by number, audio_fallback: %+v, want %+v", got, thenAudio)
	}
	cut := withFacts(func(f *rank.Facts) { f.EpisodeByNumber, f.Cut = true, rank.Differs })
	if got := timing.Plan(cut, language.Standard, syncOn); got != rigid {
		t.Errorf("chosen by number, different cut: %+v, want %+v", got, rigid)
	}
	if got := timing.Plan(sameRelease, language.Standard, syncOn); got != skipped(timing.SameRelease) {
		t.Errorf("not chosen by number: %+v, want Skip/SameRelease", got)
	}
}

func TestPlanPerTerm(t *testing.T) {
	t.Parallel()
	hash := withFacts(func(f *rank.Facts) { f.Hash = true })
	for _, c := range []struct {
		name string
		f    rank.Facts
		v    language.Variant
		cfg  timing.Config
		want timing.Decision
	}{
		{"sync off", sameRelease, language.Standard, timing.Config{AudioFallback: true}, skipped(timing.Disabled)},
		{"sync off before a hash", hash, language.Forced, timing.Config{}, skipped(timing.Disabled)},
		{"a validated hash", hash, language.Standard, syncOn, skipped(timing.Hash)},
		{"a hash before a forced variant", hash, language.Forced, syncOn, skipped(timing.Hash)},
		{"a forced variant", rank.Facts{}, language.Forced, syncOn, skipped(timing.Forced)},
		{"the same release", sameRelease, language.HI, syncOn, skipped(timing.SameRelease)},
		{"the same release, service unknown", withFacts(func(f *rank.Facts) { f.Service = rank.Unknown }), language.Standard, syncOn, skipped(timing.SameRelease)},
		{"another source", withFacts(func(f *rank.Facts) { f.Source = rank.Differs }), language.Standard, syncOn, reference},
		{"source unknown", withFacts(func(f *rank.Facts) { f.Source = rank.Unknown }), language.Standard, syncOn, reference},
		{"group unknown", withFacts(func(f *rank.Facts) { f.Group = rank.Unknown }), language.Standard, syncOn, reference},
		{"another service", withFacts(func(f *rank.Facts) { f.Service = rank.Differs }), language.Standard, syncOn, reference},
		{"a flagged cut with sync off", differentCut, language.Standard, timing.Config{}, skipped(timing.Disabled)},
		{"a flagged cut with a validated hash", withFacts(func(f *rank.Facts) { f.Cut, f.Hash = rank.Differs, true }), language.Standard, syncOn, skipped(timing.Hash)},
		{"a flagged cut, forced", differentCut, language.Forced, syncOn, skipped(timing.Forced)},
		{"nothing known", rank.Facts{}, language.Standard, syncOn, reference},
		{"nothing known, audio_fallback", rank.Facts{}, language.Standard, withAudio, thenAudio},
	} {
		if got := timing.Plan(c.f, c.v, c.cfg); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
