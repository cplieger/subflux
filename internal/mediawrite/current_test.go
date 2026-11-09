package mediawrite_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/timing"
)

func TestCurrentReadsBaselineAndTiming(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	d := delivery(t, l.inst, "hello")
	d.Timing = timing.Shift{Ms: 1500}
	d.Provenance.Timing = timing.Outcome{Method: timing.MethodReference, Confidence: 0.7, Verdict: timing.Verified, OffsetMs: 1500}
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := l.w.Current(t.Context(), mediawrite.RefOf(rec), expectOf(rec))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := timing.Render(cur.Cues, timing.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if string(baseline) != srtOf("hello") || cur.Timing != (timing.Shift{Ms: 1500}) || cur.Tag != enTag {
		t.Errorf("Current = baseline %q, timing %#v, tag %v; want the unshifted document under its shift", baseline, cur.Timing, cur.Tag)
	}
	if cur.Outcome == nil || *cur.Outcome != d.Provenance.Timing {
		t.Errorf("Current outcome = %+v, want %+v", cur.Outcome, d.Provenance.Timing)
	}
}

func TestCurrentRigidFromFacts(t *testing.T) {
	t.Parallel()
	decisions := map[string]timing.Decision{
		"RigidReference": {Action: timing.RigidReference},
		"Skip/Disabled":  {Action: timing.Skip, Skip: timing.Disabled},
		"Skip/Hash":      {Action: timing.Skip, Skip: timing.Hash},
		"Skip/Forced":    {Action: timing.Skip, Skip: timing.Forced},
	}
	for name, decision := range decisions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
			d := delivery(t, l.inst, "hello")
			d.Provenance.Facts = rank.Facts{Cut: rank.Differs}
			d.Provenance.Timing = timing.Outcome{Decision: decision, Verdict: timing.Trusted}
			rec, err := l.w.Deliver(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			cur, err := l.w.Current(t.Context(), mediawrite.RefOf(rec), expectOf(rec))
			if err != nil {
				t.Fatal(err)
			}
			if !cur.Rigid {
				t.Errorf("a flagged cut delivered under %s reads back not rigid", name)
			}
		})
	}
	t.Run("unmanaged", func(t *testing.T) {
		t.Parallel()
		l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
		writeFile(t, l.path("S01E01.en.srt"), srtOf("the user's own"))
		f := l.listOne(t)
		cur, err := l.w.Current(t.Context(), f.File, f.Expect)
		if err != nil {
			t.Fatal(err)
		}
		if cur.Rigid || cur.Outcome != nil || cur.Timing != (timing.Identity{}) {
			t.Errorf("an unmanaged file reads rigid %v, outcome %+v, timing %#v; want none at Identity", cur.Rigid, cur.Outcome, cur.Timing)
		}
	})
}

func TestCurrentFlags(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, "default:\n  - tag: en\n    variants: [standard, hi, forced]\n", mediawrite.Deps{}, nil)
	d := delivery(t, l.inst, "hello")
	d.Flags, d.Variant = language.Flags{HI: true}, language.HI
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := l.w.Current(t.Context(), mediawrite.RefOf(rec), expectOf(rec))
	if err != nil {
		t.Fatal(err)
	}
	if cur.Flags != (language.Flags{HI: true}) {
		t.Errorf("a HI record reads back flags %+v", cur.Flags)
	}
	writeFile(t, l.path("S01E01.en.forced.srt"), srtOf("signs"))
	inv, err := l.w.List(t.Context(), l.inst, mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range inv.Files {
		if f.Name != "S01E01.en.forced.srt" {
			continue
		}
		cur, err := l.w.Current(t.Context(), f.File, f.Expect)
		if err != nil {
			t.Fatal(err)
		}
		if cur.Flags != (language.Flags{Forced: true}) || cur.Tag != enTag {
			t.Errorf("an unmanaged forced file reads back tag %v flags %+v", cur.Tag, cur.Flags)
		}
		return
	}
	t.Fatal("List did not list the forced file")
}

// listOne lists the library's instance with hashes and returns its one file.
func (l *library) listOne(t *testing.T) mediawrite.Found {
	t.Helper()
	inv, err := l.w.List(t.Context(), l.inst, mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil || len(inv.Files) != 1 {
		t.Fatalf("List = %+v, %v; want one file", inv, err)
	}
	return inv.Files[0]
}
