package statedb

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"go.yaml.in/yaml/v4"
)

// pol parses a languages section value.
func pol(t testing.TB, doc string) language.Policy {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}
	p, errs := language.ParsePolicy(n.Content[0])
	if len(errs) > 0 {
		t.Fatalf("ParsePolicy: %v", errs)
	}
	return p
}

const (
	pgs        = "hdmv_pgs_subtitle"
	epPath     = "/tv/Show/S01E01.mkv"
	showTVDB   = 121361
	audioRules = "default:\n  - tag: fr\nrules:\n  - audio: ja\n    targets:\n      - tag: en\n"
)

var show = series("73", showTVDB)

// covLib is a store at path with source sonarr active under p, ignoring
// codecs.
func covLib(t *testing.T, path string, p language.Policy, codecs ...string) *Store {
	t.Helper()
	cfg := productionConfig()
	cfg.now = (&testClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}).Now
	s := mustOpen(t, path, cfg)
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: p, IgnoredCodecs: codecs})
	return s
}

func tempPath(t *testing.T) string { return filepath.Join(t.TempDir(), "subflux.bolt") }

// showEpisode reports S01E01 at path with audio, answering its key and
// instance.
func showEpisode(t *testing.T, s *Store, src media.SourceID, path string, audio ...string) (media.Key, InstanceID) {
	t.Helper()
	it := episode("e1", 1, 1, path)
	for _, a := range audio {
		it.AudioLangs = append(it.AudioLangs, mustTag(t, a))
	}
	root := show
	if src != "sonarr" {
		root.Native.ID = "radarr-73"
	}
	observe(t, s, src, wholePass(t, s, src), root, it)
	k := epKey(t, showTVDB, 1, 1)
	rec, _, _ := holderOf(t, s, k)
	return k, InstanceID(rec.Inst)
}

func coverageOf(t *testing.T, s *Store, k media.Key, tag string, v language.Variant) (Coverage, bool) {
	t.Helper()
	rows, err := s.Coverage(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Target == mustTag(t, tag) && r.Variant == v {
			return r.Coverage, true
		}
	}
	return 0, false
}

func wantCoverage(t *testing.T, s *Store, k media.Key, tag string, want Coverage) {
	t.Helper()
	got, ok := coverageOf(t, s, k, tag, language.Standard)
	if !ok || got != want {
		t.Errorf("coverage of %s standard = %v (configured %v), want %v", tag, got, ok, want)
	}
}

func track(t *testing.T, tag, codec string) EmbeddedTrack {
	t.Helper()
	return EmbeddedTrack{Tag: mustTag(t, tag), Codec: codec, Index: 2}
}

func putEmbedded(t *testing.T, s *Store, id InstanceID, tracks ...EmbeddedTrack) {
	t.Helper()
	if err := s.PutEmbedded(t.Context(), id, VideoProbe{Stamp: VideoStamp{Size: 1, MtimeNs: 1}, Tracks: tracks}); err != nil {
		t.Fatalf("PutEmbedded: %v", err)
	}
}

func putExternal(t *testing.T, s *Store, id InstanceID, files ...LocalFile) {
	t.Helper()
	if err := s.PutExternal(t.Context(), id, files); err != nil {
		t.Fatalf("PutExternal: %v", err)
	}
}

func local(t *testing.T, name, tag string) LocalFile {
	t.Helper()
	return LocalFile{Name: name, Tag: mustTag(t, tag)}
}

func setLock(t *testing.T, s *Store, id InstanceID, tag string, locked bool) {
	t.Helper()
	if err := s.SetLock(t.Context(), id, mustTag(t, tag), language.Standard, locked); err != nil {
		t.Fatalf("SetLock(%s, %v): %v", tag, locked, err)
	}
}

func locked(t *testing.T, s *Store, k media.Key, tag string) bool {
	t.Helper()
	_, found := getRow(t, s, s.t.locks, lockKey{Key: k, Tag: mustTag(t, tag), Variant: language.Standard})
	return found
}

func reopenLib(t *testing.T, s *Store, path string, p language.Policy, codecs ...string) *Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return covLib(t, path, p, codecs...)
}

func TestPutExternalUnchangedSetJudgesLocks(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	k, id := showEpisode(t, s, "sonarr", epPath)
	putExternal(t, s, id)
	putEmbedded(t, s, id, track(t, "en", "subrip"))
	setLock(t, s, id, "en", true)
	putEmbedded(t, s, id)
	if !locked(t, s, k, "en") {
		t.Fatal("PutEmbedded dropping the serving track deleted the lock")
	}
	putExternal(t, s, id)
	if locked(t, s, k, "en") {
		t.Error("PutExternal with the same empty set kept a lock nothing serves")
	}
	head := headRev(t, s)
	putExternal(t, s, id)
	if got := headRev(t, s); got != head {
		t.Errorf("a second identical PutExternal moved the head from %d to %d", head, got)
	}
}

func TestOwnerlessInstanceKeepsAudioRuleLock(t *testing.T) {
	path := tempPath(t)
	p := pol(t, audioRules)
	s := covLib(t, path, p)
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: p})
	k, id := showEpisode(t, s, "sonarr", epPath, "ja")
	putExternal(t, s, id, local(t, "S01E01.en.srt", "en"))
	setLock(t, s, id, "en", true)
	activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: p})
	head := headRev(t, s)

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := productionConfig()
	s = mustOpen(t, path, cfg)
	activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: p})
	if got := headRev(t, s); got != head {
		t.Errorf("the first Activate after Open moved the head from %d to %d", head, got)
	}
	if !locked(t, s, k, "en") {
		t.Fatal("the first Activate after Open deleted the ownerless instance's audio-rule lock")
	}
	k2, id2 := showEpisode(t, s, "radarr", epPath, "ja")
	if k2 != k || id2 != id {
		t.Fatalf("radarr's report keyed %s at instance %d, want %s at %d", k2, id2, k, id)
	}
	if !locked(t, s, k, "en") {
		t.Error("the claiming source's recompute deleted the lock its audio rule configures")
	}
}

func TestLocksAC10(t *testing.T) {
	en := policyOf(t, "en")
	t.Run("supersession survives PutEmbedded and a policy Activate, clears at the first PutExternal", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		k, id := showEpisode(t, s, "sonarr", epPath)
		putExternal(t, s, id, local(t, "S01E01.en.srt", "en"))
		putEmbedded(t, s, id)
		setLock(t, s, id, "en", true)
		_, moved := showEpisode(t, s, "sonarr", "/tv/Show/S01E01.v2.mkv")
		if moved == id {
			t.Fatal("the path change kept the old instance")
		}
		putEmbedded(t, s, moved)
		activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: pol(t, "default:\n  - tag: en\n  - tag: de\n")})
		if !locked(t, s, k, "en") {
			t.Fatal("PutEmbedded or a policy Activate deleted the superseded lock")
		}
		putExternal(t, s, moved)
		if locked(t, s, k, "en") {
			t.Error("the first PutExternal leaving nothing serving kept the lock")
		}
	})
	for _, tc := range []struct {
		name          string
		local         bool
		embeddedFirst bool
	}{
		{"local-only serving, embedded leg first", true, true},
		{"local-only serving, local leg first", true, false},
		{"embedded-only serving, embedded leg first", false, true},
		{"embedded-only serving, local leg first", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := covLib(t, tempPath(t), en)
			k, id := showEpisode(t, s, "sonarr", epPath)
			var files []LocalFile
			var tracks []EmbeddedTrack
			if tc.local {
				files = []LocalFile{local(t, "S01E01.en.srt", "en")}
			} else {
				tracks = []EmbeddedTrack{track(t, "en", "subrip")}
			}
			legs := []func(){func() { putEmbedded(t, s, id, tracks...) }, func() { putExternal(t, s, id, files...) }}
			if !tc.embeddedFirst {
				legs[0], legs[1] = legs[1], legs[0]
			}
			legs[0]()
			legs[1]()
			setLock(t, s, id, "en", true)
			legs[0]()
			legs[1]()
			if !locked(t, s, k, "en") {
				t.Error("an inventory still serving the pair deleted its lock")
			}
		})
	}
	t.Run("an in-place replacement keeps the lock", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		k, id := showEpisode(t, s, "sonarr", epPath)
		putEmbedded(t, s, id)
		putExternal(t, s, id, local(t, "S01E01.en.srt", "en"))
		setLock(t, s, id, "en", true)
		putExternal(t, s, id, local(t, "S01E01.en.2.srt", "en"))
		if !locked(t, s, k, "en") {
			t.Error("replacing the serving file deleted the lock")
		}
	})
	t.Run("a no-video report keeps the lock and the record", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		k, id := showEpisode(t, s, "sonarr", epPath)
		putExternal(t, s, id, local(t, "S01E01.en.srt", "en"))
		setLock(t, s, id, "en", true)
		showEpisode(t, s, "sonarr", "")
		rec, _, found := holderOf(t, s, k)
		if !found || rec.Inst != 0 {
			t.Fatalf("after the no-video report the record is %+v (found %v), want one with no instance", rec, found)
		}
		if !locked(t, s, k, "en") {
			t.Error("the no-video report deleted the lock")
		}
		wantCoverage(t, s, k, "en", Missing)
	})
	t.Run("a ManualOnly pick keeps its lock with Missing", func(t *testing.T) {
		s := covLib(t, tempPath(t), policyOf(t, "zh-Hans"))
		k, id := showEpisode(t, s, "sonarr", epPath)
		putEmbedded(t, s, id)
		putExternal(t, s, id, local(t, "S01E01.zh-Hant.srt", "zh-Hant"))
		setLock(t, s, id, "zh-Hans", true)
		putExternal(t, s, id, local(t, "S01E01.zh-Hant.srt", "zh-Hant"))
		rows, err := s.Coverage(t.Context(), k)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Coverage != Missing || !rows[0].Locked {
			t.Errorf("Coverage = %+v, want one Missing, locked row", rows)
		}
	})
	t.Run("a double episode locks and unlocks both items", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		p := wholePass(t, s, "sonarr")
		observe(t, s, "sonarr", p, show, episode("e1", 1, 1, epPath), episode("e2", 1, 2, epPath))
		k1, k2 := epKey(t, showTVDB, 1, 1), epKey(t, showTVDB, 1, 2)
		rec, _, _ := holderOf(t, s, k1)
		id := InstanceID(rec.Inst)
		putExternal(t, s, id, local(t, "S01E01-E02.en.srt", "en"))
		setLock(t, s, id, "en", true)
		if !locked(t, s, k1, "en") || !locked(t, s, k2, "en") {
			t.Error("SetLock(true) did not lock both items")
		}
		head := headRev(t, s)
		setLock(t, s, id, "en", true)
		if got := headRev(t, s); got != head {
			t.Errorf("locking a locked pair moved the head from %d to %d", head, got)
		}
		setLock(t, s, id, "en", false)
		if locked(t, s, k1, "en") || locked(t, s, k2, "en") {
			t.Error("SetLock(false) left an item locked")
		}
		head = headRev(t, s)
		setLock(t, s, id, "en", false)
		if got := headRev(t, s); got != head {
			t.Errorf("clearing an unlocked pair moved the head from %d to %d", head, got)
		}
	})
	t.Run("SetLock refusals", func(t *testing.T) {
		s := covLib(t, tempPath(t), en)
		_, id := showEpisode(t, s, "sonarr", epPath)
		putExternal(t, s, id, local(t, "S01E01.de.srt", "de"))
		for _, tc := range []struct {
			name string
			id   InstanceID
			tag  string
		}{{"an unknown instance", id + 100, "en"}, {"an unconfigured pair a file serves", id, "de"}, {"an unservable pair", id, "en"}} {
			head := headRev(t, s)
			if err := s.SetLock(t.Context(), tc.id, mustTag(t, tc.tag), language.Standard, true); !errors.Is(err, ErrNotLockable) {
				t.Errorf("%s: SetLock = %v, want ErrNotLockable", tc.name, err)
			}
			if got := headRev(t, s); got != head {
				t.Errorf("%s: the refusal moved the head from %d to %d", tc.name, head, got)
			}
		}
	})
}

func TestFailedProbeCompletesInventory(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	k, id := showEpisode(t, s, "sonarr", epPath)
	putExternal(t, s, id, local(t, "S01E01.en.srt", "en"))
	setLock(t, s, id, "en", true)
	putExternal(t, s, id)
	if !locked(t, s, k, "en") {
		t.Fatal("PutExternal with no probe stored deleted the lock")
	}
	if err := s.PutEmbedded(t.Context(), id, VideoProbe{}); err != nil {
		t.Fatal(err)
	}
	putExternal(t, s, id)
	if locked(t, s, k, "en") {
		t.Error("a failed probe left the inventory incomplete, keeping a lock nothing serves")
	}
}

func TestProbeFPSRoundTrip(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	_, id := showEpisode(t, s, "sonarr", epPath)
	want := VideoProbe{
		Stamp: VideoStamp{Size: 1 << 30, MtimeNs: 1_700_000_000_000_000_000}, FPS: 23.976,
		Tracks: []EmbeddedTrack{{Codec: "subrip", Title: "English SDH", Tag: mustTag(t, "en"), Index: 3, Flags: language.Flags{HI: true}}},
	}
	if err := s.PutEmbedded(t.Context(), id, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.Embedded(t.Context(), id)
	if err != nil || !found {
		t.Fatalf("Embedded = %v, %v", found, err)
	}
	if got.Stamp != want.Stamp || got.FPS != want.FPS || len(got.Tracks) != 1 || got.Tracks[0] != want.Tracks[0] {
		t.Errorf("Embedded = %+v, want %+v", got, want)
	}
	if _, found, err := s.Embedded(t.Context(), id+1); err != nil || found {
		t.Errorf("Embedded of an instance with no probe = %v, %v", found, err)
	}
}

func TestInventoryRefusals(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	_, id := showEpisode(t, s, "sonarr", epPath)
	for name, call := range map[string]func() error{
		"PutEmbedded on an unknown instance": func() error { return s.PutEmbedded(t.Context(), id+1, VideoProbe{}) },
		"PutExternal on an unknown instance": func() error { return s.PutExternal(t.Context(), id+1, nil) },
		"PutExternal with a nameless file":   func() error { return s.PutExternal(t.Context(), id, []LocalFile{{Tag: mustTag(t, "en")}}) },
		"PutExternal naming a file twice": func() error {
			return s.PutExternal(t.Context(), id, []LocalFile{local(t, "a.en.srt", "en"), local(t, "a.en.srt", "fr")})
		},
	} {
		head := headRev(t, s)
		if err := call(); err == nil {
			t.Errorf("%s succeeded", name)
		}
		if got := headRev(t, s); got != head || countRows(t, s, s.t.local, nil) != 0 {
			t.Errorf("%s wrote: head %d to %d", name, head, got)
		}
	}
}

func TestPutExternalOrderWritesNothing(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	_, id := showEpisode(t, s, "sonarr", epPath)
	putExternal(t, s, id, local(t, "b.fr.srt", "fr"), local(t, "a.en.srt", "en"))
	head := headRev(t, s)
	putExternal(t, s, id, local(t, "a.en.srt", "en"), local(t, "b.fr.srt", "fr"))
	if got := headRev(t, s); got != head {
		t.Errorf("the same set in another order moved the head from %d to %d", head, got)
	}
}
