package statedb

import (
	"slices"
	"testing"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

const ptThenEn = "default:\n  - tag: pt-BR\n  - tag: en\n"

// countsLibrary is show's season 1 under ptThenEn: S01E01 at its own file
// holding an English subtitle, locked; S01E02 at its own file holding a
// pt-AO one; S01E03 with no video; S01E04 at its own file, excluded. It
// returns the store, the root and S01E02's instance.
func countsLibrary(t *testing.T) (*Store, media.RootKey, InstanceID) {
	t.Helper()
	s := covLib(t, tempPath(t), pol(t, ptThenEn))
	excluded := episode("e4", 1, 4, "/tv/Show/S01E04.mkv")
	excluded.Excluded = true
	observe(t, s, "sonarr", wholePass(t, s, "sonarr"), show,
		episode("e1", 1, 1, "/tv/Show/S01E01.mkv"), episode("e2", 1, 2, "/tv/Show/S01E02.mkv"), episode("e3", 1, 3, ""), excluded)
	ids := make([]InstanceID, 0, 3)
	for _, ep := range []int{1, 2, 4} {
		rec, _, _ := holderOf(t, s, epKey(t, showTVDB, 1, ep))
		ids = append(ids, InstanceID(rec.Inst))
	}
	putExternal(t, s, ids[0], local(t, "S01E01.en.srt", "en"))
	putExternal(t, s, ids[1], local(t, "S01E02.pt-AO.srt", "pt-AO"))
	putExternal(t, s, ids[2], local(t, "S01E04.en.srt", "en"))
	setLock(t, s, ids[0], "en", true)
	setLock(t, s, ids[2], "en", true)
	return s, epKey(t, showTVDB, 1, 1).Root(), ids[1]
}

func rootCountsOf(t *testing.T, s *Store, root media.RootKey) []CoverageCount {
	t.Helper()
	row, found, err := s.Root(t.Context(), root)
	if err != nil || !found {
		t.Fatalf("Root(%s): found %v, %v", root, found, err)
	}
	rows, _, err := s.Roots(t.Context(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(rows, func(r RootRow) bool { return r.Key == root }); i < 0 || !slices.Equal(rows[i].Counts, row.Counts) {
		t.Errorf("Roots lists %s with other counts than Root: %+v", root, rows)
	}
	return row.Counts
}

func TestRootCounts(t *testing.T) {
	s, root, e2 := countsLibrary(t)
	pt, en := mustTag(t, "pt-BR"), mustTag(t, "en")
	std := language.Standard
	want := []CoverageCount{
		{Target: pt, Variant: std, Items: 2, Substitute: 1, Missing: 1},
		{Target: en, Variant: std, Items: 2, Exact: 1, Missing: 1, Locked: 1},
	}
	if got := rootCountsOf(t, s, root); !slices.Equal(got, want) {
		t.Errorf("counts = %+v, want %+v (no-video and excluded items uncounted, Targets order)", got, want)
	}

	t.Run("a Missing pair turning Exact moves the counts in its write", func(t *testing.T) {
		head := headRev(t, s)
		putExternal(t, s, e2, local(t, "S01E02.pt-AO.srt", "pt-AO"), local(t, "S01E02.en.srt", "en"))
		if got := headRev(t, s); got != head+1 {
			t.Fatalf("PutExternal took %d revisions, want 1", got-head)
		}
		want[1].Exact, want[1].Missing = 2, 0
		if got := rootCountsOf(t, s, root); !slices.Equal(got, want) {
			t.Errorf("counts = %+v, want %+v", got, want)
		}
	})
	t.Run("a lock counts whatever the coverage", func(t *testing.T) {
		setLock(t, s, e2, "pt-BR", true)
		want[0].Locked = 1
		if got := rootCountsOf(t, s, root); !slices.Equal(got, want) {
			t.Errorf("counts = %+v, want %+v", got, want)
		}
	})
	t.Run("an exclusion uncounts the item", func(t *testing.T) {
		second := episode("e2", 1, 2, "/tv/Show/S01E02.mkv")
		second.Excluded = true
		excluded := episode("e4", 1, 4, "/tv/Show/S01E04.mkv")
		excluded.Excluded = true
		observe(t, s, "sonarr", wholePass(t, s, "sonarr"), show,
			episode("e1", 1, 1, "/tv/Show/S01E01.mkv"), second, episode("e3", 1, 3, ""), excluded)
		want := []CoverageCount{
			{Target: pt, Variant: std, Items: 1, Missing: 1},
			{Target: en, Variant: std, Items: 1, Exact: 1, Locked: 1},
		}
		if got := rootCountsOf(t, s, root); !slices.Equal(got, want) {
			t.Errorf("counts = %+v, want %+v", got, want)
		}
	})
	t.Run("an owner removal leaves no counted item", func(t *testing.T) {
		activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: pol(t, ptThenEn)})
		if row, found := getRow(t, s, s.t.counts, root); found {
			t.Errorf("an ownerless root keeps counts %+v", row)
		}
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: pol(t, ptThenEn)})
		observe(t, s, "sonarr", wholePass(t, s, "sonarr"), show, episode("e1", 1, 1, ""), episode("e2", 1, 2, ""))
		if rec, inst, _ := holderOf(t, s, epKey(t, showTVDB, 1, 1)); rec.Inst == 0 || inst.Owner != "" {
			t.Fatalf("the returning no-video report left the record on instance %d owned by %q, want the ownerless one", rec.Inst, inst.Owner)
		}
		if row, found := getRow(t, s, s.t.counts, root); found {
			t.Errorf("items of an ownerless instance whose sightings returned are counted: %+v", row)
		}
	})
}

func TestRootCountsFollowPolicyOrder(t *testing.T) {
	s, root, _ := countsLibrary(t)
	order := func(t *testing.T, first, second string) {
		t.Helper()
		got := rootCountsOf(t, s, root)
		if len(got) != 2 || got[0].Target != mustTag(t, first) || got[1].Target != mustTag(t, second) {
			t.Errorf("counts = %+v, want %s then %s", got, first, second)
		}
	}
	order(t, "pt-BR", "en")
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: pol(t, "default:\n  - tag: en\n  - tag: pt-BR\n")})
	order(t, "en", "pt-BR")
}

func TestInspectRepairsCounts(t *testing.T) {
	s, root, _ := countsLibrary(t)
	want, _ := getRow(t, s, s.t.counts, root)
	stray := epKey(t, 999, 1, 1).Root()
	wrong := countsRow{Pairs: []countPair{{Tag: rowTag{t: mustTag(t, "en")}, Variant: uint8(language.Standard), Items: 1, Exact: 1}}}
	update(t, s, func(tx *bolttable.Tx) error {
		if err := s.t.counts.Put(tx, root, &wrong); err != nil {
			return err
		}
		return s.t.counts.Put(tx, stray, &wrong)
	})
	in := inspect(t, s, InspectOptions{Verify: true})
	if got := mismatchNamed(&in, countsName); got != (Mismatch{Name: countsName, Missing: 1, Extra: 2}) {
		t.Errorf("Verify reported %+v, want one missing, two extra", got)
	}
	in = inspect(t, s, InspectOptions{Repair: true})
	if got := mismatchNamed(&in, countsName); got != (Mismatch{Name: countsName, Missing: 1, Extra: 2, Repaired: true}) {
		t.Errorf("Repair reported %+v", got)
	}
	if got, _ := getRow(t, s, s.t.counts, root); !slices.Equal(got.Pairs, want.Pairs) {
		t.Errorf("Repair left %+v, want %+v", got, want)
	}
	if _, found := getRow(t, s, s.t.counts, stray); found {
		t.Error("Repair kept the counts of a root with no item")
	}
	if in := inspect(t, s, InspectOptions{Verify: true}); len(in.Mismatches) != 0 {
		t.Errorf("Verify after Repair = %+v", in.Mismatches)
	}
}
