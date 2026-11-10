package mediawrite_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// crashDeliver delivers "new" to E01 of l, Lock set, crashing at point.
func crashDeliver(t *testing.T, l *lib, point string) {
	t.Helper()
	l.j.arm(point, false)
	d := delivery(t, l.insts[0], "new")
	d.Lock = true
	if crashed, err := crashes(func() error { _, err := l.w.Deliver(t.Context(), d); return err }); !crashed {
		t.Fatalf("no crash at %s: %v", point, err)
	}
}

func beginDelete(t *testing.T, l *lib, rec *statedb.FileRecord) {
	t.Helper()
	if err := l.store.BeginDelete(t.Context(), rec.Inst, rec.Name, rec.Identity.SHA256); err != nil {
		t.Fatal(err)
	}
}

// statFault makes Stat of path answer ENOENT while down holds, as an
// unmounted share's missing mount point does.
func statFault(path string, down *atomic.Bool) func(*mediawrite.Faults) {
	return func(f *mediawrite.Faults) {
		stat := f.Stat
		f.Stat = func(p string) (fs.FileInfo, error) {
			if down.Load() && p == path {
				return nil, &fs.PathError{Op: "stat", Path: p, Err: syscall.ENOENT}
			}
			return stat(p)
		}
	}
}

func (l *lib) pendingAlert() (string, bool) { return l.alerts.text(status.AlertPendingWrites) }

func TestNestedMountDownAtRecovery(t *testing.T) {
	t.Parallel()
	season := filepath.Join("Show", "Season 1")
	l := newLibIn(t, season, "E01.mkv", "E02.mkv")
	kept := landRecord(t, l.store, l.insts[1], l.keys[1], l.path("E02.en.srt"), srtOf("kept"))
	l.start(nil)
	crashDeliver(t, l, "published")
	beginDelete(t, l, &kept)
	empty := t.TempDir()
	var down atomic.Bool
	down.Store(true)
	l.restart(nil, func(f *mediawrite.Faults) {
		openDir := f.OpenDir
		f.OpenDir = func(parent *os.Root, name string) (*os.Root, error) {
			switch {
			case !down.Load():
			case name == season:
				return nil, &fs.PathError{Op: "openat", Path: name, Err: syscall.ENOENT}
			case name == "Show":
				return os.OpenRoot(empty)
			}
			return openDir(parent, name)
		}
	})

	if n := pendingCount(t, l.store); n != 2 {
		t.Fatalf("%d intents while the mount is down, want both kept", n)
	}
	eventually(t, "the pending_writes alert", func() bool { _, on := l.pendingAlert(); return on })
	if text, _ := l.pendingAlert(); !strings.Contains(text, "2 under "+l.root) {
		t.Errorf("pending_writes = %q, want the count and the root", text)
	}
	if _, found := l.record(l.insts[1], "E02.en.srt"); !found {
		t.Error("the pending delete settled landed while its file exists")
	}

	down.Store(false)
	l.w.RootsChanged()
	eventually(t, "the intents to settle", func() bool { return pendingCount(t, l.store) == 0 })
	eventually(t, "the alert to clear", func() bool { _, on := l.pendingAlert(); return !on })
	l.consistent()
	if rec, found := l.record(l.insts[0], ep1); !found || !strings.Contains(readFile(t, l.path(ep1)), "new") {
		t.Errorf("the delivery under the nested mount: record %v %+v, want landed", found, rec)
	}
	if _, found := l.record(l.insts[1], "E02.en.srt"); !found || readFile(t, l.path("E02.en.srt")) != srtOf("kept") {
		t.Error("the delete of a file still holding its bytes did not settle not landed")
	}
}

func TestRecoveryRemovesTornStage(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	l.start(nil)
	crashDeliver(t, l, "stage-torn")
	if len(l.stages()) != 1 || pendingCount(t, l.store) != 1 {
		t.Fatalf("after the crash: stages %q, %d intents; want the torn stage and its intent", l.stages(), pendingCount(t, l.store))
	}
	l.restart(nil, nil)

	l.consistent()
	if _, found := l.record(l.insts[0], ep1); found {
		t.Error("the delivery landed a record with no file")
	}
	if rows := l.attempts(l.keys[0], newCandidate); len(rows) != 1 || rows[0].Delivered {
		t.Errorf("attempt rows %+v, want one undelivered", rows)
	}
	if errs := l.logs.messages(slog.LevelError); len(errs) != 0 {
		t.Errorf("ERROR logs %q, want none for subflux's own stage", errs)
	}
	d := delivery(t, l.insts[0], "new")
	d.Candidate = sameCandidate
	if _, err := l.w.Deliver(t.Context(), d); err != nil {
		t.Fatalf("delivering the same subtitle again: %v", err)
	}
	if got := readFile(t, l.path(ep1)); got != srtOf("new") {
		t.Errorf("%s holds %q, want the delivery", ep1, got)
	}
}

func TestRenamedDirSettles(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv", "E02.mkv")
	gone := landRecord(t, l.store, l.insts[1], l.keys[1], l.path("E02.en.srt"), srtOf("gone"))
	l.start(nil)
	crashDeliver(t, l, "synced")
	beginDelete(t, l, &gone)
	moved := filepath.Join(l.root, "Show (2020)")
	l.restart(func() {
		if err := os.Rename(l.dir, moved); err != nil {
			t.Fatal(err)
		}
	}, nil)

	if n := pendingCount(t, l.store); n != 0 {
		t.Fatalf("%d intents after recovery, want both settled", n)
	}
	if _, found := l.record(l.insts[0], ep1); found {
		t.Error("the delivery into the renamed folder landed")
	}
	if rows := l.attempts(l.keys[0], newCandidate); len(rows) != 1 || rows[0].Delivered {
		t.Errorf("attempt rows %+v, want one undelivered", rows)
	}
	if _, found := l.record(l.insts[1], "E02.en.srt"); found {
		t.Error("the delete in the renamed folder did not land")
	}
	if got := dirNames(t, moved); !slices.Equal(got, []string{"E01.en.srt", "E01.mkv", "E02.en.srt", "E02.mkv"}) {
		t.Errorf("the renamed folder holds %q, want nothing removed", got)
	}
	videos := []string{filepath.Join(moved, "E01.mkv"), filepath.Join(moved, "E02.mkv")}
	if err := completePass(t, l.store, videos...); err != nil {
		t.Fatalf("a complete pass after recovery: %v", err)
	}
	row, _, err := l.store.ItemRow(t.Context(), l.keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if in, _, _ := l.store.Instance(t.Context(), row.Inst); in.VideoPath != videos[0] {
		t.Errorf("E01 is at %q after the pass, want %q", in.VideoPath, videos[0])
	}
}

func TestSiblingStageSurvivesRecovery(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv", "E02.mkv")
	fault := &failOnce{}
	var sibling func()
	settledE01, publishedE02 := make(chan struct{}), make(chan struct{})
	var once sync.Once
	l.start(func(f *mediawrite.Faults) {
		rename := f.RenameNoReplace
		f.RenameNoReplace = func(dir *os.File, from, to string) error {
			if to == ep1 && fault.take() {
				return &os.LinkError{Op: "renameat2", Old: from, New: to, Err: syscall.EIO}
			}
			err := rename(dir, from, to)
			if to == "E02.en.srt" {
				close(publishedE02)
			}
			return err
		}
		step := f.Step
		f.Step = func(s string) {
			step(s)
			switch {
			case s == "recover-synced":
				// E02's publish falls between E01's directory fsync and its
				// settle: the settle waits only for E01's own operations.
				once.Do(func() {
					close(settledE01)
					select {
					case <-publishedE02:
					case <-time.After(10 * time.Second):
						t.Error("E02 did not publish while E01's recovery waited to settle")
					}
				})
			case s == "stage" && sibling != nil:
				run := sibling
				sibling = nil
				run()
			}
		}
	})
	if _, err := l.w.Deliver(t.Context(), delivery(t, l.insts[0], "new")); !errors.Is(err, syscall.EIO) {
		t.Fatalf("Deliver E01 = %v, want EIO left for recovery", err)
	}
	recovered := make(chan bool, 1)
	sibling = func() {
		go func() { recovered <- mediawrite.RecoverNow(t.Context(), l.w, l.root) }()
		select {
		case <-settledE01:
		case <-time.After(10 * time.Second):
			t.Error("the recovery did not settle E01 while E02's stage exists")
		}
	}
	if _, err := l.w.Deliver(t.Context(), delivery(t, l.insts[1], "new")); err != nil {
		t.Errorf("Deliver E02 beside the recovery: %v", err)
	}
	if !<-recovered {
		t.Fatal("a recovery attempt was already running")
	}
	l.consistent()
	for i, name := range []string{ep1, "E02.en.srt"} {
		if _, found := l.record(l.insts[i], name); !found {
			t.Errorf("%s did not land", name)
		}
	}
}

func TestLongStemsTwoStages(t *testing.T) {
	t.Parallel()
	stem := strings.Repeat("a", 210)
	l := newLib(t, stem+"1.mkv", stem+"2.mkv")
	var created []string
	var nested func()
	l.start(func(f *mediawrite.Faults) {
		create := f.CreateStage
		f.CreateStage = func(dir *os.Root, name string) (*os.File, error) {
			created = append(created, name)
			return create(dir, name)
		}
		step := f.Step
		f.Step = func(s string) {
			step(s)
			if s == "stage" && nested != nil {
				run := nested
				nested = nil
				run()
			}
		}
	})
	nested = func() {
		if _, err := l.w.Deliver(t.Context(), delivery(t, l.insts[1], "same")); err != nil {
			t.Errorf("the second delivery, while the first's stage exists: %v", err)
		}
	}
	if _, err := l.w.Deliver(t.Context(), delivery(t, l.insts[0], "same")); err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || created[0] == created[1] || len(created[0]) != 41 || len(created[1]) != 41 {
		t.Errorf("stages %q, want two distinct 41-byte names", created)
	}
	l.consistent()
	for i := range 2 {
		if _, found := l.record(l.insts[i], stem+string(rune('1'+i))+".en.srt"); !found {
			t.Errorf("delivery %d did not land", i+1)
		}
	}
}

func TestRootUnmountedDuringDeliver(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	l.start(nil)
	crashDeliver(t, l, "published")
	var down atomic.Bool
	down.Store(true)
	l.restart(nil, statFault(l.root, &down))

	if n := pendingCount(t, l.store); n != 1 {
		t.Fatalf("%d intents while the root is unmounted, want 1", n)
	}
	if _, on := l.alerts.text(status.MediaRootAlert(l.root)); !on {
		t.Error("no media_root alert for the unmounted root")
	}
	eventually(t, "the pending_writes alert", func() bool { _, on := l.pendingAlert(); return on })

	down.Store(false)
	l.w.RootsChanged()
	eventually(t, "the intent to settle", func() bool { return pendingCount(t, l.store) == 0 })
	l.consistent()
	if _, found := l.record(l.insts[0], ep1); !found {
		t.Fatal("the delivery that landed before the unmount has no record")
	}
	if _, found, err := l.store.Baseline(t.Context(), l.insts[0], ep1); err != nil || !found {
		t.Errorf("baseline found %v (%v), want it stored", found, err)
	}
	if rows := l.attempts(l.keys[0], newCandidate); len(rows) != 1 || !rows[0].Delivered {
		t.Errorf("attempt rows %+v, want one delivered", rows)
	}
	if !l.locked(l.keys[0]) {
		t.Error("the landed delivery did not lock its pair")
	}
}

func TestRootRemovedWhilePending(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	l.start(nil)
	crashDeliver(t, l, "synced")
	l.roots.set(t.TempDir())
	l.restart(nil, nil)

	eventually(t, "the intent to settle", func() bool { return pendingCount(t, l.store) == 0 })
	if got := readFile(t, l.path(ep1)); !strings.Contains(got, "new") {
		t.Errorf("%s holds %q, want the published file left alone", ep1, got)
	}
	if _, found := l.record(l.insts[0], ep1); found {
		t.Error("the delivery under the removed root landed")
	}
	if rows := l.attempts(l.keys[0], newCandidate); len(rows) != 1 || rows[0].Delivered {
		t.Errorf("attempt rows %+v, want one undelivered", rows)
	}
	// The WARN follows the settling store write.
	eventually(t, "the WARN for the intent of the removed root", func() bool {
		return slices.ContainsFunc(l.logs.messages(slog.LevelWarn), func(m string) bool { return strings.Contains(m, "no longer configured") })
	})
	if err := completePass(t, l.store); err != nil {
		t.Errorf("a complete pass retiring the instance: %v", err)
	}
}

func TestSnapshotGainedNewIntent(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	l.start(nil)
	crashDeliver(t, l, "begin")
	other := []byte(srtOf("other"))
	cues, err := timing.ParseCues(other)
	if err != nil {
		t.Fatal(err)
	}
	newer := statedb.PendingDeliver{
		Record: statedb.FileRecord{
			Inst: l.insts[0], Name: ep1, Tag: enTag, Timing: timing.Identity{},
			Identity: statedb.Identity{Size: int64(len(other)), MtimeNs: 1, SHA256: sha256.Sum256(other)},
		},
		Baseline: cues,
		Attempts: []statedb.Download{{
			At: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC), Target: enTag, Variant: language.Standard, Items: []media.Key{l.keys[0]},
			Inst: l.insts[0], Run: statedb.RunID{2}, Attempt: 1, Delivered: true, Candidate: [32]byte{5},
		}},
	}
	var swapped atomic.Bool
	l.restart(nil, func(f *mediawrite.Faults) {
		step := f.Step
		f.Step = func(s string) {
			step(s)
			if s != "recover-snapshot" || !swapped.CompareAndSwap(false, true) {
				return
			}
			if err := l.store.EndDeliver(t.Context(), l.insts[0], ep1, false); err != nil {
				t.Error(err)
			}
			if err := l.store.BeginDeliver(t.Context(), newer); err != nil {
				t.Error(err)
			}
		}
	})

	intents, err := l.store.PendingWrites(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || intents[0].SHA256 != newer.Record.Identity.SHA256 {
		t.Errorf("intents %+v, want the newer one left pending", intents)
	}
}

func TestPendingWritesAlert(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv", "E02.mkv")
	other := filepath.Join(t.TempDir(), "other")
	writeFile(t, filepath.Join(other, "keep"), "x")
	l.roots.set(l.root, other)
	for i, name := range []string{ep1, "E02.en.srt"} {
		rec := landRecord(t, l.store, l.insts[i], l.keys[i], l.path(name), srtOf("held"))
		beginDelete(t, l, &rec)
	}
	var down atomic.Bool
	down.Store(true)
	l.start(statFault(l.root, &down))

	eventually(t, "the pending_writes alert", func() bool { _, on := l.pendingAlert(); return on })
	text, _ := l.pendingAlert()
	if !strings.Contains(text, "2 subtitle writes") || !strings.Contains(text, "2 under "+l.root) || strings.Contains(text, other) {
		t.Errorf("pending_writes = %q, want two writes under %s only", text, l.root)
	}

	down.Store(false)
	l.w.RootsChanged()
	eventually(t, "the alert to clear", func() bool { _, on := l.pendingAlert(); return !on })
	if n := pendingCount(t, l.store); n != 0 {
		t.Errorf("%d intents after the root returned", n)
	}
	l.consistent()
}

func TestUnreadInstanceCountedOnce(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	other := filepath.Join(t.TempDir(), "other")
	writeFile(t, filepath.Join(other, "keep"), "x")
	l.roots.set(l.root, other)
	rec := landRecord(t, l.store, l.insts[0], l.keys[0], l.path(ep1), srtOf("held"))
	beginDelete(t, l, &rec)
	var down atomic.Bool
	down.Store(true)
	l.start(func(f *mediawrite.Faults) {
		instance := f.Instance
		f.Instance = func(ctx context.Context, inst statedb.InstanceID) (statedb.Instance, bool, error) {
			if down.Load() {
				return statedb.Instance{}, false, errCommit
			}
			return instance(ctx, inst)
		}
	})

	eventually(t, "the pending_writes alert", func() bool { _, on := l.pendingAlert(); return on })
	if text, _ := l.pendingAlert(); !strings.HasPrefix(text, "1 subtitle writes") || !strings.Contains(text, "1 whose video cannot be looked up") ||
		strings.Contains(text, l.root) || strings.Contains(text, other) {
		t.Errorf("pending_writes = %q, want the one write counted once, under no root", text)
	}
	if n := len(l.logs.messages(slog.LevelWarn)); n == 0 {
		t.Error("no WARN for the instance that cannot be read")
	}

	down.Store(false)
	l.w.RootsChanged()
	eventually(t, "the alert to clear", func() bool { _, on := l.pendingAlert(); return !on })
	if n := pendingCount(t, l.store); n != 0 {
		t.Errorf("%d intents once the instance reads again", n)
	}
	l.consistent()
}

// An intent kept while its instance could not be read stays counted once
// the instance reads again, until its root's recovery settles it: the
// orphan pass that now finds it under the root does not end its count.
func TestAlertHoldsUntilTheOwningRootRecovers(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	rec := landRecord(t, l.store, l.insts[0], l.keys[0], l.path(ep1), srtOf("held"))
	beginDelete(t, l, &rec)
	var down, hold atomic.Bool
	down.Store(true)
	held, release := make(chan struct{}), make(chan struct{})
	orphansSettled := make(chan struct{}, 16)
	l.start(func(f *mediawrite.Faults) {
		instance, step := f.Instance, f.Step
		f.Instance = func(ctx context.Context, inst statedb.InstanceID) (statedb.Instance, bool, error) {
			if down.Load() {
				return statedb.Instance{}, false, errCommit
			}
			return instance(ctx, inst)
		}
		f.Step = func(s string) {
			step(s)
			if s == "recover-snapshot" && hold.CompareAndSwap(true, false) {
				close(held)
				<-release
			}
		}
		f.Pause = func(p string) {
			if p == "orphans-settled" && !down.Load() {
				orphansSettled <- struct{}{}
			}
		}
	})
	eventually(t, "the pending_writes alert", func() bool { _, on := l.pendingAlert(); return on })

	hold.Store(true)
	down.Store(false)
	l.w.RootsChanged()
	for _, wait := range []struct {
		ch   <-chan struct{}
		what string
	}{{held, "the root's recovery at its snapshot"}, {orphansSettled, "an orphan pass that reads the instance"}} {
		select {
		case <-wait.ch:
		case <-time.After(10 * time.Second):
			close(release)
			t.Fatalf("timed out waiting for %s", wait.what)
		}
	}
	text, on := l.pendingAlert()
	close(release)
	if !on || !strings.HasPrefix(text, "1 subtitle writes") || !strings.Contains(text, "1 under "+l.root) {
		t.Errorf("pending_writes = %q (on %v) while the root's recovery has not run, want the write counted under its root", text, on)
	}
	eventually(t, "the alert to clear", func() bool { _, on := l.pendingAlert(); return !on })
	if n := pendingCount(t, l.store); n != 0 {
		t.Errorf("%d intents once the alert cleared", n)
	}
	l.consistent()
}

// An intent kept under an unavailable root stays counted when its instance
// turns unreadable: the root's next recovery skips it, and only a count
// that no longer finds it stored ends its count.
func TestAlertHoldsWhenTheInstanceTurnsUnreadable(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	rec := landRecord(t, l.store, l.insts[0], l.keys[0], l.path(ep1), srtOf("held"))
	beginDelete(t, l, &rec)
	var rootDown, instDown atomic.Bool
	rootDown.Store(true)
	l.start(func(f *mediawrite.Faults) {
		statFault(l.root, &rootDown)(f)
		instance := f.Instance
		f.Instance = func(ctx context.Context, inst statedb.InstanceID) (statedb.Instance, bool, error) {
			if instDown.Load() {
				return statedb.Instance{}, false, errCommit
			}
			return instance(ctx, inst)
		}
	})
	eventually(t, "the write counted under its root", func() bool {
		text, on := l.pendingAlert()
		return on && strings.Contains(text, "1 under "+l.root)
	})

	instDown.Store(true)
	if !mediawrite.RecoverNow(t.Context(), l.w, l.root) {
		t.Fatal("a recovery attempt was already running")
	}
	if text, on := l.pendingAlert(); !on || !strings.HasPrefix(text, "1 subtitle writes") || !strings.Contains(text, "1 whose video cannot be looked up") {
		t.Errorf("pending_writes = %q (on %v) after a recovery that cannot read the instance, want the write still counted once", text, on)
	}
	if n := pendingCount(t, l.store); n != 1 {
		t.Errorf("%d intents, want the delete still pending", n)
	}
}

// A count that read the pending writes before an intent existed leaves the
// intent another pass kept meanwhile to that pass's own count.
func TestCountKeepsAnIntentKeptAfterItsRead(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	var rootDown, hold atomic.Bool
	rootDown.Store(true)
	held, release := make(chan struct{}), make(chan struct{})
	l.start(func(f *mediawrite.Faults) {
		statFault(l.root, &rootDown)(f)
		f.Pause = func(p string) {
			if p == "pending-read" && hold.CompareAndSwap(true, false) {
				close(held)
				<-release
			}
		}
	})

	hold.Store(true)
	l.w.RootsChanged()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("no count read the pending writes")
	}
	rec := landRecord(t, l.store, l.insts[0], l.keys[0], l.path(ep1), srtOf("held"))
	beginDelete(t, l, &rec)
	l.w.RootsChanged()
	for deadline := time.Now().Add(10 * time.Second); mediawrite.Outlived(l.w) != 1; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("no recovery kept the delete under its unavailable root")
		}
	}
	close(release)
	eventually(t, "the delete counted under its root", func() bool {
		text, on := l.pendingAlert()
		return on && strings.Contains(text, "1 under "+l.root)
	})
}

// gate holds the next recovery at its snapshot until release closes.
type gate struct{ reached, release chan struct{} }

func newGate() *gate { return &gate{reached: make(chan struct{}), release: make(chan struct{})} }

func (g *gate) await(t *testing.T) {
	t.Helper()
	select {
	case <-g.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("no recovery reached its snapshot")
	}
}

func TestCallsWaitForRecovery(t *testing.T) {
	t.Parallel()
	l := newLib(t, "E01.mkv")
	l.start(nil)
	crashDeliver(t, l, "published")
	var next atomic.Pointer[gate]
	var down atomic.Bool
	boot := newGate()
	next.Store(boot)
	l.stop()
	if err := l.store.Close(); err != nil {
		t.Fatal(err)
	}
	l.store = openStoreAt(t, l.storePath, enPolicy)
	l.launch(func(f *mediawrite.Faults) {
		statFault(l.root, &down)(f)
		step := f.Step
		f.Step = func(s string) {
			step(s)
			if s != "recover-snapshot" {
				return
			}
			if g := next.Swap(nil); g != nil {
				close(g.reached)
				<-g.release
			}
		}
	})
	list := func(ctx context.Context) error {
		_, err := l.w.List(ctx, l.insts[0], mediawrite.ListOptions{})
		return err
	}

	t.Run("at boot a call waits", func(t *testing.T) {
		boot.await(t)
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		if err := list(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("List during the boot recovery = %v, want it waiting", err)
		}
		close(boot.release)
		if err := list(t.Context()); err != nil {
			t.Errorf("List after the boot recovery: %v", err)
		}
		if _, found := l.record(l.insts[0], ep1); !found {
			t.Error("the boot recovery did not land the delivery")
		}
	})
	t.Run("under a returned root a call fails until recovered", func(t *testing.T) {
		down.Store(true)
		l.w.RootsChanged()
		eventually(t, "the root to go", func() bool { return !mediawrite.Available(l.w, l.root) })
		back := newGate()
		next.Store(back)
		down.Store(false)
		l.w.RootsChanged()
		eventually(t, "the root to return", func() bool { return mediawrite.Available(l.w, l.root) })
		back.await(t)
		if err := list(t.Context()); !errors.Is(err, mediawrite.ErrRootUnavailable) {
			t.Errorf("List during the recovery = %v, want ErrRootUnavailable", err)
		}
		close(back.release)
		eventually(t, "the root to recover", func() bool { return list(t.Context()) == nil })
	})
}
