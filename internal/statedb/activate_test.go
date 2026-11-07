package statedb

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"go.yaml.in/yaml/v4"
)

// policyOf is the languages section wanting one default target, tag.
func policyOf(t *testing.T, tag string) language.Policy {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("default:\n  - tag: "+tag+"\n"), &doc); err != nil {
		t.Fatal(err)
	}
	p, errs := language.ParsePolicy(doc.Content[0])
	if len(errs) > 0 {
		t.Fatalf("ParsePolicy(%s): %v", tag, errs)
	}
	return p
}

func activate(t *testing.T, s *Store, a Activation) {
	t.Helper()
	if err := s.Activate(t.Context(), a); err != nil {
		t.Fatalf("Activate(%v): %v", a.Sources, err)
	}
}

func samePolicy(a, b language.Policy) bool { return a.Digest() == b.Digest() }

func TestPolicyFollowsActivation(t *testing.T) {
	s, _ := faultyStore(t, errInjected)
	if !samePolicy(s.Policy(), language.Policy{}) {
		t.Fatal("Policy before any Activate is not the zero policy")
	}
	en, fr := policyOf(t, "en"), policyOf(t, "fr")
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: en})
	if !samePolicy(s.Policy(), en) {
		t.Fatal("Policy after Activate is not that activation's")
	}
	err := s.Activate(markFailing(t.Context()), Activation{Sources: []media.SourceID{"sonarr"}, Languages: fr})
	if !errors.Is(err, bolttable.ErrCommit) {
		t.Fatalf("faulted Activate: %v, want ErrCommit", err)
	}
	if !samePolicy(s.Policy(), en) {
		t.Error("Policy after a failed Activate is not the previous activation's")
	}
}

func TestActivateUnchangedWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	en := policyOf(t, "en")
	s, err := open(path, new(productionConfig()))
	if err != nil {
		t.Fatal(err)
	}
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: en, IgnoredCodecs: []string{"pgs", "dvb"}})
	head := headRev(t, s)
	activate(t, s, Activation{Sources: []media.SourceID{"radarr", "sonarr"}, Languages: policyOf(t, "en"), IgnoredCodecs: []string{"dvb", "pgs", "dvb"}})
	if got := headRev(t, s); got != head {
		t.Errorf("an equal Activate moved the head from %d to %d", head, got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = mustOpen(t, path, productionConfig())
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: en, IgnoredCodecs: []string{"pgs", "dvb"}})
	if got := headRev(t, s); got != head {
		t.Errorf("a restart's equal first Activate moved the head from %d to %d", head, got)
	}
	if !samePolicy(s.Policy(), en) {
		t.Error("an equal Activate did not install its policy")
	}
}

func TestActivateConfigMarkFull(t *testing.T) {
	s := seededStore(t)
	en := policyOf(t, "en")
	cases := []struct {
		name string
		a    Activation
	}{
		{"first", Activation{Sources: []media.SourceID{"sonarr"}, Languages: en}},
		{"source_added", Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: en}},
		{"policy_changed", Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: policyOf(t, "fr")}},
		{"codec_ignored", Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: en, IgnoredCodecs: []string{"pgs"}}},
		{"source_removed", Activation{Sources: []media.SourceID{"radarr"}, Languages: en, IgnoredCodecs: []string{"pgs"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := headRev(t, s)
			activate(t, s, c.a)
			if d := since(t, s, before); !d.Full || !slices.Equal(d.Marks, []string{MarkConfig}) {
				t.Errorf("Since after the change: %+v, want Full with [%s]", d, MarkConfig)
			}
			after := headRev(t, s)
			update(t, s, putItem(s, movieKey(t, 1)))
			if d := since(t, s, after); d.Full {
				t.Errorf("Since after the change's own revision: %+v, want a partial answer", d)
			}
		})
	}
}

func TestActivateFailureRestoresPrevious(t *testing.T) {
	s, _ := faultyStore(t, errInjected)
	en := policyOf(t, "en")
	both := Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: en}
	activate(t, s, both)
	if err := s.write(t.Context(), "cursor", s.db.Update, putCursor(s, "sonarr")); err != nil {
		t.Fatal(err)
	}
	head := headRev(t, s)

	err := s.Activate(markFailing(t.Context()), Activation{Sources: []media.SourceID{"radarr"}, Languages: policyOf(t, "fr")})
	if !errors.Is(err, bolttable.ErrCommit) {
		t.Fatalf("faulted Activate: %v, want ErrCommit", err)
	}
	if !samePolicy(s.Policy(), en) {
		t.Error("a failed Activate left its policy in force")
	}
	if !hasCursor(t, s, "sonarr") {
		t.Error("a failed Activate deleted the removed source's cursor")
	}
	activate(t, s, both)
	if got := headRev(t, s); got != head {
		t.Errorf("re-activating the previous configuration moved the head from %d to %d; the failed Activate changed the stored row", head, got)
	}
}

func TestActivateRefusals(t *testing.T) {
	s := newStore(t)
	for name, sources := range map[string][]media.SourceID{
		"empty_source":     {"sonarr", ""},
		"invalid_source":   {"sonarr", "Sonarr 4K"},
		"duplicate_source": {"sonarr", "radarr", "sonarr"},
	} {
		t.Run(name, func(t *testing.T) {
			head := headRev(t, s)
			if err := s.Activate(t.Context(), Activation{Sources: sources, Languages: policyOf(t, "en")}); err == nil {
				t.Errorf("Activate(%q) succeeded", sources)
			}
			if headRev(t, s) != head || !samePolicy(s.Policy(), language.Policy{}) {
				t.Error("a refused Activate wrote or installed something")
			}
		})
	}
}

func hasCursor(t *testing.T, s *Store, src media.SourceID) bool {
	t.Helper()
	var found bool
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) (err error) {
		_, found, err = s.t.cursors.Get(r, src)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

func countRows[K, V any](t *testing.T, s *Store, tb *bolttable.Table[K, V], prefix []byte) int {
	t.Helper()
	n := 0
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
		return tb.Scan(r, bolttable.Range{Prefix: prefix}, func(K, *V) (bool, error) { n++; return true, nil })
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func getRow[K, V any](t *testing.T, s *Store, tb *bolttable.Table[K, V], k K) (V, bool) {
	t.Helper()
	var v V
	var found bool
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) (err error) {
		v, found, err = tb.Get(r, k)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return v, found
}

// removalFixture is the library design 5.4's source removal acts on:
// sonarr's root holds an attached item (k1), a no-video item no one else
// reports (k2) and a no-video item (k3) radarr reports twice, with a video
// and without; radarr has overlaps naming sonarr and another owner, and an
// instance of its own.
type removalFixture struct {
	k1, k2, k3     media.Key
	sonarrRoot     rootRef
	radarrRoot     rootRef
	overlapSonarr  itemRef
	overlapOther   itemRef
	sonarrInst     InstanceID
	radarrInst     InstanceID
	lockK1, lockK2 lockKey
}

func writeRemovalFixture(t *testing.T, s *Store) removalFixture {
	t.Helper()
	en := mustTag(t, "en")
	f := removalFixture{
		k1: movieKey(t, 1), k2: movieKey(t, 2), k3: movieKey(t, 3),
		sonarrRoot: rootRef{Source: "sonarr", Root: mediasource.NativeRef{Kind: media.Movie, ID: "100"}},
		radarrRoot: rootRef{Source: "radarr", Root: mediasource.NativeRef{Kind: media.Movie, ID: "200"}},
		sonarrInst: 1, radarrInst: 2,
	}
	f.overlapSonarr = itemRef{rootRef: f.radarrRoot, Item: "31"}
	f.overlapOther = itemRef{rootRef: f.radarrRoot, Item: "32"}
	f.lockK1 = lockKey{Key: f.k1, Tag: en, Variant: language.Standard}
	f.lockK2 = lockKey{Key: f.k2, Tag: en, Variant: language.Standard}
	sonarrRef := nativeRefRow{ID: "100", Kind: uint8(media.Movie)}
	cov := coverageRow{Pairs: []coveragePair{{Tag: rowTag{t: en}, Variant: uint8(language.Standard), Coverage: uint8(Missing)}}}
	update(t, s, func(tx *bolttable.Tx) error {
		return errors.Join(
			s.t.roots.Put(tx, f.sonarrRoot, &rootRow{Title: "sonarr root"}),
			s.t.stamps.Put(tx, itemRef{rootRef: f.sonarrRoot}, &stampRow{Pass: 1}),
			s.t.sightings.Put(tx, itemRef{rootRef: f.sonarrRoot, Item: "1"}, &sightingRow{Key: &rowKey{k: f.k1}, VideoPath: "/movies/a.mkv"}),
			s.t.sightings.Put(tx, itemRef{rootRef: f.sonarrRoot, Item: "2"}, &sightingRow{Key: &rowKey{k: f.k2}}),
			s.t.sightings.Put(tx, itemRef{rootRef: f.sonarrRoot, Item: "3"}, &sightingRow{Key: &rowKey{k: f.k3}}),
			s.t.stamps.Put(tx, itemRef{rootRef: f.sonarrRoot, Item: "1"}, &stampRow{Pass: 1}),
			s.t.overlaps.Put(tx, itemRef{rootRef: f.sonarrRoot, Item: "4"}, &overlapRow{Owner: "radarr"}),
			s.t.cursors.Put(tx, "sonarr", &cursorRow{Cursor: []byte("c")}),
			s.t.passes.Put(tx, "sonarr", &passRow{Pass: 1}),
			s.t.instances.Put(tx, f.sonarrInst, &instanceRow{Owner: "sonarr", VideoPath: "/movies/a.mkv", Items: []rowKey{{k: f.k1}}}),
			s.t.items.Put(tx, f.k1, &itemRow{Source: "sonarr", Item: "1", Root: sonarrRef, Inst: uint64(f.sonarrInst)}),
			s.t.coverage.Put(tx, f.k1, &cov),
			s.t.locks.Put(tx, f.lockK1, &lockRow{At: time.Unix(1, 0).UTC()}),
			s.t.items.Put(tx, f.k2, &itemRow{Source: "sonarr", Item: "2", Root: sonarrRef}),
			s.t.coverage.Put(tx, f.k2, &cov),
			s.t.locks.Put(tx, f.lockK2, &lockRow{At: time.Unix(1, 0).UTC()}),
			s.t.items.Put(tx, f.k3, &itemRow{Source: "sonarr", Item: "3", Root: sonarrRef}),
			s.t.sightings.Put(tx, itemRef{rootRef: f.radarrRoot, Item: "29"}, &sightingRow{Key: &rowKey{k: f.k3}, VideoPath: "/movies/c.mkv"}),
			s.t.sightings.Put(tx, itemRef{rootRef: f.radarrRoot, Item: "30"}, &sightingRow{Key: &rowKey{k: f.k3}}),
			s.t.overlaps.Put(tx, f.overlapSonarr, &overlapRow{Owner: "sonarr"}),
			s.t.overlaps.Put(tx, f.overlapOther, &overlapRow{Owner: "plex"}),
			s.t.instances.Put(tx, f.radarrInst, &instanceRow{Owner: "radarr", VideoPath: "/movies/b.mkv"}),
		)
	})
	return f
}

func TestActivateSourceRemovalMakesOwnerless(t *testing.T) {
	s := newStore(t)
	en := policyOf(t, "en")
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: en})
	f := writeRemovalFixture(t, s)
	start := time.Now()
	activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: en})

	t.Run("source_rows_deleted", func(t *testing.T) {
		sonarr := appendText(nil, "sonarr")
		for name, n := range map[string]int{
			"roots":     countRows(t, s, s.t.roots, sonarr),
			"sightings": countRows(t, s, s.t.sightings, sonarr),
			"stamps":    countRows(t, s, s.t.stamps, sonarr),
			"overlaps":  countRows(t, s, s.t.overlaps, sonarr),
		} {
			if n != 0 {
				t.Errorf("%s: %d sonarr rows left", name, n)
			}
		}
		if _, found := getRow(t, s, s.t.cursors, "sonarr"); found {
			t.Error("sonarr's cursor left")
		}
		if _, found := getRow(t, s, s.t.passes, "sonarr"); found {
			t.Error("sonarr's pass left")
		}
	})
	t.Run("instance_ownerless_keeping_its_rows", func(t *testing.T) {
		inst, found := getRow(t, s, s.t.instances, f.sonarrInst)
		if !found || inst.Owner != "" || inst.VideoPath != "/movies/a.mkv" || len(inst.Items) != 1 || inst.Items[0].k != f.k1 {
			t.Fatalf("instance %d: %+v found %v; want ownerless at its path with its item", f.sonarrInst, inst, found)
		}
		if inst.Orphaned.Before(start.Add(-time.Second)) || inst.Orphaned.After(time.Now()) {
			t.Errorf("orphaned at %v, want the removal's time", inst.Orphaned)
		}
		if item, found := getRow(t, s, s.t.items, f.k1); !found || item.Inst != uint64(f.sonarrInst) {
			t.Errorf("k1's item record: %+v found %v; want it kept on its instance", item, found)
		}
		if _, found := getRow(t, s, s.t.coverage, f.k1); !found {
			t.Error("k1's coverage deleted")
		}
		if _, found := getRow(t, s, s.t.locks, f.lockK1); !found {
			t.Error("k1's lock deleted")
		}
	})
	t.Run("no_video_record_released", func(t *testing.T) {
		if _, found := getRow(t, s, s.t.items, f.k2); found {
			t.Error("k2's item record kept")
		}
		if _, found := getRow(t, s, s.t.coverage, f.k2); found {
			t.Error("k2's coverage kept")
		}
		if _, found := getRow(t, s, s.t.locks, f.lockK2); found {
			t.Error("k2's lock kept")
		}
	})
	t.Run("live_no_video_sighting_takes_the_record", func(t *testing.T) {
		want := itemRow{Source: "radarr", Item: "30", Root: nativeRefRow{ID: "200", Kind: uint8(media.Movie)}}
		if got, found := getRow(t, s, s.t.items, f.k3); !found || got != want {
			t.Errorf("k3's item record: %+v found %v; want %+v", got, found, want)
		}
	})
	t.Run("overlaps_naming_it_deleted", func(t *testing.T) {
		if _, found := getRow(t, s, s.t.overlaps, f.overlapSonarr); found {
			t.Error("radarr's overlap naming sonarr kept")
		}
		if _, found := getRow(t, s, s.t.overlaps, f.overlapOther); !found {
			t.Error("radarr's overlap naming another owner deleted")
		}
	})
	t.Run("other_source_untouched", func(t *testing.T) {
		if inst, found := getRow(t, s, s.t.instances, f.radarrInst); !found || inst.Owner != "radarr" || !inst.Orphaned.IsZero() {
			t.Errorf("radarr's instance: %+v found %v", inst, found)
		}
		if n := countRows(t, s, s.t.sightings, appendText(nil, "radarr")); n != 2 {
			t.Errorf("radarr sightings: %d, want 2", n)
		}
	})
}

// heirLibrary is S01E01 sighted with no video by sonarr, with ja audio so
// audioRules configures en, and by radarr with no audio, which configures
// only the default fr; sonarr's sighting holds the record and en carries a
// lock.
func heirLibrary(t *testing.T) (*Store, language.Policy, media.Key, lockKey) {
	t.Helper()
	p := pol(t, audioRules)
	s := covLib(t, tempPath(t), p)
	activate(t, s, Activation{Sources: []media.SourceID{"radarr", "sonarr"}, Languages: p})
	japanese := episode("e1", 1, 1, "")
	japanese.AudioLangs = []language.Tag{mustTag(t, "ja")}
	observe(t, s, "sonarr", wholePass(t, s, "sonarr"), show, japanese)
	other := show
	other.Native.ID = "radarr-73"
	observe(t, s, "radarr", wholePass(t, s, "radarr"), other, episode("e1", 1, 1, ""))
	k := epKey(t, showTVDB, 1, 1)
	if rec, _, _ := holderOf(t, s, k); rec.Source != "sonarr" {
		t.Fatalf("the record names %s, want sonarr's sighting", rec.Source)
	}
	if _, ok := coverageOf(t, s, k, "en", language.Standard); !ok {
		t.Fatal("en is not configured from sonarr's ja audio")
	}
	en := lockKey{Key: k, Tag: mustTag(t, "en"), Variant: language.Standard}
	update(t, s, func(tx *bolttable.Tx) error { return s.t.locks.Put(tx, en, &lockRow{At: time.Unix(1, 0).UTC()}) })
	return s, p, k, en
}

// wantHeirCoverage checks that k's record passed to radarr's sighting and
// its coverage was judged from that sighting alone.
func wantHeirCoverage(t *testing.T, s *Store, k media.Key, en lockKey) {
	t.Helper()
	if rec, _, _ := holderOf(t, s, k); rec.Source != "radarr" {
		t.Fatalf("the record names %s, want radarr's no-video sighting", rec.Source)
	}
	if _, ok := coverageOf(t, s, k, "en", language.Standard); ok {
		t.Error("en stays configured from the removed sighting's audio rule")
	}
	if _, ok := coverageOf(t, s, k, "fr", language.Standard); !ok {
		t.Error("fr, the heir's default target, is not configured")
	}
	if _, found := getRow(t, s, s.t.locks, en); found {
		t.Error("the lock on en, a pair the heir does not configure, survived")
	}
}

func TestActivateRemovalRecomputesHeir(t *testing.T) {
	s, p, k, en := heirLibrary(t)
	activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: p})
	wantHeirCoverage(t, s, k, en)
}

func TestObserveHandOnRecomputesHeir(t *testing.T) {
	s, _, k, en := heirLibrary(t)
	passOver(t, s, "sonarr", report{root: show})
	wantHeirCoverage(t, s, k, en)
}
