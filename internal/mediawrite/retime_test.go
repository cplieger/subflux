package mediawrite_test

import (
	"errors"
	"os"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

func expectOf(rec statedb.FileRecord) mediawrite.Expectation {
	return mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}
}

// snapshot is everything a stale write must leave as it was: the file's
// bytes, the record and the baseline's rendering.
type snapshot struct {
	file, baseline string
	rec            statedb.FileRecord
	present        bool
	managed        bool
}

func (l *library) snapshot(t *testing.T, name string) snapshot {
	t.Helper()
	var s snapshot
	if b, err := os.ReadFile(l.path(name)); err == nil {
		s.file, s.present = string(b), true
	}
	recs, err := l.store.Files(t.Context(), l.inst)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Name == name {
			s.rec, s.managed = r, true
		}
	}
	cues, found, err := l.store.Baseline(t.Context(), l.inst, name)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		b, err := timing.Render(cues, timing.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		s.baseline = string(b)
	}
	return s
}

func sameSnapshot(a, b snapshot) bool {
	return a.file == b.file && a.present == b.present && a.managed == b.managed && a.baseline == b.baseline &&
		a.rec.Generation == b.rec.Generation && a.rec.Identity == b.rec.Identity && a.rec.Timing == b.rec.Timing
}

func TestStaleExpectationChangesNothing(t *testing.T) {
	t.Parallel()
	apply := &timing.Outcome{Method: timing.MethodReference, Confidence: 0.9, Verdict: timing.Verified, OffsetMs: 400}
	ops := []struct {
		run  func(*testing.T, *library, mediawrite.FileRef, mediawrite.Expectation) error
		name string
	}{
		{name: "an apply", run: func(t *testing.T, l *library, ref mediawrite.FileRef, e mediawrite.Expectation) error {
			_, err := l.w.Retime(t.Context(), ref, e, timing.Shift{Ms: 400}, apply)
			return err
		}},
		{name: "a user retime", run: func(t *testing.T, l *library, ref mediawrite.FileRef, e mediawrite.Expectation) error {
			_, err := l.w.Retime(t.Context(), ref, e, timing.Shift{Ms: -300}, nil)
			return err
		}},
		{name: "a delete", run: func(t *testing.T, l *library, ref mediawrite.FileRef, e mediawrite.Expectation) error {
			return l.w.Delete(t.Context(), ref, e)
		}},
	}
	stalers := []struct {
		stale func(*testing.T, *library, statedb.FileRecord)
		name  string
	}{
		{name: "a delete and a new delivery", stale: func(t *testing.T, l *library, rec statedb.FileRecord) {
			if err := l.w.Delete(t.Context(), mediawrite.RefOf(rec), expectOf(rec)); err != nil {
				t.Fatal(err)
			}
			again, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "delivered anew"))
			if err != nil {
				t.Fatal(err)
			}
			if again.Name != rec.Name || again.Generation != rec.Generation {
				t.Fatalf("premise: the new delivery is %q at %d, want %q at the old generation %d", again.Name, again.Generation, rec.Name, rec.Generation)
			}
		}},
		{name: "a re-download", stale: func(t *testing.T, l *library, rec statedb.FileRecord) {
			d := delivery(t, l.inst, "downloaded again")
			d.Supersedes = []mediawrite.Superseded{{File: mediawrite.RefOf(rec), Expect: expectOf(rec)}}
			if _, err := l.w.Deliver(t.Context(), d); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "another apply", stale: func(t *testing.T, l *library, rec statedb.FileRecord) {
			if _, err := l.w.Retime(t.Context(), mediawrite.RefOf(rec), expectOf(rec), timing.Shift{Ms: 900}, apply); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, op := range ops {
		for _, st := range stalers {
			t.Run(op.name+" after "+st.name, func(t *testing.T) {
				t.Parallel()
				l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
				rec, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "original"))
				if err != nil {
					t.Fatal(err)
				}
				st.stale(t, l, rec)
				before := l.snapshot(t, rec.Name)
				if err := op.run(t, l, mediawrite.RefOf(rec), expectOf(rec)); !errors.Is(err, mediawrite.ErrExpectation) {
					t.Errorf("%s holding a stale expectation = %v, want ErrExpectation", op.name, err)
				}
				if after := l.snapshot(t, rec.Name); !sameSnapshot(before, after) {
					t.Errorf("the stale write changed the file, record or baseline:\nbefore %+v\nafter  %+v", before, after)
				}
			})
		}
	}
}

func TestRetimeRenameEACCESKeepsDirWritable(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
		f.Rename = func(_ *os.Root, from, to string) error {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EACCES}
		}
	})
	rec, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.w.Retime(t.Context(), mediawrite.RefOf(rec), expectOf(rec), timing.Shift{Ms: 500}, nil); !errors.Is(err, syscall.EACCES) || errors.Is(err, mediawrite.ErrRootUnavailable) {
		t.Errorf("Retime whose rename-over answers EACCES = %v, want the path error alone", err)
	}
	inv, err := l.w.List(t.Context(), l.inst, mediawrite.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Unwritable {
		t.Error("a refused rename-over made the folder unwritable")
	}
}

func TestSameSizeSameTickEdit(t *testing.T) {
	t.Parallel()
	// edit rewrites path in place with other bytes of the same size and
	// puts its modification time back.
	edit := func(t *testing.T, path, text string) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(srtOf(text)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmanaged", true: "managed"}[managed], func(t *testing.T) {
			t.Parallel()
			l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
			path := l.path("S01E01.en.srt")
			if managed {
				if _, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "aaaa")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, path, srtOf("aaaa"))
			}
			first := l.listOne(t)
			edit(t, path, "bbbb")
			if _, err := l.w.Retime(t.Context(), first.File, first.Expect, timing.Shift{Ms: 500}, nil); !errors.Is(err, mediawrite.ErrExpectation) {
				t.Fatalf("Retime after a same-size, same-tick edit = %v, want ErrExpectation", err)
			}
			second := l.listOne(t)
			if second.Expect == first.Expect || second.Managed {
				t.Fatalf("the next List = %+v; want the edited file, unmanaged, at its new hash", second)
			}
			if _, err := l.w.Retime(t.Context(), second.File, second.Expect, timing.Shift{Ms: 500}, nil); err != nil {
				t.Errorf("Retime with the next List's expectation = %v, want success", err)
			}
		})
	}
}

func TestUserEditVerified(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	d := delivery(t, l.inst, "hello")
	d.Timing = timing.Shift{Ms: 250}
	decision := timing.Decision{Action: timing.ActionReference}
	d.Provenance.Facts = rank.Facts{Group: rank.Differs}
	d.Provenance.Timing = timing.Outcome{Decision: decision, Method: timing.Audio, Confidence: 0.8, Verdict: timing.Unverified, OffsetMs: 250}
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := l.w.Retime(t.Context(), mediawrite.RefOf(rec), expectOf(rec), timing.Shift{Ms: 700}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := timing.Outcome{Decision: decision, Method: timing.MethodNone, Confidence: 0, Verdict: timing.Verified, OffsetMs: 700}
	if edited.Provenance == nil || edited.Provenance.Timing != want {
		t.Fatalf("a user edit's outcome = %+v, want %+v", edited.Provenance, want)
	}
	if edited.Provenance.Facts != d.Provenance.Facts || edited.Provenance.Provider != "opensubtitles" || edited.Timing != (timing.Shift{Ms: 700}) {
		t.Errorf("a user edit lost the record's facts, provider or new timing: %+v, %#v", edited.Provenance, edited.Timing)
	}
}

func TestRetimeKeepsDecision(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	d := delivery(t, l.inst, "hello")
	decision := timing.Decision{Action: timing.RigidReference}
	d.Provenance.Timing = timing.Outcome{Decision: decision, Verdict: timing.Unverified}
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	apply := timing.Outcome{Decision: timing.Decision{Action: timing.ReferenceThenAudio}, Method: timing.MethodReference, Confidence: 0.9, Verdict: timing.Verified, OffsetMs: 300}
	applied, err := l.w.Retime(t.Context(), mediawrite.RefOf(rec), expectOf(rec), timing.Shift{Ms: 300}, &apply)
	if err != nil {
		t.Fatal(err)
	}
	want := apply
	want.Decision = decision
	if applied.Provenance == nil || applied.Provenance.Timing != want {
		t.Errorf("an apply's outcome = %+v, want the apply's with the record's decision %+v", applied.Provenance, want)
	}
}

func TestAdoptUnmanaged(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	writeFile(t, l.path("S01E01.en.srt"), srtOf("the user's own"))
	inv, err := l.w.List(t.Context(), l.inst, mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil || len(inv.Files) != 1 {
		t.Fatalf("List = %+v, %v", inv, err)
	}
	f := inv.Files[0]
	rec, err := l.w.Retime(t.Context(), f.File, f.Expect, timing.Shift{Ms: 1000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Locked || rec.Provenance != nil || rec.Generation != 1 || rec.Tag != enTag {
		t.Errorf("adopted record = locked %v, provenance %+v, generation %d, tag %v; want a locked English record with none", rec.Locked, rec.Provenance, rec.Generation, rec.Tag)
	}
	if got, want := readFile(t, l.path(f.Name)), "1\n00:00:02,000 --> 00:00:03,000\nthe user's own\n"; got != want {
		t.Errorf("adopted file reads %q, want %q", got, want)
	}
	cur, err := l.w.Current(t.Context(), mediawrite.RefOf(rec), expectOf(rec))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := timing.Render(cur.Cues, timing.Identity{}); string(b) != srtOf("the user's own") || cur.Outcome != nil {
		t.Errorf("adopted baseline renders %q with outcome %+v; want the original bytes and none", b, cur.Outcome)
	}
	rows, err := l.store.Coverage(t.Context(), l.key)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].Locked {
		t.Errorf("coverage = %+v, want English locked by the adoption", rows)
	}
}

func TestEqualBytesUpdateRecordOnly(t *testing.T) {
	t.Parallel()
	var stages atomic.Int32
	l := newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
		create := f.CreateStage
		f.CreateStage = func(dir *os.Root, name string) (*os.File, error) { stages.Add(1); return create(dir, name) }
	})
	d := delivery(t, l.inst, "hello")
	d.Timing = timing.Shift{Ms: 1000}
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(l.path(rec.Name))
	if err != nil {
		t.Fatal(err)
	}
	apply := timing.Outcome{Method: timing.MethodReference, Confidence: 0.95, Verdict: timing.Verified, OffsetMs: 1000}
	applied, err := l.w.Retime(t.Context(), mediawrite.RefOf(rec), expectOf(rec), timing.Shift{Ms: 1000}, &apply)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(l.path(rec.Name))
	if err != nil {
		t.Fatal(err)
	}
	if stages.Load() != 1 || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an equal-bytes retime staged %d times in all or replaced the file", stages.Load())
	}
	if applied.Generation != 2 || applied.Provenance.Timing.Confidence != 0.95 || applied.Identity != rec.Identity {
		t.Errorf("record after an equal-bytes apply = generation %d, outcome %+v, identity %+v; want generation 2, the apply, the same identity",
			applied.Generation, applied.Provenance.Timing, applied.Identity)
	}
}

func TestSubRefused(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	writeFile(t, l.path("S01E01.en.sub"), "\x00\x00\x01\xba vobsub")
	inv, err := l.w.List(t.Context(), l.inst, mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil || len(inv.Files) != 1 {
		t.Fatalf("List = %+v, %v; want the .sub", inv, err)
	}
	f := inv.Files[0]
	if _, err := l.w.Retime(t.Context(), f.File, f.Expect, timing.Shift{Ms: 1000}, nil); !errors.Is(err, timing.ErrFormatUnsupported) {
		t.Errorf("Retime of a .sub = %v, want ErrFormatUnsupported", err)
	}
	if err := l.w.Delete(t.Context(), f.File, f.Expect); !errors.Is(err, timing.ErrFormatUnsupported) {
		t.Errorf("Delete of a .sub = %v, want ErrFormatUnsupported", err)
	}
	if _, err := l.w.Current(t.Context(), f.File, f.Expect); !errors.Is(err, timing.ErrFormatUnsupported) {
		t.Errorf("Current of a .sub = %v, want ErrFormatUnsupported", err)
	}
	if got := readFile(t, l.path("S01E01.en.sub")); got != "\x00\x00\x01\xba vobsub" {
		t.Errorf("the .sub now reads %q", got)
	}
}

func TestRetimeOversizeRefusedBeforeIntent(t *testing.T) {
	t.Parallel()
	const capBytes = 16 << 20
	doc := padded(capBytes-100, "95:00:01,000 --> 95:00:02,000", string(slices.Repeat([]byte("x"), 999)))
	sixHours := timing.Shift{Ms: 6 * 3_600_000}
	var stages atomic.Int32
	countStages := func(f *mediawrite.Faults) {
		create := f.CreateStage
		f.CreateStage = func(dir *os.Root, name string) (*os.File, error) { stages.Add(1); return create(dir, name) }
	}
	check := func(t *testing.T, l *library, f mediawrite.FileRef, e mediawrite.Expectation) {
		t.Helper()
		before := stages.Load()
		if _, err := l.w.Retime(t.Context(), f, e, sixHours, nil); !errors.Is(err, timing.ErrFormatUnsupported) {
			t.Errorf("Retime past the cap = %v, want ErrFormatUnsupported", err)
		}
		if n, staged := pendingCount(t, l.store), stages.Load()-before; n != 0 || staged != 0 {
			t.Errorf("%d intents and %d stages after the refusal, want none", n, staged)
		}
		if got := readFile(t, l.path(f.Name())); got != doc {
			t.Error("the file changed")
		}
	}
	t.Run("at adoption", func(t *testing.T) {
		l := newLibrary(t, enOnly, mediawrite.Deps{}, countStages)
		writeFile(t, l.path("S01E01.en.srt"), doc)
		inv, err := l.w.List(t.Context(), l.inst, mediawrite.ListOptions{UnmanagedExpect: true})
		if err != nil || len(inv.Files) != 1 {
			t.Fatalf("List = %+v, %v", inv, err)
		}
		check(t, l, inv.Files[0].File, inv.Files[0].Expect)
	})
	t.Run("a managed baseline", func(t *testing.T) {
		l := newLibrary(t, enOnly, mediawrite.Deps{}, countStages)
		cues, err := timing.ParseCues([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		d := delivery(t, l.inst, "")
		d.Cues = cues
		rec, err := l.w.Deliver(t.Context(), d)
		if err != nil {
			t.Fatal(err)
		}
		check(t, l, mediawrite.RefOf(rec), expectOf(rec))
	})
}
