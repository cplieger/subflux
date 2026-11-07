package statedb

import (
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

func storedWorkOf(t *testing.T, s *Store, k media.Key) (workRow, bool) {
	t.Helper()
	rec, found := getRow(t, s, s.t.items, k)
	if !found {
		t.Fatalf("no record for %s", k)
	}
	return storedWork(t, s, InstanceID(rec.Inst))
}

func TestFinishPassKeepRetiresNothingUnderKept(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	rp, rq, rr := series("p", 100), series("q", 200), series("r", 300)
	all := []report{
		{rp, []mediasource.Item{episode("p1", 1, 1, "/tv/P/e1.mkv")}},
		{rq, []mediasource.Item{episode("q1", 1, 1, "/tv/Q/e1.mkv")}},
		{rr, []mediasource.Item{episode("r1", 1, 1, "/tv/R/e1.mkv")}},
	}
	passOver(t, s, "sonarr", all...)
	kp, kq, kr := epKey(t, 100, 1, 1), epKey(t, 200, 1, 1), epKey(t, 300, 1, 1)
	recP, _, _ := holderOf(t, s, kp)
	if err := s.AckWork(t.Context(), nextWork(t, s, 10)); err != nil {
		t.Fatal(err)
	}

	p := wholePass(t, s, "sonarr")
	observe(t, s, "sonarr", p, rq, all[1].items...)
	if _, err := s.FinishPass(t.Context(), "sonarr", p, true, mediasource.Cursor{}, []mediasource.NativeRef{rp.Native}); err != nil {
		t.Fatalf("FinishPass(keep p): %v", err)
	}
	if _, _, found := holderOf(t, s, kr); found {
		t.Error("the unlisted root r kept its item")
	}
	if _, found := getRow(t, s, s.t.roots, rootRef{Source: "sonarr", Root: rr.Native}); found {
		t.Error("the unlisted root r kept its root record")
	}
	if got, _, found := holderOf(t, s, kp); !found || got != recP {
		t.Errorf("the kept root's record %+v found %v, want unchanged %+v", got, found, recP)
	}
	if _, found := getRow(t, s, s.t.roots, rootRef{Source: "sonarr", Root: rp.Native}); !found {
		t.Error("the kept root lost its root record")
	}
	if _, found := sightingOf(t, s, "sonarr", rp, "p1"); !found {
		t.Error("the kept root lost its sighting")
	}
	if _, found := getRow(t, s, s.t.instances, InstanceID(recP.Inst)); !found {
		t.Error("the kept root lost its instance")
	}
	if w, found := storedWorkOf(t, s, kq); !found || w.Cause != uint8(Scan) {
		t.Errorf("q's instance work %+v found %v, want Scan", w, found)
	}
	if _, found := storedWorkOf(t, s, kp); found {
		t.Error("the kept root's instance was queued for Scan")
	}
}

func TestFinishPassKeepBound(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	r := series("1", 100)
	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv")}})
	k := epKey(t, 100, 1, 1)
	many := make([]mediasource.NativeRef, maxKeep+1)
	for i := range many {
		many[i] = mediasource.NativeRef{ID: mediasource.NativeID(strconv.Itoa(i + 1)), Kind: media.Episode}
	}

	whole := wholePass(t, s, "sonarr")
	cases := []struct {
		name     string
		complete bool
		keep     []mediasource.NativeRef
	}{
		{"33 refs", true, many},
		{"incomplete pass", false, []mediasource.NativeRef{r.Native}},
		{"incomplete pass, empty keep", false, []mediasource.NativeRef{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head := headRev(t, s)
			if _, err := s.FinishPass(t.Context(), "sonarr", whole, tc.complete, mediasource.Cursor{}, tc.keep); err == nil || errors.Is(err, ErrPassUnknown) {
				t.Fatalf("FinishPass: %v, want a plain refusal", err)
			}
			if got := headRev(t, s); got != head {
				t.Errorf("the refusal moved the head from %d to %d", head, got)
			}
			if _, found := getRow(t, s, s.t.passes, "sonarr"); !found {
				t.Error("the refusal closed the pass")
			}
		})
	}
	t.Run("change batch", func(t *testing.T) {
		p := batchPass(t, s, "sonarr", r)
		if _, err := s.FinishPass(t.Context(), "sonarr", p, true, mediasource.Cursor{}, []mediasource.NativeRef{r.Native}); err == nil || errors.Is(err, ErrPassUnknown) {
			t.Fatalf("FinishPass of a batch with keep: %v, want a plain refusal", err)
		}
		if _, found := getRow(t, s, s.t.passes, "sonarr"); !found {
			t.Error("the refusal closed the pass")
		}
		if _, err := s.FinishPass(t.Context(), "sonarr", p, true, mediasource.Cursor{}, nil); err != nil {
			t.Fatalf("FinishPass after the refusal: %v", err)
		}
	})
	if _, _, found := holderOf(t, s, k); found {
		t.Error("the batch observing nothing kept the item it scoped")
	}
}

func TestEmptyBatchRetiresNothing(t *testing.T) {
	path := t.TempDir() + "/subflux.bolt"
	s, _ := libraryStore(t, path, "sonarr")
	r := series("1", 100)
	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv")}})
	p, err := s.BeginPass(t.Context(), "sonarr", PassScope{Roots: []mediasource.NativeRef{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, _ = libraryStore(t, path, "sonarr")
	finish(t, s, "sonarr", p)
	if _, _, found := holderOf(t, s, epKey(t, 100, 1, 1)); !found {
		t.Error("an empty change batch retired the source's item, as a whole pass would")
	}
}

func TestEnqueueSkipsRetiredInstance(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	r := series("1", 100)
	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv")}})
	rec, _, _ := holderOf(t, s, epKey(t, 100, 1, 1))
	if err := s.AckWork(t.Context(), nextWork(t, s, 10)); err != nil {
		t.Fatal(err)
	}
	passOver(t, s, "sonarr", report{r, nil})
	drainWorkReady(s)
	enqueueWork(t, s, Item, InstanceID(rec.Inst))
	if _, found := storedWork(t, s, InstanceID(rec.Inst)); found {
		t.Error("work was queued for the retired instance")
	}
	if drainWorkReady(s) {
		t.Error("skipping the retired instance signalled WorkReady")
	}
}

func TestChangeBatchEnqueuesImport(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	r, other := series("1", 100), series("2", 200)
	e1 := episode("1", 1, 1, "/tv/S/e1.mkv")
	passOver(t, s, "sonarr", report{r, []mediasource.Item{e1}}, report{other, []mediasource.Item{episode("9", 1, 1, "/tv/T/e1.mkv")}})
	if err := s.AckWork(t.Context(), nextWork(t, s, 10)); err != nil {
		t.Fatal(err)
	}
	drainWorkReady(s)
	p := batchPass(t, s, "sonarr", r)
	observe(t, s, "sonarr", p, r, e1, episode("2", 1, 2, "/tv/S/e2.mkv"), episode("3", 1, 3, ""))
	if !drainWorkReady(s) {
		t.Error("the batch's Import work did not signal WorkReady")
	}
	for _, ep := range []int{1, 2} {
		if w, found := storedWorkOf(t, s, epKey(t, 100, 1, ep)); !found || w.Cause != uint8(Import) {
			t.Errorf("episode %d: work %+v found %v, want Import", ep, w, found)
		}
	}
	if _, found := storedWorkOf(t, s, epKey(t, 200, 1, 1)); found {
		t.Error("an instance outside the batch was queued")
	}
	finish(t, s, "sonarr", p)
	if ws := nextWork(t, s, 10); len(ws) != 2 {
		t.Errorf("after the batch %d rows queued, want the two Import rows", len(ws))
	}

	q := wholePass(t, s, "sonarr")
	if err := s.AckWork(t.Context(), nextWork(t, s, 10)); err != nil {
		t.Fatal(err)
	}
	observe(t, s, "sonarr", q, r, e1)
	if w, found := storedWorkOf(t, s, epKey(t, 100, 1, 1)); found {
		t.Errorf("a whole pass's observation queued %+v", w)
	}
}

func TestWholeSourcePassEnqueuesScan(t *testing.T) {
	s, _ := newLibrary(t, "sonarr", "radarr")
	r := series("1", 100)
	movie := mediasource.Root{Native: mediasource.NativeRef{ID: "7", Kind: media.Movie}, External: media.ExternalIDs{TMDB: 603}}
	passOver(t, s, "radarr", report{movie, []mediasource.Item{{Native: "m", VideoPath: "/movies/m.mkv"}}})
	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv"), episode("2", 1, 2, "/tv/S/e2.mkv")}})
	if err := s.AckWork(t.Context(), nextWork(t, s, 10)); err != nil {
		t.Fatal(err)
	}
	b := batchPass(t, s, "sonarr", r)
	observe(t, s, "sonarr", b, r, episode("1", 1, 1, "/tv/S/e1.mkv"), episode("2", 1, 2, "/tv/S/e2.mkv"))
	if err := s.AckWork(t.Context(), nextWork(t, s, 10)); err != nil {
		t.Fatal(err)
	}
	finish(t, s, "sonarr", b)
	if ws := nextWork(t, s, 10); len(ws) != 0 {
		t.Errorf("finishing a batch queued %d rows", len(ws))
	}

	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv"), episode("2", 1, 2, "/tv/S/e2.mkv")}})
	ws := nextWork(t, s, 10)
	var want []InstanceID
	for _, ep := range []int{1, 2} {
		rec, _, _ := holderOf(t, s, epKey(t, 100, 1, ep))
		want = append(want, InstanceID(rec.Inst))
	}
	got := insts(ws)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("queued %v, want sonarr's instances %v", got, want)
	}
	for _, w := range ws {
		if w.Cause != Scan {
			t.Errorf("instance %d queued for %d, want Scan", w.Inst, w.Cause)
		}
	}
}

func TestFinishPassStoresCursor(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	if _, found, err := s.SourceCursor(t.Context(), "sonarr"); found || err != nil {
		t.Fatalf("SourceCursor before any pass: %v, %v", found, err)
	}
	var c mediasource.Cursor
	if err := c.UnmarshalBinary([]byte("at-42")); err != nil {
		t.Fatal(err)
	}
	p := wholePass(t, s, "sonarr")
	if _, err := s.FinishPass(t.Context(), "sonarr", p, true, c, nil); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.SourceCursor(t.Context(), "sonarr")
	if err != nil || !found || string(must(got.MarshalBinary())) != "at-42" {
		t.Errorf("SourceCursor: %q, %v, %v; want at-42", must(got.MarshalBinary()), found, err)
	}
	if _, err := s.FinishPass(t.Context(), "sonarr", p, true, c, nil); !errors.Is(err, ErrPassUnknown) {
		t.Errorf("a second FinishPass of the pass: %v, want ErrPassUnknown", err)
	}
}
