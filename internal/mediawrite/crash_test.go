package mediawrite_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

const enPolicy = "default:\n  - tag: en\n"

// errCommit is a store write that did not commit: statedb's own commit
// fault is internal to it, so the End seam fails before the store writes.
var errCommit = fmt.Errorf("commit failed: %w", syscall.EIO)

// errCrash stops a write where a crash would: the armed crash point panics
// with it in the caller's goroutine.
var errCrash = errors.New("injected crash")

// lib is one library on disk: a media root holding one season folder, a
// store file, and the writer running over them, which restart replaces as
// a process restart would.
type lib struct {
	t         *testing.T
	j         *journal
	store     *statedb.Store
	w         *mediawrite.Writer
	alerts    *fakeAlerts
	roots     *fakeRoots
	logs      *logRecords
	stop      func()
	root      string
	dir       string
	storePath string
	insts     []statedb.InstanceID
	keys      []media.Key
}

// newLib is a library whose season folder is Show, holding videos.
func newLib(t *testing.T, videos ...string) *lib {
	t.Helper()
	return newLibIn(t, "Show", videos...)
}

// newLibIn is a library whose season folder is sub, below the root.
func newLibIn(t *testing.T, sub string, videos ...string) *lib {
	t.Helper()
	tmp := t.TempDir()
	l := &lib{t: t, root: filepath.Join(tmp, "media"), storePath: filepath.Join(tmp, "subflux.bolt"), alerts: &fakeAlerts{}, logs: &logRecords{}}
	l.dir = filepath.Join(l.root, sub)
	l.roots = rootsOf(l.root)
	l.j = &journal{t: t, l: l, dir: l.dir}
	paths := make([]string, 0, len(videos))
	for _, v := range videos {
		paths = append(paths, filepath.Join(l.dir, v))
		writeFile(t, paths[len(paths)-1], "video")
	}
	l.store = openStoreAt(t, l.storePath, enPolicy)
	l.insts, l.keys = seedKeys(t, l.store, paths...)
	return l
}

// start runs a writer over the library through the journal's seams, then
// inject's, and waits for its boot recovery.
func (l *lib) start(inject func(*mediawrite.Faults)) {
	l.t.Helper()
	l.launch(inject)
	if err := mediawrite.Settled(l.t.Context(), l.w); err != nil {
		l.t.Fatal(err)
	}
}

// launch is start without the wait.
func (l *lib) launch(inject func(*mediawrite.Faults)) {
	l.t.Helper()
	l.j.reset()
	l.w = newWriter(l.t, mediawrite.Deps{Store: l.store, Roots: l.roots, Alerts: l.alerts, Logger: slog.New(l.logs)}, func(f *mediawrite.Faults) {
		l.j.wrap(f)
		if inject != nil {
			inject(f)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.w.Run(ctx) }()
	var once sync.Once
	l.stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				l.t.Errorf("Run: %v", err)
			}
		})
	}
	l.t.Cleanup(l.stop)
}

// restart stops the writer and closes the store, runs between, then
// reopens the store and starts a fresh writer, as a process restart would.
func (l *lib) restart(between func(), inject func(*mediawrite.Faults)) {
	l.t.Helper()
	l.stop()
	if err := l.store.Close(); err != nil {
		l.t.Fatal(err)
	}
	if between != nil {
		between()
	}
	l.store = openStoreAt(l.t, l.storePath, enPolicy)
	l.start(inject)
}

func (l *lib) path(name string) string { return filepath.Join(l.dir, name) }

func (l *lib) ref(inst statedb.InstanceID, name string) mediawrite.FileRef {
	l.t.Helper()
	r, err := mediawrite.DecodeRef(inst, name)
	if err != nil {
		l.t.Fatal(err)
	}
	return r
}

// record is inst's record of name; false when it has none.
func (l *lib) record(inst statedb.InstanceID, name string) (statedb.FileRecord, bool) {
	l.t.Helper()
	recs, err := l.store.Files(l.t.Context(), inst)
	if err != nil {
		l.t.Fatal(err)
	}
	for _, r := range recs {
		if r.Name == name {
			return r, true
		}
	}
	return statedb.FileRecord{}, false
}

// stages are the stage names in the season folder.
func (l *lib) stages() []string {
	l.t.Helper()
	var out []string
	for _, n := range dirNames(l.t, l.dir) {
		if strings.HasPrefix(n, ".sfx-") {
			out = append(out, n)
		}
	}
	return out
}

// consistent checks what every recovery must leave: no intent, no stage,
// and each record's file holding the record's bytes.
func (l *lib) consistent() {
	l.t.Helper()
	if n := pendingCount(l.t, l.store); n != 0 {
		l.t.Errorf("%d intents left after recovery", n)
	}
	if s := l.stages(); len(s) != 0 {
		l.t.Errorf("stages left after recovery: %q", s)
	}
	for _, inst := range l.insts {
		recs, err := l.store.Files(l.t.Context(), inst)
		if err != nil {
			l.t.Fatal(err)
		}
		for _, r := range recs {
			b, err := os.ReadFile(l.path(r.Name))
			if err != nil || sha256.Sum256(b) != r.Identity.SHA256 {
				l.t.Errorf("record %s does not match its file (%v)", r.Name, err)
			}
		}
	}
}

// attempts are the history rows of key k delivered by candidate.
func (l *lib) attempts(k media.Key, candidate [32]byte) []statedb.Download {
	l.t.Helper()
	var out []statedb.Download
	for _, r := range history(l.t, l.store, k) {
		if r.Candidate == candidate {
			out = append(out, r)
		}
	}
	return out
}

// locked reports whether k's en Standard pair is locked.
func (l *lib) locked(k media.Key) bool {
	l.t.Helper()
	rows, err := l.store.Coverage(l.t.Context(), k)
	if err != nil {
		l.t.Fatal(err)
	}
	for _, r := range rows {
		if r.Target == enTag {
			return r.Locked
		}
	}
	return false
}

// crashes runs op and reports whether it stopped at the armed crash point.
func crashes(op func() error) (crashed bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			if r != any(errCrash) {
				panic(r)
			}
			crashed = true
		}
	}()
	return false, op()
}

// eventually polls cond every 10 ms for up to 10 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// journal records the writer's directory operations in the season folder
// since its last directory fsync, so a test can drop any suffix of them as
// a power loss would, and stops the writer at one armed crash point: a step
// name, "fsync#<n>", the writer's n-th directory fsync, before it syncs,
// "stage-torn", half of a stage's bytes written, or "stage-fsynced",
// between the stage's fsync and its close.
// A crash in the caller's goroutine panics with errCrash; one armed with
// goexit ends a recovery's goroutine instead.
type journal struct {
	t       *testing.T
	l       *lib
	ops     []dirOp
	points  []string
	crashAt string
	dir     string
	fsyncs  int
	mu      sync.Mutex
	goexit  bool
}

// dirOp is one directory operation: names are the entries it changed.
type dirOp struct {
	undo  func() error
	what  string
	names []string
}

func (j *journal) reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.points, j.fsyncs = nil, 0
}

func (j *journal) arm(point string, goexit bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.crashAt, j.goexit = point, goexit
}

// seen are the crash points passed since the writer started.
func (j *journal) seen() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.points)
}

func (j *journal) unsynced() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.ops)
}

// unsyncedOf counts the unsynced operations that changed one of names.
func (j *journal) unsyncedOf(names []string) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for _, op := range j.ops {
		if slices.ContainsFunc(op.names, func(name string) bool { return slices.Contains(names, name) }) {
			n++
		}
	}
	return n
}

func (j *journal) add(what string, undo func() error, names ...string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ops = append(j.ops, dirOp{what: what, undo: undo, names: names})
}

// entriesOf are the entries the pending intent of name beside inst's video
// changes: name, its stage and the names a delivery supersedes. A sibling
// write's operations in the same directory are not this intent's.
func (j *journal) entriesOf(ctx context.Context, inst statedb.InstanceID, name string) []string {
	intents, err := j.l.store.PendingWrites(ctx)
	if err != nil {
		j.t.Errorf("the pending writes: %v", err)
	}
	names := []string{name}
	for _, it := range intents {
		if it.Inst != inst || it.Name != name {
			continue
		}
		names = append(names, mediawrite.StageName(inst, name, it.SHA256))
		if it.Op != statedb.Deliver {
			break
		}
		d, found, err := j.l.store.PendingDeliverOf(ctx, inst, name)
		if err != nil || !found {
			j.t.Errorf("the pending delivery of %s: found %v, %v", name, found, err)
		}
		for _, r := range d.Removes {
			names = append(names, r.Name)
		}
	}
	return names
}

func (j *journal) at(point string) {
	j.mu.Lock()
	j.points = append(j.points, point)
	crash, goexit := point == j.crashAt, j.goexit
	if crash {
		j.crashAt = ""
	}
	j.mu.Unlock()
	switch {
	case !crash:
	case goexit:
		runtime.Goexit()
	default:
		panic(errCrash)
	}
}

// powerLoss undoes the last n unsynced operations, newest first, and
// treats the rest as durable.
func (j *journal) powerLoss(n int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := len(j.ops) - 1; i >= len(j.ops)-n; i-- {
		if err := j.ops[i].undo(); err != nil {
			j.t.Fatalf("undo %s: %v", j.ops[i].what, err)
		}
	}
	j.ops = nil
}

func (j *journal) path(name string) string { return filepath.Join(j.dir, name) }

// saved restores name's current bytes and modification time, or its
// absence.
func (j *journal) saved(name string) func() error {
	p := j.path(name)
	info, err := os.Lstat(p)
	if err != nil {
		return func() error { return nil }
	}
	b, err := os.ReadFile(p)
	if err != nil {
		j.t.Fatal(err)
	}
	return func() error {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
		return os.Chtimes(p, info.ModTime(), info.ModTime())
	}
}

func (j *journal) wrap(f *mediawrite.Faults) {
	createStage, renameNoReplace, link, rename, remove := f.CreateStage, f.RenameNoReplace, f.Link, f.Rename, f.Remove
	write, fsync, fsyncDir, step, end := f.Write, f.Fsync, f.FsyncDir, f.Step, f.End
	f.Write = func(file *os.File, b []byte) error {
		if err := write(file, b[:len(b)/2]); err != nil {
			return err
		}
		j.at("stage-torn")
		return write(file, b[len(b)/2:])
	}
	f.Fsync = func(file *os.File) error {
		err := fsync(file)
		if err == nil {
			j.at("stage-fsynced")
		}
		return err
	}
	f.CreateStage = func(dir *os.Root, name string) (*os.File, error) {
		file, err := createStage(dir, name)
		if err == nil {
			j.add("create "+name, func() error { return os.Remove(j.path(name)) }, name)
		}
		return file, err
	}
	f.RenameNoReplace = func(dir *os.File, from, to string) error {
		err := renameNoReplace(dir, from, to)
		if err == nil {
			j.add("rename "+from+" to "+to, func() error { return os.Rename(j.path(to), j.path(from)) }, from, to)
		}
		return err
	}
	f.Link = func(dir *os.Root, from, to string) error {
		err := link(dir, from, to)
		if err == nil {
			j.add("link "+from+" to "+to, func() error { return os.Remove(j.path(to)) }, to)
		}
		return err
	}
	f.Rename = func(dir *os.Root, from, to string) error {
		restore := j.saved(to)
		err := rename(dir, from, to)
		if err == nil {
			j.add("rename "+from+" over "+to, func() error {
				if err := os.Rename(j.path(to), j.path(from)); err != nil {
					return err
				}
				return restore()
			}, from, to)
		}
		return err
	}
	f.Remove = func(dir *os.Root, name string) error {
		restore := j.saved(name)
		err := remove(dir, name)
		if err == nil {
			j.add("remove "+name, restore, name)
		}
		return err
	}
	f.FsyncDir = func(d *os.File) error {
		j.mu.Lock()
		j.fsyncs++
		point := fmt.Sprintf("fsync#%d", j.fsyncs)
		j.mu.Unlock()
		j.at(point)
		err := fsyncDir(d)
		if err == nil {
			j.mu.Lock()
			j.ops = nil
			j.mu.Unlock()
		}
		return err
	}
	f.Step = func(s string) {
		j.at(s)
		step(s)
	}
	f.End = func(ctx context.Context, op statedb.Op, inst statedb.InstanceID, name string, landed bool) error {
		if n := j.unsyncedOf(j.entriesOf(ctx, inst, name)); n > 0 {
			j.t.Errorf("the intent of %s settled with %d of its directory operations unsynced", name, n)
		}
		return end(ctx, op, inst, name, landed)
	}
}

// scenario is one write the crash suite stops at every boundary: setup
// runs before the first writer starts, act is the write, and check judges
// the state recovery left, landed when the write's outcome should be on
// disk. landsFrom is the first crash point from which recovery lands it.
type scenario struct {
	setup     func(l *lib)
	act       func(l *lib) error
	check     func(l *lib, landed bool)
	inject    func(*mediawrite.Faults)
	name      string
	landsFrom string
}

const ep1 = "E01.en.srt"

var (
	newCandidate  = [32]byte{7}
	sameCandidate = [32]byte{8}
)

func scenarios() []scenario {
	deliverNew := func(l *lib) error {
		d := delivery(l.t, l.insts[0], "new")
		d.Lock = true
		_, err := l.w.Deliver(l.t.Context(), d)
		return err
	}
	checkNew := func(l *lib, landed bool) {
		rec, found := l.record(l.insts[0], ep1)
		b, err := os.ReadFile(l.path(ep1))
		switch {
		case landed && (err != nil || !strings.Contains(string(b), "new") || !found):
			l.t.Errorf("not landed: file %q (%v), record %v", b, err, found)
		case !landed && (!errors.Is(err, fs.ErrNotExist) || found):
			l.t.Errorf("landed: file %q (%v), record %+v", b, err, rec)
		}
		if l.locked(l.keys[0]) != landed {
			l.t.Errorf("locked = %v, want %v", !landed, landed)
		}
		rows := l.attempts(l.keys[0], newCandidate)
		if len(rows) != 1 || rows[0].Delivered != landed {
			l.t.Errorf("attempt rows %+v, want one delivered=%v", rows, landed)
		}
	}
	return []scenario{
		{name: "deliver", landsFrom: "stage-fsynced", act: deliverNew, check: checkNew},
		{
			name: "deliver by hard link", landsFrom: "stage-fsynced", act: deliverNew, check: checkNew,
			inject: func(f *mediawrite.Faults) {
				f.RenameNoReplace = func(*os.File, string, string) error { return syscall.EINVAL }
			},
		},
		{
			name: "deliver over the same name", landsFrom: "same-name-removed",
			setup: func(l *lib) {
				landRecord(l.t, l.store, l.insts[0], l.keys[0], l.path(ep1), srtOf("old"))
				landRecord(l.t, l.store, l.insts[0], l.keys[0], l.path("E01.en.1.srt"), srtOf("old one"))
			},
			act: func(l *lib) error {
				d := delivery(l.t, l.insts[0], "new")
				for _, name := range []string{ep1, "E01.en.1.srt"} {
					rec, _ := l.record(l.insts[0], name)
					d.Supersedes = append(d.Supersedes, mediawrite.Superseded{
						File: l.ref(l.insts[0], name), Expect: mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256},
					})
				}
				_, err := l.w.Deliver(l.t.Context(), d)
				return err
			},
			check: func(l *lib, landed bool) {
				got := readFile(l.t, l.path(ep1))
				_, other := l.record(l.insts[0], "E01.en.1.srt")
				_, otherErr := os.Stat(l.path("E01.en.1.srt"))
				switch {
				case landed && (!strings.Contains(got, "new") || other || otherErr == nil):
					l.t.Errorf("not landed: %s holds %q, the other supersede's record %v, file %v", ep1, got, other, otherErr)
				case !landed && (got != srtOf("old") || !other || otherErr != nil):
					l.t.Errorf("landed: %s holds %q, the other supersede's record %v, file %v", ep1, got, other, otherErr)
				}
				if rows := l.attempts(l.keys[0], newCandidate); len(rows) != 1 || rows[0].Delivered != landed {
					l.t.Errorf("attempt rows %+v, want one delivered=%v", rows, landed)
				}
			},
		},
		{
			name: "retime", landsFrom: "published",
			setup: func(l *lib) { landRecord(l.t, l.store, l.insts[0], l.keys[0], l.path(ep1), srtOf("old")) },
			act: func(l *lib) error {
				rec, _ := l.record(l.insts[0], ep1)
				_, err := l.w.Retime(l.t.Context(), l.ref(l.insts[0], ep1), mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}, timing.Shift{Ms: 1000}, nil)
				return err
			},
			check: func(l *lib, landed bool) {
				rec, _ := l.record(l.insts[0], ep1)
				_, shifted := rec.Timing.(timing.Shift)
				if old := readFile(l.t, l.path(ep1)) == srtOf("old"); old == landed || shifted != landed {
					l.t.Errorf("file still old %v, record shifted %v; want landed=%v", old, shifted, landed)
				}
			},
		},
		{
			name: "delete", landsFrom: "removed",
			setup: func(l *lib) { landRecord(l.t, l.store, l.insts[0], l.keys[0], l.path(ep1), srtOf("old")) },
			act: func(l *lib) error {
				rec, _ := l.record(l.insts[0], ep1)
				return l.w.Delete(l.t.Context(), l.ref(l.insts[0], ep1), mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256})
			},
			check: func(l *lib, landed bool) {
				_, found := l.record(l.insts[0], ep1)
				_, err := os.Stat(l.path(ep1))
				if gone := errors.Is(err, fs.ErrNotExist); gone != landed || found == landed {
					l.t.Errorf("file gone %v, record kept %v; want landed=%v", gone, found, landed)
				}
			},
		},
	}
}

func (sc *scenario) lib(t *testing.T) *lib {
	t.Helper()
	l := newLib(t, "E01.mkv")
	if sc.setup != nil {
		sc.setup(l)
	}
	l.start(sc.inject)
	return l
}

// points are the crash points sc's write passes, in order; the write and
// its check must pass uncrashed.
func (sc *scenario) points(t *testing.T) []string {
	t.Helper()
	l := sc.lib(t)
	boot := len(l.j.seen())
	if err := sc.act(l); err != nil {
		t.Fatalf("%s: %v", sc.name, err)
	}
	sc.check(l, true)
	points := l.j.seen()[boot:]
	if !slices.Contains(points, sc.landsFrom) {
		t.Fatalf("%s passes %q, not %q", sc.name, points, sc.landsFrom)
	}
	return points
}

// crashAt runs sc's write until it crashes at point, checking that no
// intent settled while its stage exists.
func (sc *scenario) crashAt(t *testing.T, point string) *lib {
	t.Helper()
	l := sc.lib(t)
	l.j.arm(point, false)
	if crashed, err := crashes(func() error { return sc.act(l) }); !crashed {
		t.Fatalf("no crash at %s: %v", point, err)
	}
	if len(l.stages()) > 0 && pendingCount(t, l.store) == 0 {
		t.Errorf("a stage outlived its intent: %q", l.stages())
	}
	return l
}

func TestCrashAtEveryBoundary(t *testing.T) {
	t.Parallel()
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			points := sc.points(t)
			from := slices.Index(points, sc.landsFrom)
			for i, point := range points {
				t.Run(point, func(t *testing.T) {
					t.Parallel()
					l := sc.crashAt(t, point)
					l.restart(nil, sc.inject)
					l.consistent()
					sc.check(l, i >= from)
				})
			}
		})
	}
}

// recoveryPoints are the crash points a recovery can pass.
var recoveryPoints = []string{"recover-snapshot", "recover-published", "recover-removed", "fsync#1", "recover-synced"}

func TestPowerLossDropsDirSuffix(t *testing.T) {
	t.Parallel()
	for _, sc := range scenarios() {
		if sc.setup == nil || sc.name == "delete" {
			continue // only a replace holds an old and a new file
		}
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			for _, point := range sc.points(t) {
				t.Run(point, func(t *testing.T) {
					t.Parallel()
					n := sc.crashAt(t, point).j.unsynced()
					for drop := range n + 1 {
						t.Run(fmt.Sprint("drop", drop), func(t *testing.T) {
							t.Parallel()
							powerLossCase(t, &sc, point, drop)
						})
					}
				})
			}
		})
	}
}

// powerLossCase crashes sc at point, drops the last drop unsynced
// operations, then crashes the recovery at each of its points with each
// drop of its own, and checks that the final recovery keeps the old or the
// new file.
func powerLossCase(t *testing.T, sc *scenario, point string, drop int) {
	t.Helper()
	settled := sc.crashAt(t, point)
	settled.restart(func() { settled.j.powerLoss(drop) }, sc.inject)
	settled.consistent()
	keepsOne(settled)
	for _, rp := range recoveryPoints {
		t.Run(rp, func(t *testing.T) {
			probe := func(t *testing.T) (*lib, bool) {
				l := sc.crashAt(t, point)
				l.restart(func() {
					l.j.powerLoss(drop)
					l.j.arm(rp, true)
				}, sc.inject)
				return l, !slices.Contains(l.j.seen(), rp)
			}
			l, missed := probe(t)
			if missed {
				return
			}
			for drop2 := range l.j.unsynced() + 1 {
				l2, _ := probe(t)
				l2.restart(func() { l2.j.powerLoss(drop2) }, sc.inject)
				l2.consistent()
				keepsOne(l2)
			}
		})
	}
}

// keepsOne checks that E01.en.srt holds the whole old file or the whole
// new one.
func keepsOne(l *lib) {
	l.t.Helper()
	b, err := os.ReadFile(l.path(ep1))
	if err != nil {
		l.t.Fatalf("both the old and the new file are lost: %v", err)
	}
	cues, err := timing.ParseCues(b)
	if s := string(b); err != nil || len(cues.Events()) != 1 || !strings.Contains(s, "old") && !strings.Contains(s, "new") {
		l.t.Errorf("%s holds %q (%v), neither the old file nor the new one", ep1, s, err)
	}
}

func TestSameNameIdenticalBytesCrash(t *testing.T) {
	t.Parallel()
	first := func(l *lib) statedb.FileRecord {
		rec, err := l.w.Deliver(l.t.Context(), delivery(l.t, l.insts[0], "same"))
		if err != nil {
			l.t.Fatal(err)
		}
		return rec
	}
	again := func(l *lib, rec statedb.FileRecord) error {
		d := delivery(l.t, l.insts[0], "same")
		d.Candidate = sameCandidate
		d.Supersedes = []mediawrite.Superseded{{File: l.ref(l.insts[0], ep1), Expect: mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}}}
		_, err := l.w.Deliver(l.t.Context(), d)
		return err
	}
	dry := newLib(t, "E01.mkv")
	dry.start(nil)
	rec := first(dry)
	boot := len(dry.j.seen())
	if err := again(dry, rec); err != nil {
		t.Fatal(err)
	}
	for _, point := range dry.j.seen()[boot:] {
		t.Run(point, func(t *testing.T) {
			t.Parallel()
			l := newLib(t, "E01.mkv")
			l.start(nil)
			rec := first(l)
			l.j.arm(point, false)
			if crashed, err := crashes(func() error { return again(l, rec) }); !crashed {
				t.Fatalf("no crash at %s: %v", point, err)
			}
			l.restart(nil, nil)
			l.consistent()
			got, found := l.record(l.insts[0], ep1)
			if !found || got.Identity.SHA256 != rec.Identity.SHA256 || readFile(t, l.path(ep1)) == "" {
				t.Errorf("record %v %+v, want the file and its record present", found, got)
			}
			if rows := l.attempts(l.keys[0], sameCandidate); len(rows) != 1 {
				t.Errorf("attempt rows %+v, want one", rows)
			}
		})
	}
}

// failOnce answers err from the first call of name the fault selects and
// passes every other call through.
type failOnce struct {
	err  error
	done bool
	mu   sync.Mutex
}

func (f *failOnce) take() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return false
	}
	f.done = true
	return true
}

// leftThenRecovered runs sc's write under inject, which must leave it for
// recovery, then runs a recovery attempt and checks it landed.
func leftThenRecovered(t *testing.T, sc *scenario, inject func(*mediawrite.Faults), want error) {
	t.Helper()
	l := newLib(t, "E01.mkv")
	if sc.setup != nil {
		sc.setup(l)
	}
	l.start(func(f *mediawrite.Faults) {
		if sc.inject != nil {
			sc.inject(f)
		}
		inject(f)
	})
	err := sc.act(l)
	if err == nil || !errors.Is(err, want) || !strings.Contains(err.Error(), "left for recovery") {
		t.Fatalf("write = %v, want %v left for recovery", err, want)
	}
	if n := pendingCount(t, l.store); n != 1 {
		t.Fatalf("%d intents after the fault, want 1", n)
	}
	if !mediawrite.RecoverNow(t.Context(), l.w, l.root) {
		t.Fatal("a recovery attempt is already running")
	}
	l.consistent()
	sc.check(l, true)
}

func scenarioNamed(t *testing.T, name string) *scenario {
	t.Helper()
	for _, sc := range scenarios() {
		if sc.name == name {
			return &sc
		}
	}
	t.Fatalf("no scenario %q", name)
	return nil
}

func TestFailedEndCommit(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"deliver", "retime", "delete"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fault := &failOnce{err: errCommit}
			leftThenRecovered(t, scenarioNamed(t, name), func(f *mediawrite.Faults) {
				end := f.End
				f.End = func(ctx context.Context, op statedb.Op, inst statedb.InstanceID, name string, landed bool) error {
					if landed && fault.take() {
						return fault.err
					}
					return end(ctx, op, inst, name, landed)
				}
			}, errCommit)
		})
	}
}

func TestEIOAfterEffect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		inject   func(f *mediawrite.Faults, fault *failOnce)
		name, sc string
	}{
		{name: "rename", sc: "deliver", inject: func(f *mediawrite.Faults, fault *failOnce) {
			rename := f.RenameNoReplace
			f.RenameNoReplace = func(dir *os.File, from, to string) error {
				if err := rename(dir, from, to); err != nil || !fault.take() {
					return err
				}
				return &os.LinkError{Op: "renameat2", Old: from, New: to, Err: syscall.EIO}
			}
		}},
		{name: "link", sc: "deliver by hard link", inject: func(f *mediawrite.Faults, fault *failOnce) {
			link := f.Link
			f.Link = func(dir *os.Root, from, to string) error {
				if err := link(dir, from, to); err != nil || !fault.take() {
					return err
				}
				return &os.LinkError{Op: "linkat", Old: from, New: to, Err: syscall.EIO}
			}
		}},
		{name: "rename over", sc: "retime", inject: func(f *mediawrite.Faults, fault *failOnce) {
			rename := f.Rename
			f.Rename = func(dir *os.Root, from, to string) error {
				if err := rename(dir, from, to); err != nil || !fault.take() {
					return err
				}
				return &os.LinkError{Op: "renameat", Old: from, New: to, Err: syscall.EIO}
			}
		}},
		{name: "removal", sc: "delete", inject: func(f *mediawrite.Faults, fault *failOnce) {
			remove := f.Remove
			f.Remove = func(dir *os.Root, name string) error {
				if err := remove(dir, name); err != nil || !fault.take() {
					return err
				}
				return &fs.PathError{Op: "unlinkat", Path: name, Err: syscall.EIO}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fault := &failOnce{}
			leftThenRecovered(t, scenarioNamed(t, c.sc), func(f *mediawrite.Faults) { c.inject(f, fault) }, syscall.EIO)
		})
	}
}

func TestOneShotStageRemovalFault(t *testing.T) {
	t.Parallel()
	failStageRemoval := func(f *mediawrite.Faults, fault *failOnce) {
		remove := f.Remove
		f.Remove = func(dir *os.Root, name string) error {
			if strings.HasPrefix(name, ".sfx-") && fault.take() {
				return &fs.PathError{Op: "unlinkat", Path: name, Err: syscall.EIO}
			}
			return remove(dir, name)
		}
	}
	t.Run("the linked stage", func(t *testing.T) {
		t.Parallel()
		fault := &failOnce{}
		leftThenRecovered(t, scenarioNamed(t, "deliver by hard link"), func(f *mediawrite.Faults) { failStageRemoval(f, fault) }, syscall.EIO)
	})
	t.Run("the stage of a contended publish", func(t *testing.T) {
		t.Parallel()
		l := newLib(t, "E01.mkv")
		fault := &failOnce{}
		l.start(func(f *mediawrite.Faults) {
			failStageRemoval(f, fault)
			rename := f.RenameNoReplace
			f.RenameNoReplace = func(dir *os.File, from, to string) error {
				writeFile(t, l.path(to), srtOf("outside"))
				return rename(dir, from, to)
			}
		})
		_, err := l.w.Deliver(t.Context(), delivery(t, l.insts[0], "new"))
		if !errors.Is(err, syscall.EIO) || pendingCount(t, l.store) != 1 || len(l.stages()) != 1 {
			t.Fatalf("Deliver = %v with %d intents and stages %q, want EIO, the intent and the stage left", err, pendingCount(t, l.store), l.stages())
		}
		if !mediawrite.RecoverNow(t.Context(), l.w, l.root) {
			t.Fatal("a recovery attempt is already running")
		}
		l.consistent()
		if got := readFile(t, l.path(ep1)); got != srtOf("outside") {
			t.Errorf("%s holds %q, want the outside writer's file", ep1, got)
		}
		if rows := l.attempts(l.keys[0], newCandidate); len(rows) != 1 || rows[0].Delivered {
			t.Errorf("attempt rows %+v, want one undelivered", rows)
		}
	})
}
