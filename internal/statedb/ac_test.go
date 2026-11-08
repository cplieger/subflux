package statedb

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// TestAC7 holds the architecture's AC7 storage cases (storage requirement
// 8), each a subtest here or the per-task test that pins it.
func TestAC7(t *testing.T) {
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
	t.Run("after expiry a no-video sighting's key answers its facts", TestExpiryRewritesRecordToNoVideoSighting)
	t.Run("owner removed, restart, new source keeps the audio-rule lock", TestOwnerlessInstanceKeepsAudioRuleLock)
	t.Run("shared root stays listed after its lowest owner goes", TestSharedRootSurvivesLowestOwnerRemoval)
	t.Run("an intent pins its instance against rename, claim, retirement and expiry", TestIntentPinsInstance)
	t.Run("an intent pins its instance's item set", TestIntentPinsItemSet)
	t.Run("an intent pins its keys against release", TestIntentPinsKeyRelease)
	t.Run("work survives a crash before acknowledgement", TestWorkSurvivesCrashBeforeAck)
	t.Run("a re-enqueue during a run survives its acknowledgement", TestReenqueueDuringRunSurvivesAck)
}

// TestAC8 holds the architecture's AC8 storage cases (storage requirement
// 9), each a subtest here or the per-task test that pins it.
func TestAC8(t *testing.T) {
	en := policyOf(t, "en")
	t.Run("embedded English SRT covers en", func(t *testing.T) {
		s := covLib(t, tempPath(t), en, pgs)
		k, id := showEpisode(t, s, "sonarr", epPath)
		wantCoverage(t, s, k, "en", Missing)
		putEmbedded(t, s, id, track(t, "en", "subrip"))
		wantCoverage(t, s, k, "en", Exact)
	})
	t.Run("an ignored PGS track covers nothing", func(t *testing.T) {
		s := covLib(t, tempPath(t), en, pgs)
		k, id := showEpisode(t, s, "sonarr", epPath)
		putEmbedded(t, s, id, track(t, "en", pgs))
		wantCoverage(t, s, k, "en", Missing)
	})
	t.Run("a stored pt-BR track is Exact, pt-AO a Substitute", func(t *testing.T) {
		s := covLib(t, tempPath(t), policyOf(t, "pt-BR"))
		k, id := showEpisode(t, s, "sonarr", epPath)
		putEmbedded(t, s, id, track(t, "pt-AO", "subrip"))
		wantCoverage(t, s, k, "pt-BR", Substitute)
		putEmbedded(t, s, id, track(t, "pt-BR", "subrip"))
		wantCoverage(t, s, k, "pt-BR", Exact)
	})
	t.Run("a user's movie.en.srt is Exact", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		k, id := showEpisode(t, s, "sonarr", epPath)
		putExternal(t, s, id, local(t, "movie.en.srt", "en"))
		wantCoverage(t, s, k, "en", Exact)
	})
	t.Run("a managed file record is evidence", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		k, id := showEpisode(t, s, "sonarr", epPath)
		update(t, s, func(tx *bolttable.Tx) error {
			return s.t.files.Put(tx, fileKey{Inst: id, Name: "S01E01.en.hi.srt"}, &fileRow{Tag: rowTag{t: mustTag(t, "en")}, Timing: []byte{0}, Flags: flagHI})
		})
		putExternal(t, s, id)
		wantCoverage(t, s, k, "en", Missing)
		hi, _ := coverageOf(t, s, k, "en", language.HI)
		if hi != 0 {
			t.Errorf("an unconfigured HI pair has coverage %v", hi)
		}
	})
	t.Run("a policy or codec change recomputes inside Activate", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		k, id := showEpisode(t, s, "sonarr", epPath)
		putEmbedded(t, s, id, track(t, "en", pgs))
		putExternal(t, s, id)
		wantCoverage(t, s, k, "en", Exact)
		setLock(t, s, id, "en", true)
		head := headRev(t, s)
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: en, IgnoredCodecs: []string{pgs}})
		if got := headRev(t, s); got != head+1 {
			t.Errorf("the codec change took the head from %d to %d, want one revision", head, got)
		}
		wantCoverage(t, s, k, "en", Missing)
		if !locked(t, s, k, "en") {
			t.Error("the codec change deleted a configured pair's lock")
		}
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: policyOf(t, "fr"), IgnoredCodecs: []string{pgs}})
		wantCoverage(t, s, k, "fr", Missing)
		if _, ok := coverageOf(t, s, k, "en", language.Standard); ok {
			t.Error("the policy change kept the unconfigured en pair")
		}
		if locked(t, s, k, "en") {
			t.Error("the policy change kept the unconfigured en pair's lock")
		}
	})
	t.Run("an unchanged Activate writes nothing", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		_, id := showEpisode(t, s, "sonarr", epPath)
		putEmbedded(t, s, id, track(t, "en", "subrip"))
		head := headRev(t, s)
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: en})
		if got := headRev(t, s); got != head {
			t.Errorf("an unchanged Activate moved the head from %d to %d", head, got)
		}
	})
	t.Run("a stale pt-AO row is corrected at the first Activate", func(t *testing.T) {
		path := tempPath(t)
		ptBR := policyOf(t, "pt-BR")
		s := covLib(t, path, ptBR)
		k, id := showEpisode(t, s, "sonarr", epPath)
		putEmbedded(t, s, id, track(t, "pt-AO", "subrip"))
		update(t, s, func(tx *bolttable.Tx) error {
			return s.t.coverage.Put(tx, k, &coverageRow{Pairs: []coveragePair{{Tag: rowTag{t: mustTag(t, "pt-BR")}, Variant: uint8(language.Standard), Coverage: uint8(Exact)}}})
		})
		s = reopenLib(t, s, path, ptBR)
		wantCoverage(t, s, k, "pt-BR", Substitute)
	})
	t.Run("an unchanged restart leaves Rev", func(t *testing.T) {
		path := tempPath(t)
		s := covLib(t, path, en)
		_, id := showEpisode(t, s, "sonarr", epPath)
		putEmbedded(t, s, id, track(t, "en", "subrip"))
		head := headRev(t, s)
		s = reopenLib(t, s, path, en)
		if got := headRev(t, s); got != head {
			t.Errorf("an unchanged restart moved the head from %d to %d", head, got)
		}
	})
	t.Run("an added audio language recomputes", func(t *testing.T) {
		s := covLib(t, tempPath(t), pol(t, audioRules))
		k, _ := showEpisode(t, s, "sonarr", epPath)
		wantCoverage(t, s, k, "fr", Missing)
		showEpisode(t, s, "sonarr", epPath, "ja")
		wantCoverage(t, s, k, "en", Missing)
		if _, ok := coverageOf(t, s, k, "fr", language.Standard); ok {
			t.Error("the ja audio rule kept the default fr pair")
		}
	})
	t.Run("a lock toggle, a timing save and an inventory change publish their root", TestPublishesRoot)
	t.Run("a file-record change lists its root in Written", TestEndDeliverOneTransaction)
	t.Run("an unmanaged delete lists its root in Written", TestEndDeleteRemovesUnmanagedName)
	t.Run("a supersession cascading file records lists its root in Written", TestSupersessionListsRootInWritten)
	t.Run("a probe's FPS reads back with its tracks", TestProbeFPSRoundTrip)
	t.Run("an older accepted spelling reads back as its representative", TestRowTagDecodesThroughParse)
	t.Run("a lock key spelled mo is ErrCorrupt", TestTagKeyMustBeCanonical)
	t.Run("a FormatVersion change folds into the tagged codecs", TestCodecVersionFoldsFormatVersion)
	t.Run("a core bump keeps the other namespace", TestCoreBumpKeepsOtherNamespace)
}
