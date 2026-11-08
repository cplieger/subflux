package statedb

import (
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/rank"
)

var enTag, _ = language.Parse("en")

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// libraryStore is a store at path whose clock the test moves, with sources
// active.
func libraryStore(t *testing.T, path string, sources ...media.SourceID) (*Store, *testClock) {
	t.Helper()
	c := &testClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	cfg := productionConfig()
	cfg.now = c.Now
	s := mustOpen(t, path, cfg)
	activate(t, s, Activation{Sources: sources, Languages: policyOf(t, "en")})
	return s, c
}

func newLibrary(t *testing.T, sources ...media.SourceID) (*Store, *testClock) {
	t.Helper()
	return libraryStore(t, filepath.Join(t.TempDir(), "subflux.bolt"), sources...)
}

func series(id string, tvdb uint64) mediasource.Root {
	return mediasource.Root{
		Native:   mediasource.NativeRef{ID: mediasource.NativeID(id), Kind: media.Episode},
		Title:    "Series " + id,
		External: media.ExternalIDs{TVDB: tvdb},
	}
}

func episode(native string, season, ep int, path string) mediasource.Item {
	it := mediasource.Item{
		Native:    mediasource.NativeID(native),
		Title:     "Episode " + native,
		VideoPath: path,
		Numbering: media.Numbering{Scheme: media.TVDB, Season: season, Episode: ep},
	}
	if path != "" {
		it.SceneName = filepath.Base(path)
	}
	return it
}

func epKey(t *testing.T, tvdb uint64, season, ep int) media.Key {
	t.Helper()
	k, ok := media.Choose(media.Episode, media.ExternalIDs{TVDB: tvdb}, media.Numbering{Scheme: media.TVDB, Season: season, Episode: ep})
	if !ok {
		t.Fatalf("no key for tvdb %d s%d e%d", tvdb, season, ep)
	}
	return k
}

func wholePass(t *testing.T, s *Store, src media.SourceID) PassID {
	t.Helper()
	p, err := s.BeginPass(t.Context(), src, PassScope{})
	if err != nil {
		t.Fatalf("BeginPass(%s): %v", src, err)
	}
	return p
}

func batchPass(t *testing.T, s *Store, src media.SourceID, roots ...mediasource.Root) PassID {
	t.Helper()
	refs := []mediasource.NativeRef{}
	for _, r := range roots {
		refs = append(refs, r.Native)
	}
	p, err := s.BeginPass(t.Context(), src, PassScope{Roots: refs})
	if err != nil {
		t.Fatalf("BeginPass(%s, batch): %v", src, err)
	}
	return p
}

func observe(t *testing.T, s *Store, src media.SourceID, p PassID, root mediasource.Root, items ...mediasource.Item) {
	t.Helper()
	if err := s.ObserveItems(t.Context(), src, p, root, items); err != nil {
		t.Fatalf("ObserveItems(%s, %s): %v", src, root.Native.ID, err)
	}
}

func finish(t *testing.T, s *Store, src media.SourceID, p PassID) []media.SourceID {
	t.Helper()
	owners, err := s.FinishPass(t.Context(), src, p, true, mediasource.Cursor{}, nil)
	if err != nil {
		t.Fatalf("FinishPass(%s): %v", src, err)
	}
	return owners
}

// report is one root of a pass and the items it lists.
type report struct {
	root  mediasource.Root
	items []mediasource.Item
}

// passOver runs one complete whole pass of src reporting rs.
func passOver(t *testing.T, s *Store, src media.SourceID, rs ...report) []media.SourceID {
	t.Helper()
	p := wholePass(t, s, src)
	for _, r := range rs {
		observe(t, s, src, p, r.root, r.items...)
	}
	return finish(t, s, src, p)
}

// holderOf answers k's item record and the instance it names.
func holderOf(t *testing.T, s *Store, k media.Key) (itemRow, instanceRow, bool) {
	t.Helper()
	rec, found := getRow(t, s, s.t.items, k)
	if !found {
		return itemRow{}, instanceRow{}, false
	}
	inst, _ := getRow(t, s, s.t.instances, InstanceID(rec.Inst))
	return rec, inst, true
}

func keysOf(inst *instanceRow) []media.Key {
	ks := make([]media.Key, 0, len(inst.Items))
	for _, x := range inst.Items {
		ks = append(ks, x.k)
	}
	return ks
}

func putLock(s *Store, k media.Key, at time.Time) func(*bolttable.Tx) error {
	return func(tx *bolttable.Tx) error {
		return s.t.locks.Put(tx, lockKey{Key: k, Tag: enTag, Variant: language.Standard}, &lockRow{At: at})
	}
}

func putDownload(s *Store, seq uint64, inst InstanceID, k media.Key) func(*bolttable.Tx) error {
	return func(tx *bolttable.Tx) error {
		row := downloadRow{
			Target: rowTag{t: enTag}, Items: []rowKey{{k: k}}, Inst: uint64(inst), Variant: uint8(language.Standard), Delivered: true,
			Provenance: provenanceRow{Provider: "opensubtitles", MatchedBy: uint8(rank.Hash), Fit: uint8(rank.Exact)},
		}
		return s.t.downloads.Put(tx, seq, &row)
	}
}

func hasLock(t *testing.T, s *Store, k media.Key) bool {
	t.Helper()
	return countRows(t, s, s.t.locks, k.Bytes()) > 0
}

// downloadBytes is every download row's stored bytes, by sequence.
func downloadBytes(t *testing.T, s *Store) map[uint64]string {
	t.Helper()
	out := map[uint64]string{}
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
		return s.t.downloads.Scan(r, bolttable.Range{}, func(seq uint64, v *downloadRow) (bool, error) {
			b, err := downloadCodec.Append(nil, v)
			out[seq] = string(b)
			return true, err
		})
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func sightingOf(t *testing.T, s *Store, src media.SourceID, root mediasource.Root, item string) (sightingRow, bool) {
	t.Helper()
	return getRow(t, s, s.t.sightings, itemRef{Source: src, Root: root.Native, Item: mediasource.NativeID(item)})
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestObserveRefusals(t *testing.T) {
	r := series("1", 100)
	cases := []struct {
		name  string
		src   media.SourceID
		root  mediasource.Root
		items []mediasource.Item
		stale bool
		want  error
	}{
		{name: "inactive source", src: "radarr", root: r, items: []mediasource.Item{episode("1", 1, 1, "")}, want: ErrSourceInactive},
		{name: "unknown pass", src: "sonarr", root: r, items: []mediasource.Item{episode("1", 1, 1, "")}, stale: true, want: ErrPassUnknown},
		{name: "empty native id", src: "sonarr", root: r, items: []mediasource.Item{episode("", 1, 1, "")}},
		{name: "duplicate native id", src: "sonarr", root: r, items: []mediasource.Item{episode("1", 1, 1, ""), episode("1", 1, 2, "")}},
		{name: "relative path", src: "sonarr", root: r, items: []mediasource.Item{episode("1", 1, 1, "tv/e1.mkv")}},
		{name: "unclean path", src: "sonarr", root: r, items: []mediasource.Item{episode("1", 1, 1, "/tv/../e1.mkv")}},
		{name: "trailing slash", src: "sonarr", root: r, items: []mediasource.Item{episode("1", 1, 1, "/tv/e1/")}},
		{name: "root of no kind", src: "sonarr", root: mediasource.Root{Native: mediasource.NativeRef{ID: "1"}}, items: nil},
		{name: "root with no id", src: "sonarr", root: mediasource.Root{Native: mediasource.NativeRef{Kind: media.Episode}}, items: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newLibrary(t, "sonarr")
			p := wholePass(t, s, "sonarr")
			if tc.stale {
				wholePass(t, s, "sonarr")
			}
			head := headRev(t, s)
			err := s.ObserveItems(t.Context(), tc.src, p, tc.root, tc.items)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("ObserveItems: %v, want a refusal (%v)", err, tc.want)
			}
			if got := headRev(t, s); got != head {
				t.Errorf("the refusal moved the head from %d to %d", head, got)
			}
			if n := countRows(t, s, s.t.stamps, nil) + countRows(t, s, s.t.sightings, nil) + countRows(t, s, s.t.roots, nil); n != 0 {
				t.Errorf("the refusal wrote %d rows", n)
			}
		})
	}
	t.Run("pass of an inactive source", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		if _, err := s.BeginPass(t.Context(), "radarr", PassScope{}); !errors.Is(err, ErrSourceInactive) {
			t.Errorf("BeginPass(radarr): %v, want ErrSourceInactive", err)
		}
		if _, err := s.BeginPass(t.Context(), "sonarr", PassScope{Roots: []mediasource.NativeRef{{ID: "1"}}}); err == nil {
			t.Error("BeginPass over a root of no kind succeeded")
		}
	})
	t.Run("a removed source's open pass", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr", "radarr")
		p := wholePass(t, s, "radarr")
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: policyOf(t, "en")})
		if _, err := s.FinishPass(t.Context(), "radarr", p, true, mediasource.Cursor{}, nil); !errors.Is(err, ErrSourceInactive) {
			t.Errorf("FinishPass(radarr): %v, want ErrSourceInactive", err)
		}
	})
}

func TestUnidentifiedMark(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	r := series("1", 100)
	unknown := episode("1", 0, 0, "/tv/S/x.mkv")
	unknown.Numbering.Scheme = media.None
	step := func(name string, items []mediasource.Item, want bool) {
		t.Helper()
		after := headRev(t, s)
		passOver(t, s, "sonarr", report{r, items})
		d := since(t, s, after)
		if got := slices.Contains(d.Marks, MarkUnidentified); got != want {
			t.Errorf("%s: unidentified mark %v, want %v (marks %v)", name, got, want, d.Marks)
		}
	}
	step("identified", []mediasource.Item{episode("2", 1, 1, "/tv/S/e1.mkv")}, false)
	step("appears", []mediasource.Item{episode("2", 1, 1, "/tv/S/e1.mkv"), unknown}, true)
	renamed := unknown
	renamed.Title = "Renamed"
	step("changes", []mediasource.Item{episode("2", 1, 1, "/tv/S/e1.mkv"), renamed}, true)
	step("goes", []mediasource.Item{episode("2", 1, 1, "/tv/S/e1.mkv")}, true)
	step("stays gone", []mediasource.Item{episode("2", 1, 1, "/tv/S/e1.mkv")}, false)
}

func TestExpiryRewritesRecordToNoVideoSighting(t *testing.T) {
	s, clock := newLibrary(t, "sonarr", "sonarr-b")
	r := series("1", 100)
	k, lone := epKey(t, 100, 1, 1), epKey(t, 100, 1, 2)
	const path = "/tv/S/S01E01-E02.mkv"
	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, path), episode("2", 1, 2, path)}})
	rec, _, _ := holderOf(t, s, k)
	update(t, s, putLock(s, k, time.Unix(1, 0)))
	update(t, s, putLock(s, lone, time.Unix(1, 0)))
	update(t, s, putDownload(s, 1, InstanceID(rec.Inst), lone))
	update(t, s, func(tx *bolttable.Tx) error {
		return s.t.coverage.Put(tx, lone, &coverageRow{Pairs: []coveragePair{{Tag: rowTag{t: enTag}, Variant: uint8(language.Standard), Coverage: uint8(Missing)}}})
	})
	history := downloadBytes(t, s)
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr-b"}, Languages: policyOf(t, "en")})
	b := series("b1", 100)
	b.Title = "B's series"
	wanted := episode("5", 1, 1, "")
	wanted.Title = "B's episode"
	passOver(t, s, "sonarr-b", report{b, []mediasource.Item{wanted}})
	if got, _, _ := holderOf(t, s, k); got.Source != "sonarr" {
		t.Fatalf("before expiry the record names %s, want the ownerless instance's", got.Source)
	}

	clock.advance(31 * 24 * time.Hour)
	passOver(t, s, "sonarr-b", report{b, []mediasource.Item{wanted}})
	if _, found := getRow(t, s, s.t.instances, InstanceID(rec.Inst)); found {
		t.Fatal("the instance did not expire")
	}
	got, found := getRow(t, s, s.t.items, k)
	if !found || got.Source != "sonarr-b" || got.Item != "5" || got.Inst != 0 {
		t.Errorf("record %+v found %v, want B's no-video sighting with no instance", got, found)
	}
	it, root, found, err := s.Item(t.Context(), k)
	if err != nil || !found || it.Title != "B's episode" || root.Title != "B's series" {
		t.Errorf("Item: %q of %q, %v, %v; want B's facts", it.Title, root.Title, found, err)
	}
	if _, found := getRow(t, s, s.t.items, lone); found {
		t.Error("the expired key with no heir keeps its record")
	}
	if _, found := getRow(t, s, s.t.coverage, lone); found || hasLock(t, s, lone) {
		t.Error("the expired key keeps its coverage or lock")
	}
	if !maps.Equal(downloadBytes(t, s), history) {
		t.Error("expiry changed a download row")
	}
}

func TestItemOwnerlessOrAbsentFalse(t *testing.T) {
	s, _ := newLibrary(t, "sonarr", "radarr")
	r := series("1", 100)
	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv")}})
	k := epKey(t, 100, 1, 1)
	if it, _, found, err := s.Item(t.Context(), k); err != nil || !found || it.Native != "1" || it.VideoPath != "/tv/S/e1.mkv" {
		t.Fatalf("Item of an owned key: %+v, %v, %v", it, found, err)
	}
	if _, _, found, err := s.Item(t.Context(), epKey(t, 100, 1, 9)); found || err != nil {
		t.Errorf("Item of an absent key: %v, %v", found, err)
	}
	activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: policyOf(t, "en")})
	if _, _, found, err := s.Item(t.Context(), k); found || err != nil {
		t.Errorf("Item of an ownerless key: %v, %v", found, err)
	}
}

func TestMovieKeyFallsBackToRootIDs(t *testing.T) {
	s, _ := newLibrary(t, "radarr")
	root := mediasource.Root{Native: mediasource.NativeRef{ID: "7", Kind: media.Movie}, Title: "Film", External: media.ExternalIDs{TMDB: 603}}
	own := mediasource.Item{Native: "m1", VideoPath: "/movies/a.mkv", External: media.ExternalIDs{TMDB: 604}}
	inherited := mediasource.Item{Native: "m2", VideoPath: "/movies/b.mkv", External: media.ExternalIDs{IMDb: 133093}}
	passOver(t, s, "radarr", report{root, []mediasource.Item{own, inherited}})
	for _, tc := range []struct {
		key  string
		item mediasource.NativeID
	}{{"tmdb-604", "m1"}, {"tmdb-603", "m2"}} {
		rec, found := getRow(t, s, s.t.items, mustKey(t, tc.key))
		if !found || rec.Item != tc.item {
			t.Errorf("%s: record %+v found %v, want item %s", tc.key, rec, found, tc.item)
		}
	}
}

// TestNoVideoReportKeepsOwnerlessInstance pins rule 2's exception for a
// record naming an instance: a returning source's no-video report of a key
// its removal left on an ownerless instance, under the same native ids,
// leaves the record on that instance, which its video report then claims.
func TestNoVideoReportKeepsOwnerlessInstance(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	r := series("1", 100)
	const path = "/tv/S/e1.mkv"
	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, path)}})
	k := epKey(t, 100, 1, 1)
	rec, _, _ := holderOf(t, s, k)
	held := InstanceID(rec.Inst)
	activate(t, s, Activation{Languages: policyOf(t, "en")})
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: policyOf(t, "en")})

	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "")}})
	got, inst, found := holderOf(t, s, k)
	if !found || InstanceID(got.Inst) != held || !slices.ContainsFunc(inst.Items, func(x rowKey) bool { return x.k == k }) {
		t.Fatalf("after the no-video report the record names instance %d (found %v), holding %d items; want ownerless instance %d holding it", got.Inst, found, len(inst.Items), held)
	}
	passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, path)}})
	if got, inst, _ := holderOf(t, s, k); InstanceID(got.Inst) != held || inst.Owner != "sonarr" {
		t.Errorf("the video report left the record on instance %d owned by %q, want %d claimed by sonarr", got.Inst, inst.Owner, held)
	}
}
