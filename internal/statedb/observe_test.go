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

func TestObserveAC8(t *testing.T) {
	t.Run("double-episode file stays one instance over two passes", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		r := series("1", 100)
		second := episode("2", 1, 2, "/tv/S/S01E01-E02.mkv")
		second.SceneName = "Second.Scene.Name"
		file := report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/S01E01-E02.mkv"), second}}
		passOver(t, s, "sonarr", file)
		k1, k2 := epKey(t, 100, 1, 1), epKey(t, 100, 1, 2)
		rec1, inst, _ := holderOf(t, s, k1)
		rec2, _, _ := holderOf(t, s, k2)
		if rec1.Inst == 0 || rec1.Inst != rec2.Inst {
			t.Fatalf("instances %d and %d, want one", rec1.Inst, rec2.Inst)
		}
		if !slices.Equal(keysOf(&inst), []media.Key{k1, k2}) || inst.SceneName != "S01E01-E02.mkv" {
			t.Errorf("instance %+v, want both keys and the first item's scene name", inst)
		}
		head := headRev(t, s)
		passOver(t, s, "sonarr", file)
		if got, _, _ := holderOf(t, s, k1); got.Inst != rec1.Inst {
			t.Errorf("second pass moved the file to instance %d", got.Inst)
		}
		if got := headRev(t, s); got != head {
			t.Errorf("an unchanged pass moved the head from %d to %d", head, got)
		}
	})

	t.Run("renumbered episode at an unchanged path", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		r := series("1", 100)
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv")}})
		k1, k5 := epKey(t, 100, 1, 1), epKey(t, 100, 1, 5)
		before, _, _ := holderOf(t, s, k1)
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 5, "/tv/S/e1.mkv")}})
		if _, _, found := holderOf(t, s, k1); found {
			t.Error("the old key keeps an item record")
		}
		rec, inst, found := holderOf(t, s, k5)
		if !found || !slices.Equal(keysOf(&inst), []media.Key{k5}) {
			t.Fatalf("new key: record %v, instance items %v, want exactly the new key", found, keysOf(&inst))
		}
		if rec.Inst != before.Inst {
			t.Errorf("the video at the unchanged path moved from instance %d to %d", before.Inst, rec.Inst)
		}
		if n := countRows(t, s, s.t.instances, nil); n != 1 {
			t.Errorf("%d instances, want 1", n)
		}
	})

	t.Run("path moving into another source's instance", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr", "sonarr-4k")
		r := series("1", 100)
		passOver(t, s, "sonarr-4k", report{r, []mediasource.Item{episode("9", 1, 9, "/tv/S/e9.mkv")}})
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv")}})
		owners := passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e9.mkv")}})
		if !slices.Equal(owners, []media.SourceID{"sonarr-4k"}) {
			t.Errorf("FinishPass named %v, want [sonarr-4k]", owners)
		}
		if _, _, found := holderOf(t, s, epKey(t, 100, 1, 1)); found {
			t.Error("the released key keeps its item record")
		}
		if n := countRows(t, s, s.t.instances, nil); n != 1 {
			t.Errorf("%d instances, want only sonarr-4k's", n)
		}
		ov, found := getRow(t, s, s.t.overlaps, itemRef{Source: "sonarr", Root: r.Native, Item: "1"})
		if !found || ov.Owner != "sonarr-4k" || ov.ByKey {
			t.Errorf("overlap %+v found %v, want a path overlap naming sonarr-4k", ov, found)
		}
	})

	t.Run("two paths for one key in one pass", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		r := series("1", 100)
		k := epKey(t, 100, 1, 1)
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/a.mkv")}})
		before, _, _ := holderOf(t, s, k)
		twice := report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/a.mkv"), episode("2", 1, 1, "/tv/S/b.mkv")}}
		passOver(t, s, "sonarr", twice)
		rec, inst, _ := holderOf(t, s, k)
		if rec.Inst != before.Inst || inst.VideoPath != "/tv/S/a.mkv" || rec.Item != "1" {
			t.Errorf("record %+v at %s, want the key left on instance %d at /tv/S/a.mkv", rec, inst.VideoPath, before.Inst)
		}
		if sg, _ := sightingOf(t, s, "sonarr", r, "2"); sg.Key != nil {
			t.Error("the second path's sighting holds the key, want it unidentified")
		}
		head := headRev(t, s)
		passOver(t, s, "sonarr", twice)
		if got := headRev(t, s); got != head {
			t.Errorf("the next unchanged pass moved the head from %d to %d", head, got)
		}
	})

	t.Run("second source's report of an owned path or key", func(t *testing.T) {
		for _, tc := range []struct {
			name, path string
			byKey      bool
		}{{"path", "/tv/S/e1.mkv", false}, {"key", "/tv/S4K/e1.mkv", true}} {
			t.Run(tc.name, func(t *testing.T) {
				s, _ := newLibrary(t, "sonarr", "sonarr-4k")
				r := series("1", 100)
				k := epKey(t, 100, 1, 1)
				passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv")}})
				rec0, inst0, _ := holderOf(t, s, k)
				update(t, s, putLock(s, k, time.Unix(1, 0)))
				update(t, s, putDownload(s, 1, InstanceID(rec0.Inst), k))
				history := downloadBytes(t, s)
				owners := passOver(t, s, "sonarr-4k", report{r, []mediasource.Item{episode("1", 1, 1, tc.path)}})
				if !slices.Equal(owners, []media.SourceID{"sonarr"}) {
					t.Errorf("FinishPass named %v, want [sonarr]", owners)
				}
				rec, inst, _ := holderOf(t, s, k)
				if rec != rec0 || !slices.Equal(keysOf(&inst), keysOf(&inst0)) || inst.Owner != "sonarr" {
					t.Errorf("record %+v, instance %+v changed", rec, inst)
				}
				if !hasLock(t, s, k) || !maps.Equal(downloadBytes(t, s), history) {
					t.Error("the overlap changed a lock or history")
				}
				ov, _ := getRow(t, s, s.t.overlaps, itemRef{Source: "sonarr-4k", Root: r.Native, Item: "1"})
				if ov.ByKey != tc.byKey {
					t.Errorf("overlap by key %v, want %v", ov.ByKey, tc.byKey)
				}

				if owners := passOver(t, s, "sonarr-4k", report{r, nil}); len(owners) != 0 {
					t.Errorf("after the overlap ended FinishPass named %v", owners)
				}
				passOver(t, s, "sonarr-4k", report{r, []mediasource.Item{episode("1", 1, 1, tc.path)}})
				activate(t, s, Activation{Sources: []media.SourceID{"sonarr-4k"}, Languages: policyOf(t, "en")})
				p := wholePass(t, s, "sonarr-4k")
				if owners, err := s.FinishPass(t.Context(), "sonarr-4k", p, false, mediasource.Cursor{}, nil); err != nil || len(owners) != 0 {
					t.Errorf("after the owner's removal FinishPass named %v, %v", owners, err)
				}
			})
		}
	})

	t.Run("clean batch on one root keeps an overlap on another", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr", "sonarr-4k")
		r1, r2 := series("1", 100), series("2", 200)
		both := []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv"), episode("3", 1, 2, "/tv/S/e2.mkv")}
		passOver(t, s, "sonarr", report{r1, both})
		passOver(t, s, "sonarr-4k", report{r1, both}, report{r2, []mediasource.Item{episode("2", 1, 1, "/tv/T/e1.mkv")}})
		p := batchPass(t, s, "sonarr-4k", r2)
		observe(t, s, "sonarr-4k", p, r2, episode("2", 1, 1, "/tv/T/e1.mkv"))
		if owners := finish(t, s, "sonarr-4k", p); !slices.Equal(owners, []media.SourceID{"sonarr"}) {
			t.Errorf("a clean batch on r2 named %v, want r1's overlap owner [sonarr]", owners)
		}
	})

	t.Run("owner removed and a new source re-keys the instance", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr", "sonarr-4k")
		r := series("1", 100)
		k1, k2, k3 := epKey(t, 100, 1, 1), epKey(t, 100, 1, 2), epKey(t, 100, 1, 3)
		const path = "/tv/S/S01E01-E02.mkv"
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, path), episode("2", 1, 2, path)}})
		rec, _, _ := holderOf(t, s, k1)
		id := InstanceID(rec.Inst)
		update(t, s, putLock(s, k1, time.Unix(1, 0)))
		update(t, s, putLock(s, k2, time.Unix(1, 0)))
		update(t, s, putDownload(s, 1, id, k1))
		history := downloadBytes(t, s)
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr-4k"}, Languages: policyOf(t, "en")})
		passOver(t, s, "sonarr-4k", report{r, []mediasource.Item{episode("7", 1, 1, path), episode("8", 1, 3, path)}})
		inst, _ := getRow(t, s, s.t.instances, id)
		if inst.Owner != "sonarr-4k" || !inst.Orphaned.IsZero() || !slices.Equal(keysOf(&inst), []media.Key{k1, k3}) {
			t.Fatalf("claimed instance %+v, want sonarr-4k's with k1 and k3", inst)
		}
		if got, _, _ := holderOf(t, s, k1); got.Source != "sonarr-4k" || got.Item != "7" || InstanceID(got.Inst) != id {
			t.Errorf("k1's record %+v, want sonarr-4k's item 7 on instance %d", got, id)
		}
		if !hasLock(t, s, k1) || !maps.Equal(downloadBytes(t, s), history) {
			t.Error("the unchanged key lost its lock or history")
		}
		if _, _, found := holderOf(t, s, k2); found || hasLock(t, s, k2) {
			t.Error("the key the new owner does not report keeps its record or lock")
		}
	})

	t.Run("rename moves history with download rows byte-identical", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		r := series("1", 100)
		k := epKey(t, 100, 1, 1)
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/old.mkv")}})
		old, _, _ := holderOf(t, s, k)
		update(t, s, putLock(s, k, time.Unix(1, 0)))
		update(t, s, putDownload(s, 1, InstanceID(old.Inst), k))
		history := downloadBytes(t, s)
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/new.mkv")}})
		rec, inst, _ := holderOf(t, s, k)
		if rec.Inst == old.Inst || inst.VideoPath != "/tv/S/new.mkv" {
			t.Errorf("record %+v at %s, want a new instance at the new path", rec, inst.VideoPath)
		}
		if _, found := getRow(t, s, s.t.instances, InstanceID(old.Inst)); found {
			t.Error("the old path's instance remains")
		}
		if !maps.Equal(downloadBytes(t, s), history) || countRows(t, s, s.t.downloads, nil) != 1 || !hasLock(t, s, k) {
			t.Error("the rename changed a download row or the lock")
		}
		var hist int
		if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
			return s.t.itemHistory.Scan(r, bolttable.Range{Prefix: k.Bytes()}, func([]byte, uint64, []byte) (bool, error) { hist++; return true, nil })
		}); err != nil || hist != 1 {
			t.Errorf("the key's history reads %d rows (%v), want 1", hist, err)
		}
	})

	t.Run("31-day vs 29-day orphan with no pass between", func(t *testing.T) {
		s, clock := newLibrary(t, "sonarr", "sonarr-b", "sonarr-4k")
		r := series("1", 100)
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/old.mkv")}})
		passOver(t, s, "sonarr-b", report{r, []mediasource.Item{episode("2", 1, 2, "/tv/S/young.mkv")}})
		oldRec, _, _ := holderOf(t, s, epKey(t, 100, 1, 1))
		youngRec, _, _ := holderOf(t, s, epKey(t, 100, 1, 2))
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr-b", "sonarr-4k"}, Languages: policyOf(t, "en")})
		clock.advance(2 * 24 * time.Hour)
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr-4k"}, Languages: policyOf(t, "en")})
		clock.advance(29*24*time.Hour + time.Minute)
		p := wholePass(t, s, "sonarr-4k")
		observe(t, s, "sonarr-4k", p, r, episode("7", 1, 1, "/tv/S/old.mkv"), episode("8", 1, 2, "/tv/S/young.mkv"))
		got, _, _ := holderOf(t, s, epKey(t, 100, 1, 1))
		if got.Inst == oldRec.Inst {
			t.Error("the instance orphaned 31 days ago was claimed, want a fresh one")
		}
		if _, found := getRow(t, s, s.t.instances, InstanceID(oldRec.Inst)); found {
			t.Error("the instance orphaned 31 days ago did not expire at the claim")
		}
		if got, _, _ := holderOf(t, s, epKey(t, 100, 1, 2)); got.Inst != youngRec.Inst {
			t.Errorf("the instance orphaned 29 days ago is %d, want it claimed (%d)", got.Inst, youngRec.Inst)
		}
	})

	t.Run("with and without video over two passes", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			items []mediasource.Item
		}{
			{"video first", []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv"), episode("2", 1, 1, "")}},
			{"no video first", []mediasource.Item{episode("2", 1, 1, ""), episode("1", 1, 1, "/tv/S/e1.mkv")}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s, _ := newLibrary(t, "sonarr")
				r := series("1", 100)
				passOver(t, s, "sonarr", report{r, tc.items})
				rec, inst, _ := holderOf(t, s, epKey(t, 100, 1, 1))
				if rec.Item != "1" || inst.VideoPath != "/tv/S/e1.mkv" {
					t.Errorf("record %+v, want the video's sighting attached", rec)
				}
				head := headRev(t, s)
				passOver(t, s, "sonarr", report{r, tc.items})
				if got := headRev(t, s); got != head {
					t.Errorf("the second pass moved the head from %d to %d", head, got)
				}
			})
		}
	})

	t.Run("multi-item root retires only what the refetch drops", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		r := series("1", 100)
		e1, e2, e3 := episode("1", 1, 1, "/tv/S/e1.mkv"), episode("2", 1, 2, "/tv/S/e2.mkv"), episode("3", 1, 3, "/tv/S/e3.mkv")
		passOver(t, s, "sonarr", report{r, []mediasource.Item{e1, e2, e3}})
		k1, _, _ := holderOf(t, s, epKey(t, 100, 1, 1))
		p := batchPass(t, s, "sonarr", r)
		observe(t, s, "sonarr", p, r, e1, e3)
		finish(t, s, "sonarr", p)
		if _, _, found := holderOf(t, s, epKey(t, 100, 1, 2)); found {
			t.Error("the dropped item keeps its record")
		}
		if _, found := sightingOf(t, s, "sonarr", r, "2"); found {
			t.Error("the dropped item keeps its sighting")
		}
		for _, ep := range []int{1, 3} {
			if _, _, found := holderOf(t, s, epKey(t, 100, 1, ep)); !found {
				t.Errorf("episode %d lost its record", ep)
			}
		}
		if got, _, _ := holderOf(t, s, epKey(t, 100, 1, 1)); got != k1 {
			t.Errorf("a kept item's record changed: %+v", got)
		}
		if n := countRows(t, s, s.t.instances, nil); n != 2 {
			t.Errorf("%d instances, want 2", n)
		}
	})

	t.Run("incomplete pass retires nothing", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		r := series("1", 100)
		passOver(t, s, "sonarr", report{r, []mediasource.Item{episode("1", 1, 1, "/tv/S/e1.mkv")}})
		var c mediasource.Cursor
		if err := c.UnmarshalBinary([]byte("at-1")); err != nil {
			t.Fatal(err)
		}
		p := wholePass(t, s, "sonarr")
		if _, err := s.FinishPass(t.Context(), "sonarr", p, false, c, nil); err != nil {
			t.Fatal(err)
		}
		if _, _, found := holderOf(t, s, epKey(t, 100, 1, 1)); !found {
			t.Error("an incomplete pass retired the item")
		}
		if n := countRows(t, s, s.t.roots, nil); n != 1 {
			t.Errorf("%d root records, want 1", n)
		}
		if got, _, err := s.SourceCursor(t.Context(), "sonarr"); err != nil || len(must(got.MarshalBinary())) != 0 {
			t.Errorf("an incomplete pass stored its cursor (%v)", err)
		}
		if _, found := getRow(t, s, s.t.passes, "sonarr"); found {
			t.Error("the incomplete pass stays open")
		}
	})
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
