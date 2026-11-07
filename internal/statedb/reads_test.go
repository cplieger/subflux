package statedb

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

func movie(id string, tmdb uint64) mediasource.Root {
	return mediasource.Root{
		Native:   mediasource.NativeRef{ID: mediasource.NativeID(id), Kind: media.Movie},
		Title:    "Movie " + id,
		External: media.ExternalIDs{TMDB: tmdb},
	}
}

func film(native, path string) mediasource.Item {
	return mediasource.Item{Native: mediasource.NativeID(native), Title: "Film " + native, VideoPath: path, SceneName: filepath.Base(path)}
}

// allRoots pages Roots at limit to its end.
func allRoots(t *testing.T, s *Store, limit int) []RootRow {
	t.Helper()
	var out []RootRow
	var c ListCursor
	for range 1000 {
		page, next, err := s.Roots(t.Context(), c, limit)
		if err != nil {
			t.Fatalf("Roots(%q, %d): %v", c, limit, err)
		}
		out = append(out, page...)
		if next == "" {
			return out
		}
		c = next
	}
	t.Fatal("Roots never answered a last page")
	return nil
}

func allItems(t *testing.T, s *Store, root media.RootKey, limit int) []ItemRow {
	t.Helper()
	var out []ItemRow
	var c ListCursor
	for range 1000 {
		page, next, err := s.Items(t.Context(), root, c, limit)
		if err != nil {
			t.Fatalf("Items(%s, %q, %d): %v", root, c, limit, err)
		}
		out = append(out, page...)
		if next == "" {
			return out
		}
		c = next
	}
	t.Fatal("Items never answered a last page")
	return nil
}

func itemKeys(rows []ItemRow) []media.Key {
	ks := make([]media.Key, 0, len(rows))
	for i := range rows {
		ks = append(ks, rows[i].Key)
	}
	return ks
}

func itemByKey(t *testing.T, s *Store, k media.Key) (ItemRow, bool) {
	t.Helper()
	row, found, err := s.ItemRow(t.Context(), k)
	if err != nil {
		t.Fatalf("ItemRow(%s): %v", k, err)
	}
	return row, found
}

func rootByKey(t *testing.T, s *Store, r media.RootKey) (RootRow, bool) {
	t.Helper()
	row, found, err := s.Root(t.Context(), r)
	if err != nil {
		t.Fatalf("Root(%s): %v", r, err)
	}
	return row, found
}

func instOf(t *testing.T, s *Store, k media.Key) InstanceID {
	t.Helper()
	row, found := itemByKey(t, s, k)
	if !found || row.Inst == 0 {
		t.Fatalf("%s has no listed instance", k)
	}
	return row.Inst
}

func TestSharedRootSurvivesLowestOwnerRemoval(t *testing.T) {
	s, _ := newLibrary(t, "sonarr", "sonarr-4k")
	a, b := series("1", 100), series("7", 100)
	a.Title, b.Title = "A title", "B title"
	passOver(t, s, "sonarr", report{a, []mediasource.Item{episode("11", 1, 1, "/tv/a/S01E01.mkv")}})
	passOver(t, s, "sonarr-4k", report{b, []mediasource.Item{episode("72", 1, 2, "/tv/b/S01E02.mkv")}})
	r := epKey(t, 100, 1, 1).Root()
	if rows := allRoots(t, s, 10); len(rows) != 1 || rows[0].Source != "sonarr" {
		t.Fatalf("before the removal Roots = %+v, want R from sonarr, the lowest key's source", rows)
	}
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr-4k"}, Languages: policyOf(t, "en")})

	rows := allRoots(t, s, 10)
	if len(rows) != 1 {
		t.Fatalf("Roots = %+v, want R alone", rows)
	}
	if got := rows[0]; got.Key != r || got.Source != "sonarr-4k" || got.Root.Title != "B title" || got.Root.Native != b.Native {
		t.Errorf("Roots row = %+v, want R with sonarr-4k's display fields", got)
	}
	if got := itemKeys(allItems(t, s, r, 10)); !slices.Equal(got, []media.Key{epKey(t, 100, 1, 2)}) {
		t.Errorf("Items(R) = %v, want sonarr-4k's S01E02 only", got)
	}
}

func TestRootAndItemRowByKey(t *testing.T) {
	s, _ := newLibrary(t, "radarr", "sonarr", "sonarr-old")
	passOver(t, s, "radarr", report{movie("5", 5), []mediasource.Item{film("5", "/m/5.mkv")}})
	passOver(t, s, "sonarr",
		report{series("1", 100), []mediasource.Item{
			episode("11", 1, 1, "/tv/100/E01.mkv"), episode("12", 1, 2, "/tv/100/E02.mkv"), episode("13", 1, 3, ""),
		}},
		report{series("2", 200), []mediasource.Item{episode("21", 1, 1, "/tv/200/E01.mkv")}},
	)
	passOver(t, s, "sonarr-old", report{series("3", 300), []mediasource.Item{episode("31", 1, 1, "/tv/300/E01.mkv")}})
	historied := epKey(t, 200, 1, 1)
	recordAttempts(t, s, attemptOf(instOf(t, s, historied), false, historied))
	ownerless := epKey(t, 300, 1, 1)
	activate(t, s, Activation{Sources: []media.SourceID{"radarr", "sonarr"}, Languages: policyOf(t, "en")})

	roots := allRoots(t, s, 1)
	if len(roots) != 3 {
		t.Fatalf("Roots = %+v, want tmdb-5, tvdb-100 and tvdb-200", roots)
	}
	for _, want := range roots {
		t.Run("root "+want.Key.String(), func(t *testing.T) {
			got, found := rootByKey(t, s, want.Key)
			if !found || !reflect.DeepEqual(got, want) {
				t.Errorf("Root = %+v, %v; want the Roots row %+v", got, found, want)
			}
			if want.HasHistory != (want.Key == historied.Root()) {
				t.Errorf("HasHistory = %v", want.HasHistory)
			}
			for _, item := range allItems(t, s, want.Key, 1) {
				got, found := itemByKey(t, s, item.Key)
				if !found || !reflect.DeepEqual(got, item) {
					t.Errorf("ItemRow(%s) = %+v, %v; want the Items row %+v", item.Key, got, found, item)
				}
				if item.HasHistory != (item.Key == historied) {
					t.Errorf("ItemRow(%s).HasHistory = %v", item.Key, item.HasHistory)
				}
				if len(item.Coverage) != 1 || item.Coverage[0].Target != enTag {
					t.Errorf("ItemRow(%s).Coverage = %+v, want the en pair", item.Key, item.Coverage)
				}
			}
		})
	}
	t.Run("absent and ownerless keys", func(t *testing.T) {
		for _, k := range []media.Key{epKey(t, 999, 1, 1), ownerless} {
			if _, found := itemByKey(t, s, k); found {
				t.Errorf("ItemRow(%s) found", k)
			}
			if _, found := rootByKey(t, s, k.Root()); found {
				t.Errorf("Root(%s) found", k.Root())
			}
		}
	})
}

// An ItemRow that pages its root's records allocates per record, so its
// allocations grow with the root; reading the one record keeps them flat.
func TestItemRowReadsOneRecord(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	big := make([]mediasource.Item, 0, 1000)
	for e := 1; e <= 1000; e++ {
		big = append(big, episode(fmt.Sprint(e), 1, e, fmt.Sprintf("/tv/big/E%04d.mkv", e)))
	}
	passOver(t, s, "sonarr",
		report{series("1", 100), big},
		report{series("2", 200), []mediasource.Item{episode("1", 1, 1, "/tv/small/E01.mkv")}},
	)
	allocs := func(k media.Key) float64 {
		return testing.AllocsPerRun(20, func() {
			if _, found, err := s.ItemRow(t.Context(), k); err != nil || !found {
				t.Fatalf("ItemRow(%s) = %v, %v", k, found, err)
			}
		})
	}
	small, inBig := allocs(epKey(t, 200, 1, 1)), allocs(epKey(t, 100, 1, 500))
	if inBig > 2*small {
		t.Errorf("ItemRow in a 1,000-episode root allocates %.0f times, against %.0f in a one-episode root", inBig, small)
	}
}

func TestItemOwnerlessFalse(t *testing.T) {
	s, _ := newLibrary(t, "sonarr", "radarr")
	passOver(t, s, "radarr", report{movie("9", 9), []mediasource.Item{film("9", "/m/9.mkv")}})
	k := movieKey(t, 9)
	id := instOf(t, s, k)
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: policyOf(t, "en")})

	if _, found := itemByKey(t, s, k); found {
		t.Error("ItemRow found an ownerless key")
	}
	got, found, err := s.Instance(t.Context(), id)
	want := Instance{ID: id, VideoPath: "/m/9.mkv", SceneName: "9.mkv", Items: []media.Key{k}}
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Errorf("Instance = %+v, %v, %v; want %+v, ownerless", got, found, err, want)
	}
	if ts, err := s.RootTargets(t.Context(), k.Root()); err != nil || len(ts) != 0 {
		t.Errorf("RootTargets = %+v, %v; want none", ts, err)
	}
}

func TestRootTargetsPerSource(t *testing.T) {
	s, _ := newLibrary(t, "sonarr", "sonarr-4k")
	passOver(t, s, "sonarr-4k", report{series("9", 100), []mediasource.Item{episode("1", 1, 1, "/tv/4k/E01.mkv")}})
	passOver(t, s, "sonarr", report{series("1", 100), []mediasource.Item{
		episode("2", 1, 2, "/tv/hd/b.mkv"), episode("3", 1, 3, "/tv/hd/b.mkv"),
		episode("4", 1, 4, "/tv/hd/a.mkv"), episode("5", 1, 5, ""),
	}}, report{series("8", 800), []mediasource.Item{episode("1", 1, 1, "/tv/800/E01.mkv")}})

	got, err := s.RootTargets(t.Context(), epKey(t, 100, 1, 1).Root())
	if err != nil {
		t.Fatal(err)
	}
	want := []RootTarget{
		{Source: "sonarr", Root: series("1", 100).Native, VideoPaths: []string{"/tv/hd/a.mkv", "/tv/hd/b.mkv"}},
		{Source: "sonarr-4k", Root: series("9", 100).Native, VideoPaths: []string{"/tv/4k/E01.mkv"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RootTargets = %+v, want %+v", got, want)
	}
}

func TestInstanceAndFiles(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	passOver(t, s, "sonarr", report{series("1", 100), []mediasource.Item{episode("1", 1, 1, "/tv/S/S01E01.mkv")}})
	k := epKey(t, 100, 1, 1)
	id := instOf(t, s, k)

	got, found, err := s.Instance(t.Context(), id)
	want := Instance{ID: id, Source: "sonarr", VideoPath: "/tv/S/S01E01.mkv", SceneName: "S01E01.mkv", Items: []media.Key{k}}
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Errorf("Instance = %+v, %v, %v; want %+v", got, found, err, want)
	}
	deliver(t, s, pendingDeliver(t, id, "S01E01.b.en.srt", 2, k))
	deliver(t, s, pendingDeliver(t, id, "S01E01.a.en.srt", 1, k))
	files, err := s.Files(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
		if f.Inst != id || f.Generation != 1 || f.Tag != enTag || f.Provenance == nil {
			t.Errorf("record %+v, want instance %d at generation 1 in en with its provenance", f, id)
		}
	}
	if !slices.Equal(names, []string{"S01E01.a.en.srt", "S01E01.b.en.srt"}) {
		t.Errorf("Files names = %v, want both by name", names)
	}
	if _, found, err := s.Instance(t.Context(), id+1); err != nil || found {
		t.Errorf("Instance(unknown) = %v, %v", found, err)
	}
	if fs, err := s.Files(t.Context(), id+1); err != nil || len(fs) != 0 {
		t.Errorf("Files(unknown) = %+v, %v", fs, err)
	}
}

func TestUnidentifiedPages(t *testing.T) {
	s, _ := newLibrary(t, "radarr", "sonarr")
	passOver(t, s, "sonarr", report{series("5", 0), []mediasource.Item{episode("52", 1, 2, "/tv/x/E02.mkv"), episode("51", 1, 1, "/tv/x/E01.mkv")}})
	passOver(t, s, "radarr", report{movie("3", 0), []mediasource.Item{film("31", "/m/x.mkv")}})

	type row struct {
		src        media.SourceID
		root, item mediasource.NativeID
		title      string
	}
	var pages [][]row
	var c ListCursor
	for range 10 {
		page, next, err := s.Unidentified(t.Context(), c, 2)
		if err != nil {
			t.Fatalf("Unidentified(%q): %v", c, err)
		}
		var rs []row
		for _, u := range page {
			rs = append(rs, row{u.Source, u.Root.Native.ID, u.Item.Native, u.Root.Title})
		}
		pages = append(pages, rs)
		if c = next; c == "" {
			break
		}
	}
	want := [][]row{
		{{"radarr", "3", "31", "Movie 3"}, {"sonarr", "5", "51", "Series 5"}},
		{{"sonarr", "5", "52", "Series 5"}},
	}
	if !reflect.DeepEqual(pages, want) {
		t.Errorf("Unidentified pages = %v, want %v", pages, want)
	}
}

func TestListLimitBounds(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	root := epKey(t, 100, 1, 1).Root()
	lists := []struct {
		name string
		page func(limit int) error
	}{
		{"Roots", func(n int) error { _, _, err := s.Roots(t.Context(), "", n); return err }},
		{"Items", func(n int) error { _, _, err := s.Items(t.Context(), root, "", n); return err }},
		{"Unidentified", func(n int) error { _, _, err := s.Unidentified(t.Context(), "", n); return err }},
		{"History", func(n int) error { _, _, err := s.History(t.Context(), HistoryScope{}, "", n); return err }},
	}
	for _, l := range lists {
		t.Run(l.name, func(t *testing.T) {
			for _, n := range []int{0, -1, 501} {
				if err := l.page(n); err == nil || errors.Is(err, ErrCursorInvalid) {
					t.Errorf("limit %d: %v, want a plain error", n, err)
				}
			}
			for _, n := range []int{1, 500} {
				if err := l.page(n); err != nil {
					t.Errorf("limit %d: %v", n, err)
				}
			}
		})
	}
}

// A live item record always names a stored sighting, root record and
// instance; a read finding one gone fails rather than answering a guess.
func TestReadsRefuseDanglingRecords(t *testing.T) {
	setup := func(t *testing.T) (*Store, media.Key, itemRow) {
		t.Helper()
		s, _ := newLibrary(t, "sonarr")
		passOver(t, s, "sonarr", report{series("1", 100), []mediasource.Item{episode("1", 1, 1, "/tv/S/E01.mkv")}})
		k := epKey(t, 100, 1, 1)
		rec, _ := getRow(t, s, s.t.items, k)
		return s, k, rec
	}
	reads := func(s *Store, k media.Key) map[string]error {
		_, _, _, item := s.Item(t.Context(), k)
		_, _, itemRow := s.ItemRow(t.Context(), k)
		_, _, items := s.Items(t.Context(), k.Root(), "", 10)
		_, _, root := s.Root(t.Context(), k.Root())
		_, _, roots := s.Roots(t.Context(), "", 10)
		_, targets := s.RootTargets(t.Context(), k.Root())
		return map[string]error{"Item": item, "ItemRow": itemRow, "Items": items, "Root": root, "Roots": roots, "RootTargets": targets}
	}
	t.Run("instance gone", func(t *testing.T) {
		s, k, rec := setup(t)
		rec.Inst = 99
		update(t, s, func(tx *bolttable.Tx) error { return s.t.items.Put(tx, k, &rec) })
		for name, err := range reads(s, k) {
			if err == nil {
				t.Errorf("%s answered no error", name)
			}
		}
	})
	t.Run("sighting gone", func(t *testing.T) {
		s, k, rec := setup(t)
		update(t, s, func(tx *bolttable.Tx) error { _, err := s.t.sightings.Delete(tx, rec.ref()); return err })
		errs := reads(s, k)
		for _, name := range []string{"Item", "ItemRow", "Items"} {
			if errs[name] == nil {
				t.Errorf("%s answered no error", name)
			}
		}
	})
	t.Run("root record gone", func(t *testing.T) {
		s, k, rec := setup(t)
		update(t, s, func(tx *bolttable.Tx) error { _, err := s.t.roots.Delete(tx, rec.ref().rootRef); return err })
		errs := reads(s, k)
		for _, name := range []string{"Item", "Root", "Roots"} {
			if errs[name] == nil {
				t.Errorf("%s answered no error", name)
			}
		}
	})
}
