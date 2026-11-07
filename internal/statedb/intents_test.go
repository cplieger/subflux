package statedb

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/timing"
)

const (
	managedName = "S01E01.en.srt"
	renamedPath = "/tv/Show/S01E01.renamed.mkv"
)

func cuesOf(t *testing.T, text string) timing.Cues {
	t.Helper()
	c, err := timing.ParseCues([]byte("1\n00:00:01,000 --> 00:00:02,000\n" + text + "\n"))
	if err != nil {
		t.Fatalf("ParseCues: %v", err)
	}
	return c
}

func recordAt(inst InstanceID, name string, tag language.Tag, sum byte) FileRecord {
	return FileRecord{
		Inst: inst, Name: name, Tag: tag, Timing: timing.Identity{}, Locked: true, Ordinal: 0,
		Identity:   Identity{Size: 10, MtimeNs: 1, SHA256: [32]byte{sum}},
		Provenance: &Provenance{Provider: "opensubtitles", Release: "Show.S01E01", MatchedBy: rank.Hash, Fit: rank.Exact},
	}
}

func attemptOf(inst InstanceID, delivered bool, keys ...media.Key) Download {
	return Download{
		At: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Target: enTag, Variant: language.Standard, Items: keys,
		Inst: inst, Run: RunID{1}, Attempt: 1, Delivered: delivered, Candidate: [32]byte{9},
		Provenance: Provenance{Provider: "opensubtitles", MatchedBy: rank.Hash, Fit: rank.Exact},
	}
}

func pendingDeliver(t *testing.T, inst InstanceID, name string, sum byte, keys ...media.Key) PendingDeliver {
	t.Helper()
	return PendingDeliver{Record: recordAt(inst, name, enTag, sum), Baseline: cuesOf(t, "Hi"), Attempts: []Download{attemptOf(inst, true, keys...)}}
}

func beginDeliver(t *testing.T, s *Store, d PendingDeliver) {
	t.Helper()
	if err := s.BeginDeliver(t.Context(), d); err != nil {
		t.Fatalf("BeginDeliver(%s): %v", d.Record.Name, err)
	}
}

func endDeliver(t *testing.T, s *Store, inst InstanceID, name string, landed bool) {
	t.Helper()
	if err := s.EndDeliver(t.Context(), inst, name, landed); err != nil {
		t.Fatalf("EndDeliver(%s, %v): %v", name, landed, err)
	}
}

// deliver lands one delivery of name.
func deliver(t *testing.T, s *Store, d PendingDeliver) {
	t.Helper()
	beginDeliver(t, s, d)
	endDeliver(t, s, d.Record.Inst, d.Record.Name, true)
}

func pendingCount(t *testing.T, s *Store) int {
	t.Helper()
	ws, err := s.PendingWrites(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return len(ws)
}

// pinOp is one Begin with its End: the three operations an intent pins
// an instance for.
type pinOp struct {
	begin func(t *testing.T, s *Store, inst InstanceID, k media.Key)
	end   func(t *testing.T, s *Store, inst InstanceID) error
	name  string
}

// pinOps begins each operation on an instance holding a landed record of
// managedName.
var pinOps = []pinOp{
	{
		name: "deliver",
		begin: func(t *testing.T, s *Store, inst InstanceID, k media.Key) {
			beginDeliver(t, s, pendingDeliver(t, inst, "S01E01.en.2.srt", 2, k))
		},
		end: func(t *testing.T, s *Store, inst InstanceID) error {
			return s.EndDeliver(t.Context(), inst, "S01E01.en.2.srt", true)
		},
	},
	{
		name: "retime",
		begin: func(t *testing.T, s *Store, inst InstanceID, _ media.Key) {
			rec := recordAt(inst, managedName, enTag, 3)
			rec.Timing = timing.Shift{Ms: 500}
			if err := s.BeginRetime(t.Context(), rec, nil); err != nil {
				t.Fatalf("BeginRetime: %v", err)
			}
		},
		end: func(t *testing.T, s *Store, inst InstanceID) error {
			return s.EndRetime(t.Context(), inst, managedName, true)
		},
	},
	{
		name: "delete",
		begin: func(t *testing.T, s *Store, inst InstanceID, _ media.Key) {
			if err := s.BeginDelete(t.Context(), inst, managedName, [32]byte{1}); err != nil {
				t.Fatalf("BeginDelete: %v", err)
			}
		},
		end: func(t *testing.T, s *Store, inst InstanceID) error {
			return s.EndDelete(t.Context(), inst, managedName, true)
		},
	},
}

// pinnedLib is a library whose sonarr instance at epPath holds S01E01 with
// a landed record, after a complete pass that stored cursor "c1", and op
// begun on it.
func pinnedLib(t *testing.T, op pinOp, sources ...media.SourceID) (*Store, *testClock, media.Key, InstanceID) {
	t.Helper()
	s, clock := newLibrary(t, append([]media.SourceID{"sonarr"}, sources...)...)
	k, inst := showEpisode(t, s, "sonarr", epPath)
	p := wholePass(t, s, "sonarr")
	observe(t, s, "sonarr", p, show, episode("e1", 1, 1, epPath))
	finishWith(t, s, "sonarr", p, true, "c1")
	deliver(t, s, pendingDeliver(t, inst, managedName, 1, k))
	op.begin(t, s, inst, k)
	return s, clock, k, inst
}

func finishWith(t *testing.T, s *Store, src media.SourceID, p PassID, complete bool, cursor string) {
	t.Helper()
	var c mediasource.Cursor
	if err := c.UnmarshalBinary([]byte(cursor)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishPass(t.Context(), src, p, complete, c, nil); err != nil {
		t.Fatalf("FinishPass(%s, %v): %v", src, complete, err)
	}
}

func cursorOf(t *testing.T, s *Store, src media.SourceID) string {
	t.Helper()
	c, _, err := s.SourceCursor(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.MarshalBinary()
	return string(b)
}

// refusedPinned asserts err refuses a pinned instance and Rev did not move.
func refusedPinned(t *testing.T, s *Store, err error, before uint64) {
	t.Helper()
	if !errors.Is(err, errPinned) {
		t.Fatalf("err = %v, want errPinned", err)
	}
	if got := headRev(t, s); got != before {
		t.Errorf("Rev %d after a refused write, want %d", got, before)
	}
}

func endOp(t *testing.T, s *Store, op pinOp, inst InstanceID) {
	t.Helper()
	if err := op.end(t, s, inst); err != nil {
		t.Fatalf("End %s: %v", op.name, err)
	}
}

func TestIntentPinsInstance(t *testing.T) {
	for _, op := range pinOps {
		t.Run(op.name, func(t *testing.T) {
			t.Run("the owner's rename is refused until End", func(t *testing.T) {
				s, _, k, inst := pinnedLib(t, op)
				p := wholePass(t, s, "sonarr")
				before := headRev(t, s)
				err := s.ObserveItems(t.Context(), "sonarr", p, show, []mediasource.Item{episode("e1", 1, 1, renamedPath)})
				refusedPinned(t, s, err, before)
				finishWith(t, s, "sonarr", p, false, "c2")
				if got := cursorOf(t, s, "sonarr"); got != "c1" {
					t.Errorf("cursor %q after an incomplete pass, want c1", got)
				}
				endOp(t, s, op, inst)
				if _, held, _ := holderOf(t, s, k); held.VideoPath != epPath {
					t.Errorf("End applied to path %q, want %q", held.VideoPath, epPath)
				}
				history := downloadBytes(t, s)
				passOver(t, s, "sonarr", report{show, []mediasource.Item{episode("e1", 1, 1, renamedPath)}})
				if _, held, _ := holderOf(t, s, k); held.VideoPath != renamedPath {
					t.Errorf("after the rerun the key is at %q, want %q", held.VideoPath, renamedPath)
				}
				if n := countRows(t, s, s.t.files, appendUint64(nil, uint64(inst))); n != 0 {
					t.Errorf("%d file records left on the renamed-away instance", n)
				}
				if !locked(t, s, k, "en") {
					t.Error("the rename lost the key's lock")
				}
				if got := downloadBytes(t, s); len(got) != len(history) {
					t.Errorf("history %d rows after the rename, want %d", len(got), len(history))
				}
			})
			t.Run("an unchanged observation still writes", func(t *testing.T) {
				s, _, k, inst := pinnedLib(t, op)
				passOver(t, s, "sonarr", report{show, []mediasource.Item{episode("e1", 1, 1, epPath)}})
				if rec, _, _ := holderOf(t, s, k); InstanceID(rec.Inst) != inst || pendingCount(t, s) != 1 {
					t.Errorf("after an unchanged pass the key names instance %d with %d intents, want %d with one", rec.Inst, pendingCount(t, s), inst)
				}
			})
			t.Run("a complete pass that misses the item is refused until End", func(t *testing.T) {
				s, _, k, inst := pinnedLib(t, op)
				p := wholePass(t, s, "sonarr")
				before := headRev(t, s)
				_, err := s.FinishPass(t.Context(), "sonarr", p, true, mediasource.Cursor{}, nil)
				refusedPinned(t, s, err, before)
				if _, found := getRow(t, s, s.t.instances, inst); !found {
					t.Fatal("the refused retirement deleted the instance")
				}
				endOp(t, s, op, inst)
				finishWith(t, s, "sonarr", p, true, "c2")
				if _, _, found := holderOf(t, s, k); found {
					t.Error("the rerun retirement kept the key's record")
				}
				if _, found := getRow(t, s, s.t.instances, inst); found {
					t.Error("the rerun retirement kept the instance")
				}
			})
			t.Run("owner removal leaves it ownerless and unclaimable until End", func(t *testing.T) {
				s, _, k, inst := pinnedLib(t, op, "radarr")
				activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: policyOf(t, "en")})
				row, _ := getRow(t, s, s.t.instances, inst)
				if row.Owner != "" || row.VideoPath != epPath || !slices.Equal(keysOf(&row), []media.Key{k}) {
					t.Fatalf("after removal the instance is %+v, want ownerless at %s holding %s", row, epPath, k)
				}
				if pendingCount(t, s) != 1 || !locked(t, s, k, "en") || countRows(t, s, s.t.files, appendUint64(nil, uint64(inst))) == 0 {
					t.Fatal("owner removal dropped the intent, the lock or the file records")
				}
				claim := show
				claim.Native.ID = "radarr-73"
				p := wholePass(t, s, "radarr")
				before := headRev(t, s)
				err := s.ObserveItems(t.Context(), "radarr", p, claim, []mediasource.Item{episode("e1", 1, 1, epPath)})
				refusedPinned(t, s, err, before)
				endOp(t, s, op, inst)
				observe(t, s, "radarr", p, claim, episode("e1", 1, 1, epPath))
				if _, held, _ := holderOf(t, s, k); held.Owner != "radarr" {
					t.Errorf("after End the claim gave owner %q, want radarr", held.Owner)
				}
			})
			t.Run("expiry skips it until End", func(t *testing.T) {
				s, clock, _, inst := pinnedLib(t, op, "radarr")
				activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: policyOf(t, "en")})
				clock.advance(31 * 24 * time.Hour)
				passOver(t, s, "radarr")
				if _, found := getRow(t, s, s.t.instances, inst); !found {
					t.Fatal("expiry deleted a pinned instance")
				}
				endOp(t, s, op, inst)
				passOver(t, s, "radarr")
				if _, found := getRow(t, s, s.t.instances, inst); found {
					t.Error("after End, expiry kept the instance")
				}
			})
		})
	}
}

func TestIntentPinsItemSet(t *testing.T) {
	for _, op := range pinOps {
		t.Run(op.name, func(t *testing.T) {
			s, _, k1, inst := pinnedLib(t, op)
			k2 := epKey(t, showTVDB, 1, 2)
			both := []mediasource.Item{episode("e1", 1, 1, epPath), episode("e2", 1, 2, epPath)}
			p := wholePass(t, s, "sonarr")
			before := headRev(t, s)
			err := s.ObserveItems(t.Context(), "sonarr", p, show, both)
			refusedPinned(t, s, err, before)
			if _, found := sightingOf(t, s, "sonarr", show, "e2"); found {
				t.Error("the refused observation stored the new item's sighting")
			}
			finishWith(t, s, "sonarr", p, false, "c2")
			if got := cursorOf(t, s, "sonarr"); got != "c1" {
				t.Errorf("cursor %q after an incomplete pass, want c1", got)
			}
			endOp(t, s, op, inst)
			history := downloadBytes(t, s)
			passOver(t, s, "sonarr", report{show, both})
			_, held, _ := holderOf(t, s, k2)
			if !slices.Equal(keysOf(&held), []media.Key{k1, k2}) {
				t.Fatalf("after the rerun the instance holds %v, want %s and %s", keysOf(&held), k1, k2)
			}
			if _, configured := coverageOf(t, s, k2, "en", language.Standard); !configured {
				t.Error("the attached key has no coverage row")
			}
			if locked(t, s, k2, "en") {
				t.Error("the attached key got a lock from a delivery that did not name it")
			}
			if w, queued := storedWorkOf(t, s, k1); !queued || w.Cause != uint8(Scan) {
				t.Errorf("work %+v queued %v, want the two-item instance queued for Scan", w, queued)
			}
			if got := downloadBytes(t, s); len(got) != len(history) {
				t.Errorf("history %d rows, want %d", len(got), len(history))
			}
		})
	}
}

func TestIntentPinsKeyRelease(t *testing.T) {
	for _, op := range pinOps {
		t.Run(op.name, func(t *testing.T) {
			s, _ := newLibrary(t, "sonarr")
			k1, k2 := epKey(t, showTVDB, 1, 1), epKey(t, showTVDB, 1, 2)
			passOver(t, s, "sonarr", report{show, []mediasource.Item{episode("e1", 1, 1, epPath), episode("e2", 1, 2, epPath)}})
			rec, _, _ := holderOf(t, s, k1)
			inst := InstanceID(rec.Inst)
			deliver(t, s, pendingDeliver(t, inst, managedName, 1, k1))
			op.begin(t, s, inst, k1)
			split := []mediasource.Item{episode("e1", 1, 1, epPath), episode("e2", 1, 2, renamedPath)}
			p := wholePass(t, s, "sonarr")
			before := headRev(t, s)
			refusedPinned(t, s, s.ObserveItems(t.Context(), "sonarr", p, show, split), before)
			endOp(t, s, op, inst)
			observe(t, s, "sonarr", p, show, split...)
			if row, _ := getRow(t, s, s.t.instances, inst); !slices.Equal(keysOf(&row), []media.Key{k1}) {
				t.Errorf("after the rerun the instance holds %v, want only %s", keysOf(&row), k1)
			}
			if _, held, _ := holderOf(t, s, k2); held.VideoPath != renamedPath {
				t.Errorf("after the rerun %s is at %q, want %q", k2, held.VideoPath, renamedPath)
			}
		})
	}
}

func TestUndeliveredAttemptsAreNeverPruned(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	var earlier map[uint64]string
	for i := 1; i <= 21; i++ {
		beginDeliver(t, s, pendingDeliver(t, inst, managedName, byte(i), k))
		endDeliver(t, s, inst, managedName, false)
		rows := downloadBytes(t, s)
		if len(rows) != i {
			t.Fatalf("after %d undelivered calls %d rows", i, len(rows))
		}
		for seq, b := range earlier {
			if rows[seq] != b {
				t.Errorf("call %d rewrote row %d", i, seq)
			}
		}
		earlier = rows
	}
	for seq, b := range earlier {
		var row downloadRow
		if err := downloadCodec.Decode([]byte(b), &row); err != nil || row.Delivered {
			t.Errorf("row %d delivered %v (%v), want an undelivered attempt", seq, row.Delivered, err)
		}
	}
}

func TestEndDeliverKeepsOwnNameAmongRemoves(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	deliver(t, s, pendingDeliver(t, inst, "a.en.srt", 1, k))
	deliver(t, s, pendingDeliver(t, inst, "b.en.srt", 2, k))
	d := pendingDeliver(t, inst, "a.en.srt", 3, k)
	d.Removes = []Removal{{Name: "a.en.srt", SHA256: [32]byte{1}}, {Name: "b.en.srt", SHA256: [32]byte{2}}}
	deliver(t, s, d)
	a, found := getRow(t, s, s.t.files, fileKey{Inst: inst, Name: "a.en.srt"})
	if !found || a.Generation != 2 || a.Identity.SHA256 != (sum32{3}) {
		t.Errorf("a.en.srt is %+v (found %v), want the fresh record at generation 2", a, found)
	}
	if _, found := getRow(t, s, s.t.baselines, fileKey{Inst: inst, Name: "a.en.srt"}); !found {
		t.Error("the fresh record's baseline is gone")
	}
	if _, found := getRow(t, s, s.t.files, fileKey{Inst: inst, Name: "b.en.srt"}); found {
		t.Error("b.en.srt's record survived its supersede")
	}
	if _, found := getRow(t, s, s.t.baselines, fileKey{Inst: inst, Name: "b.en.srt"}); found {
		t.Error("b.en.srt's baseline survived its supersede")
	}
}

func TestEndDeliverOneTransaction(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	beginDeliver(t, s, pendingDeliver(t, inst, managedName, 1, k))
	before := headRev(t, s)
	endDeliver(t, s, inst, managedName, true)
	if got := headRev(t, s); got != before+1 {
		t.Fatalf("EndDeliver took revisions %d to %d, want one", before, got)
	}
	d := since(t, s, before)
	if !slices.Contains(d.Written, k.Root()) || !slices.Contains(d.Marks, MarkHistory) {
		t.Errorf("Since = %+v, want the root written and the history mark", d)
	}
	if pendingCount(t, s) != 0 || len(downloadBytes(t, s)) != 1 || !locked(t, s, k, "en") {
		t.Error("the landed End did not clear the intent, record the attempt and lock the item together")
	}
}

func TestEndDeliverFailedCommitKeepsIntent(t *testing.T) {
	s := faultyStore(t, errInjected)
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: policyOf(t, "en")})
	k, inst := showEpisode(t, s, "sonarr", epPath)
	beginDeliver(t, s, pendingDeliver(t, inst, managedName, 1, k))
	err := s.EndDeliver(markFailing(t.Context()), inst, managedName, true)
	if !errors.Is(err, bolttable.ErrCommit) {
		t.Fatalf("EndDeliver = %v, want ErrCommit", err)
	}
	if pendingCount(t, s) != 1 || len(downloadBytes(t, s)) != 0 || locked(t, s, k, "en") {
		t.Error("a failed End changed rows")
	}
	if _, found := getRow(t, s, s.t.files, fileKey{Inst: inst, Name: managedName}); found {
		t.Error("a failed End wrote the record")
	}
}

func TestEndRepeatedWritesNothing(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	deliver(t, s, pendingDeliver(t, inst, managedName, 1, k))
	before := headRev(t, s)
	for name, end := range map[string]func() error{
		"deliver": func() error { return s.EndDeliver(t.Context(), inst, managedName, true) },
		"retime":  func() error { return s.EndRetime(t.Context(), inst, managedName, true) },
		"delete":  func() error { return s.EndDelete(t.Context(), inst, managedName, false) },
		"unknown": func() error { return s.EndDeliver(t.Context(), 99, managedName, true) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := end(); err != nil {
				t.Errorf("End with no intent = %v, want nil", err)
			}
			if got := headRev(t, s); got != before {
				t.Errorf("Rev %d, want %d", got, before)
			}
		})
	}
	if n := len(downloadBytes(t, s)); n != 1 {
		t.Errorf("%d attempt rows, want the one landed", n)
	}
}

func TestEndDeleteRemovesUnmanagedName(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	k, inst := showEpisode(t, s, "sonarr", epPath)
	putExternal(t, s, inst, local(t, "x.en.srt", "en"), local(t, "y.fr.srt", "fr"))
	wantCoverage(t, s, k, "en", Exact)
	if err := s.BeginDelete(t.Context(), inst, "x.en.srt", [32]byte{4}); err != nil {
		t.Fatal(err)
	}
	before := headRev(t, s)
	if err := s.EndDelete(t.Context(), inst, "x.en.srt", true); err != nil {
		t.Fatal(err)
	}
	set, _ := getRow(t, s, s.t.local, inst)
	if len(set.Files) != 1 || set.Files[0].Name != "y.fr.srt" {
		t.Errorf("local set %+v, want only y.fr.srt", set.Files)
	}
	wantCoverage(t, s, k, "en", Missing)
	if d := since(t, s, before); !slices.Contains(d.Written, k.Root()) {
		t.Errorf("Since = %+v, want the root written", d)
	}
}

func TestBeginRefusals(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	deliver(t, s, pendingDeliver(t, inst, managedName, 1, k))
	beginDeliver(t, s, pendingDeliver(t, inst, "pending.en.srt", 2, k))
	other := epKey(t, showTVDB, 5, 5)
	cases := map[string]func() error{
		"a pending name": func() error { return s.BeginDeliver(t.Context(), pendingDeliver(t, inst, "pending.en.srt", 3, k)) },
		"an unknown instance": func() error {
			return s.BeginDeliver(t.Context(), pendingDeliver(t, 99, "a.en.srt", 3, k))
		},
		"an invalid timing": func() error {
			d := pendingDeliver(t, inst, "a.en.srt", 3, k)
			d.Record.Timing = timing.Shift{Ms: 0}
			return s.BeginDeliver(t.Context(), d)
		},
		"the zero baseline": func() error {
			d := pendingDeliver(t, inst, "a.en.srt", 3, k)
			d.Baseline = timing.Cues{}
			return s.BeginDeliver(t.Context(), d)
		},
		"no attempt": func() error {
			d := pendingDeliver(t, inst, "a.en.srt", 3, k)
			d.Attempts = nil
			return s.BeginDeliver(t.Context(), d)
		},
		"an attempt naming no item": func() error {
			return s.BeginDeliver(t.Context(), pendingDeliver(t, inst, "a.en.srt", 3))
		},
		"an attempt for another instance": func() error {
			d := pendingDeliver(t, inst, "a.en.srt", 3, k)
			d.Attempts = append(d.Attempts, attemptOf(inst+1, false, k))
			return s.BeginDeliver(t.Context(), d)
		},
		"a delivered attempt naming an item the instance does not hold": func() error {
			return s.BeginDeliver(t.Context(), pendingDeliver(t, inst, "a.en.srt", 3, other))
		},
		"no file name": func() error { return s.BeginDelete(t.Context(), inst, "", [32]byte{1}) },
		"a retime of an unmanaged name": func() error {
			return s.BeginRetime(t.Context(), recordAt(inst, "x.en.srt", enTag, 3), nil)
		},
		"an adoption of a managed name": func() error {
			c := cuesOf(t, "Hi")
			return s.BeginRetime(t.Context(), recordAt(inst, managedName, enTag, 3), &c)
		},
		"an adoption of the zero baseline": func() error {
			return s.BeginRetime(t.Context(), recordAt(inst, "x.en.srt", enTag, 3), &timing.Cues{})
		},
		"a delete on an unknown instance": func() error { return s.BeginDelete(t.Context(), 99, managedName, [32]byte{1}) },
		"an End of another op":            func() error { return s.EndRetime(t.Context(), inst, "pending.en.srt", true) },
		"a delivered attempt recorded undelivered": func() error {
			return s.RecordAttempts(t.Context(), []Download{attemptOf(inst, true, k)})
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			before, pending := headRev(t, s), pendingCount(t, s)
			if err := call(); err == nil {
				t.Fatal("accepted")
			}
			if headRev(t, s) != before || pendingCount(t, s) != pending {
				t.Error("a refusal wrote")
			}
		})
	}
	if _, found, err := s.PendingDeliverOf(t.Context(), inst, "pending.en.srt"); err != nil || !found {
		t.Errorf("the pending delivery is gone after refused calls: %v %v", found, err)
	}
}

func TestPendingDeliverRoundTrip(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	d := pendingDeliver(t, inst, managedName, 1, k)
	d.Record.Timing = timing.Linear{Scale: 1.001, OffsetMs: -40}
	d.Attempts = append(d.Attempts, attemptOf(inst, false, k))
	d.Attempts[1].Refusal = "different_cut"
	d.Removes = []Removal{{Name: "old.en.srt", SHA256: [32]byte{7}}}
	beginDeliver(t, s, d)
	got, found, err := s.PendingDeliverOf(t.Context(), inst, managedName)
	if err != nil || !found {
		t.Fatalf("PendingDeliverOf = %v, %v", found, err)
	}
	if got.Record.Timing != d.Record.Timing || *got.Record.Provenance != *d.Record.Provenance || got.Record.Name != managedName || got.Record.Tag != enTag {
		t.Errorf("record %+v, want %+v", got.Record, d.Record)
	}
	if len(got.Attempts) != 2 || got.Attempts[1].Refusal != "different_cut" || !slices.Equal(got.Attempts[0].Items, []media.Key{k}) {
		t.Errorf("attempts %+v", got.Attempts)
	}
	if !slices.Equal(got.Removes, d.Removes) || got.Baseline.Len() != d.Baseline.Len() {
		t.Errorf("removes %v, baseline of %d events", got.Removes, got.Baseline.Len())
	}
	ws, _ := s.PendingWrites(t.Context())
	if len(ws) != 1 || ws[0] != (Intent{Op: Deliver, Inst: inst, Name: managedName, SHA256: [32]byte{1}}) {
		t.Errorf("PendingWrites = %+v", ws)
	}
	endDeliver(t, s, inst, managedName, true)
	if b, found, err := s.Baseline(t.Context(), inst, managedName); err != nil || !found || b.Len() != 1 {
		t.Errorf("Baseline = %d events, %v, %v", b.Len(), found, err)
	}
}

func TestDownloadCandidateRoundTrip(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	manual := attemptOf(inst, false, k)
	manual.Provenance.Manual, manual.Candidate = true, [32]byte{0: 0xAA, 31: 0x01}
	if err := s.RecordAttempts(t.Context(), []Download{manual}); err != nil {
		t.Fatal(err)
	}
	notLanded := pendingDeliver(t, inst, managedName, 1, k)
	notLanded.Attempts[0].Candidate = [32]byte{0: 0xBB, 31: 0x02}
	beginDeliver(t, s, notLanded)
	endDeliver(t, s, inst, managedName, false)
	landed := pendingDeliver(t, inst, managedName, 2, k)
	landed.Attempts[0].Candidate = [32]byte{0: 0xCC, 31: 0x03}
	deliver(t, s, landed)
	want := [][32]byte{manual.Candidate, notLanded.Attempts[0].Candidate, landed.Attempts[0].Candidate}
	got := storedDownloads(t, s)
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d", len(got), len(want))
	}
	for i, d := range got {
		if d.Candidate != want[i] {
			t.Errorf("row %d candidate %x, want %x", i, d.Candidate, want[i])
		}
	}
	if !got[0].Provenance.Manual || got[1].Delivered || !got[2].Delivered {
		t.Errorf("rows %+v: want manual, undelivered, delivered", got)
	}
}

func storedDownloads(t *testing.T, s *Store) []Download {
	t.Helper()
	var out []Download
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) error {
		return s.t.downloads.Scan(r, bolttable.Range{}, func(_ uint64, v *downloadRow) (bool, error) {
			out = append(out, v.download())
			return true, nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRefusalStoredVerbatim(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	refusals := []string{"", "different_cut", "format_unsupported", "fetch_gone", "fetch_failed", "different_episode", "episode_count", "order_undetermined", "Not-A-Known_Spelling"}
	ds := make([]Download, 0, len(refusals))
	for _, r := range refusals {
		d := attemptOf(inst, false, k)
		d.Refusal = r
		ds = append(ds, d)
	}
	if err := s.RecordAttempts(t.Context(), ds); err != nil {
		t.Fatal(err)
	}
	got := storedDownloads(t, s)
	for i, r := range refusals {
		if i >= len(got) || got[i].Refusal != r {
			t.Errorf("refusal %d read back %q, want %q", i, got[min(i, len(got)-1)].Refusal, r)
		}
	}
}

func TestProvenanceFlagsRoundTrip(t *testing.T) {
	path := tempPath(t)
	s, _ := libraryStore(t, path, "sonarr")
	k, inst := showEpisode(t, s, "sonarr", epPath)
	d := pendingDeliver(t, inst, managedName, 1, k)
	d.Record.Provenance.MachineTranslated, d.Record.Provenance.Uploaded = true, true
	d.Attempts[0].Provenance.MachineTranslated, d.Attempts[0].Provenance.Uploaded = true, true
	deliver(t, s, d)
	check := func(t *testing.T, s *Store) {
		t.Helper()
		row, _ := getRow(t, s, s.t.files, fileKey{Inst: inst, Name: managedName})
		rec, err := row.record(fileKey{Inst: inst, Name: managedName})
		if err != nil || rec.Provenance == nil || !rec.Provenance.MachineTranslated || !rec.Provenance.Uploaded {
			t.Errorf("file record provenance %+v, %v; want both flags", rec.Provenance, err)
		}
		dl := storedDownloads(t, s)
		if len(dl) != 1 || !dl[0].Provenance.MachineTranslated || !dl[0].Provenance.Uploaded {
			t.Errorf("download rows %+v; want one with both flags", dl)
		}
	}
	check(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, _ = libraryStore(t, path, "sonarr")
	check(t, s)
}

func TestAdoptionLocksServablePairs(t *testing.T) {
	s := covLib(t, tempPath(t), pol(t, "default:\n  - tag: en\n  - tag: fr\n"))
	k, inst := showEpisode(t, s, "sonarr", epPath)
	putExternal(t, s, inst, local(t, "x.en.srt", "en"))
	rec := recordAt(inst, "x.en.srt", enTag, 5)
	rec.Provenance = nil
	c := cuesOf(t, "Hi")
	if err := s.BeginRetime(t.Context(), rec, &c); err != nil {
		t.Fatal(err)
	}
	if err := s.EndRetime(t.Context(), inst, "x.en.srt", true); err != nil {
		t.Fatal(err)
	}
	if !locked(t, s, k, "en") || locked(t, s, k, "fr") {
		t.Errorf("locks en %v fr %v, want en only", locked(t, s, k, "en"), locked(t, s, k, "fr"))
	}
	row, found := getRow(t, s, s.t.files, fileKey{Inst: inst, Name: "x.en.srt"})
	if !found || row.Provenance != nil || row.Generation != 1 {
		t.Errorf("adopted record %+v (found %v), want generation 1 with no provenance", row, found)
	}
	if _, found := getRow(t, s, s.t.baselines, fileKey{Inst: inst, Name: "x.en.srt"}); !found {
		t.Error("the adoption stored no baseline")
	}
}

func TestReserveOrdinalNeverBelowDiskOrReused(t *testing.T) {
	path := tempPath(t)
	s, _ := libraryStore(t, path, "sonarr")
	_, inst := showEpisode(t, s, "sonarr", epPath)
	k := OrdinalKey{Inst: inst, Tag: enTag}
	reserve := func(s *Store, k OrdinalKey, highest, want int) {
		t.Helper()
		got, err := s.ReserveOrdinal(t.Context(), k, highest)
		if err != nil || got != want {
			t.Errorf("ReserveOrdinal(%d) = %d, %v; want %d", highest, got, err, want)
		}
	}
	reserve(s, k, 0, 1)
	reserve(s, k, 0, 2)
	reserve(s, k, 5, 6)
	reserve(s, k, 3, 7)
	reserve(s, OrdinalKey{Inst: inst, Tag: enTag, Flags: language.Flags{HI: true}}, 0, 1)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, _ = libraryStore(t, path, "sonarr")
	reserve(s, k, 0, 8)
}

func TestReserveOrdinalBounds(t *testing.T) {
	path := tempPath(t)
	s, _ := libraryStore(t, path, "sonarr")
	_, inst := showEpisode(t, s, "sonarr", epPath)
	k := OrdinalKey{Inst: inst, Tag: enTag}
	if got, err := s.ReserveOrdinal(t.Context(), k, math.MaxInt-1); err != nil || got != math.MaxInt {
		t.Fatalf("ReserveOrdinal(MaxInt-1) = %d, %v; want MaxInt", got, err)
	}
	refused := func(s *Store, k OrdinalKey, highest int) {
		t.Helper()
		if got, err := s.ReserveOrdinal(t.Context(), k, highest); err == nil {
			t.Errorf("ReserveOrdinal(%d) = %d, want a refusal", highest, got)
		}
	}
	refused(s, k, 0)
	fresh := OrdinalKey{Inst: inst, Tag: enTag, Flags: language.Flags{Forced: true}}
	refused(s, fresh, math.MaxInt)
	if _, found := getRow(t, s, s.t.ordinals, fresh); found {
		t.Error("a refused reservation stored a ledger")
	}
	refused(s, fresh, -1)
	refused(s, OrdinalKey{Inst: 99, Tag: enTag}, 0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, _ = libraryStore(t, path, "sonarr")
	refused(s, k, 0)
}
