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
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subtitlefile"
	"github.com/cplieger/subflux/internal/timing"
)

// library is one root b holding Show/S01E01.mkv, seeded into a store
// configuring policy, with a running writer.
type library struct {
	store *statedb.Store
	w     *mediawrite.Writer
	root  string
	dir   string // the video directory
	key   media.Key
	inst  statedb.InstanceID
}

const enOnly = "default:\n  - tag: en\n"

func newLibrary(t *testing.T, policy string, d mediawrite.Deps, inject func(*mediawrite.Faults)) *library {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "b")
	video := filepath.Join(root, "Show", "S01E01.mkv")
	writeFile(t, video, "video")
	store := openStoreAt(t, filepath.Join(tmp, "subflux.bolt"), policy)
	ids, keys := seedKeys(t, store, video)
	d.Store, d.Roots = store, rootsOf(root)
	w := newWriter(t, d, inject)
	run(t, w)
	return &library{store: store, w: w, root: root, dir: filepath.Dir(video), key: keys[0], inst: ids[0]}
}

func (l *library) path(name string) string { return filepath.Join(l.dir, name) }

// managed lands content at name as a record of the library's instance and
// returns its supersede.
func (l *library) managed(t *testing.T, name, content string) mediawrite.Superseded {
	t.Helper()
	return l.managedTag(t, name, enTag, content)
}

func (l *library) managedTag(t *testing.T, name string, tag language.Tag, content string) mediawrite.Superseded {
	t.Helper()
	rec := landRecordAs(t, l.store, l.inst, l.key, l.path(name), content, tag)
	return mediawrite.Superseded{File: mediawrite.RefOf(rec), Expect: mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}}
}

// subtitleName is the unnumbered SRT name of tag beside the video stem.
func subtitleName(stem string, tag language.Tag) (string, error) {
	return subtitlefile.Build(stem, subtitlefile.Name{Tag: tag, Ext: subtitlefile.ExtSRT})
}

func TestDeliverPublishesRecordAndFile(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	before := time.Now().Truncate(time.Second)
	rec, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "S01E01.en.srt" || rec.Generation != 1 || rec.Ordinal != 0 {
		t.Errorf("record = %q generation %d ordinal %d, want S01E01.en.srt at 1, unnumbered", rec.Name, rec.Generation, rec.Ordinal)
	}
	if got := readFile(t, l.path(rec.Name)); got != srtOf("hello") {
		t.Errorf("published %q, want the delivered document", got)
	}
	info, err := os.Stat(l.path(rec.Name))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Identity.Size != info.Size() || rec.Identity.MtimeNs != info.ModTime().UnixNano() || rec.Identity.SHA256 != sha256.Sum256([]byte(srtOf("hello"))) {
		t.Errorf("identity %+v does not describe the file (size %d, mtime %d)", rec.Identity, info.Size(), info.ModTime().UnixNano())
	}
	if mt := info.ModTime(); mt.Nanosecond() != 0 || mt.Before(before) {
		t.Errorf("modification time %v, want now to the second", mt)
	}
	if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.en.srt", "S01E01.mkv"}) {
		t.Errorf("directory holds %v, want the video and the subtitle alone", got)
	}
	if n := pendingCount(t, l.store); n != 0 {
		t.Errorf("%d intents pending after a landed delivery", n)
	}
}

func TestStageTimesBeforeFsync(t *testing.T) {
	t.Parallel()
	var calls []string
	l := newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
		write, futimens, fsync, closeF, fsyncDir := f.Write, f.Futimens, f.Fsync, f.Close, f.FsyncDir
		f.Write = func(file *os.File, b []byte) error { calls = append(calls, "write"); return write(file, b) }
		f.Futimens = func(file *os.File, t time.Time) error {
			calls = append(calls, "futimens("+filepath.Base(file.Name())+")")
			return futimens(file, t)
		}
		f.Fsync = func(file *os.File) error {
			calls = append(calls, "fsync("+filepath.Base(file.Name())+")")
			return fsync(file)
		}
		f.Close = func(file *os.File) error { calls = append(calls, "close"); return closeF(file) }
		f.FsyncDir = func(dir *os.File) error { calls = append(calls, "fsync(dir)"); return fsyncDir(dir) }
	})
	d := delivery(t, l.inst, "hello")
	stage := mediawrite.StageName(l.inst, "S01E01.en.srt", sha256.Sum256([]byte(srtOf("hello"))))
	if _, err := l.w.Deliver(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	want := []string{"write", "futimens(" + stage + ")", "fsync(" + stage + ")", "close", "fsync(dir)"}
	if len(calls) < len(want) || !slices.Equal(calls[:len(want)], want) {
		t.Errorf("stage calls = %v, want %v first", calls, want)
	}
	if len(stage) != 41 {
		t.Errorf("stage name %q is %d bytes, want 41", stage, len(stage))
	}
}

func TestDroppedSupersedeFreesNoName(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	old := l.managed(t, "S01E01.en.srt", srtOf("old"))
	writeFile(t, l.path("S01E01.en.srt"), srtOf("edited by the user"))

	d := delivery(t, l.inst, "new")
	d.Supersedes = []mediawrite.Superseded{old}
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatalf("Deliver over a changed supersede: %v, want a numbered sibling", err)
	}
	if rec.Name != "S01E01.en.1.srt" {
		t.Errorf("delivered to %q, want the numbered sibling S01E01.en.1.srt", rec.Name)
	}
	if got := readFile(t, l.path("S01E01.en.srt")); got != srtOf("edited by the user") {
		t.Errorf("the user's edit now reads %q; it must stay byte-identical", got)
	}
}

func TestLeftoverStageReplacedOnDeliver(t *testing.T) {
	t.Parallel()
	t.Run("removed and the creation retried", func(t *testing.T) {
		t.Parallel()
		logs := &logRecords{}
		l := newLibrary(t, enOnly, mediawrite.Deps{Logger: slog.New(logs)}, nil)
		stage := mediawrite.StageName(l.inst, "S01E01.en.srt", sha256.Sum256([]byte(srtOf("hello"))))
		writeFile(t, l.path(stage), "1\n00:00:01,000 --> 00:00")

		rec, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello"))
		if err != nil {
			t.Fatalf("Deliver beside a leftover stage: %v", err)
		}
		if got := readFile(t, l.path(rec.Name)); got != srtOf("hello") {
			t.Errorf("published %q, want the delivered document", got)
		}
		if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.en.srt", "S01E01.mkv"}) {
			t.Errorf("directory holds %v, want the leftover gone", got)
		}
		if warns := logs.messages(slog.LevelWarn); len(warns) != 1 || !strings.Contains(warns[0], "leftover stage") {
			t.Errorf("WARN logs = %q, want one naming the leftover stage", warns)
		}
		if rows := history(t, l.store, l.key); len(rows) != 1 || !rows[0].Delivered {
			t.Errorf("history = %+v, want the one attempt delivered", rows)
		}
	})
	t.Run("a second EEXIST settles not landed", func(t *testing.T) {
		t.Parallel()
		l := newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
			f.CreateStage = func(_ *os.Root, name string) (*os.File, error) {
				return nil, &fs.PathError{Op: "openat", Path: name, Err: syscall.EEXIST}
			}
		})
		if _, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello")); !errors.Is(err, fs.ErrExist) {
			t.Errorf("Deliver whose stage name stays taken = %v, want the EEXIST refusal", err)
		}
		if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.mkv"}) {
			t.Errorf("directory holds %v, want nothing published", got)
		}
		if n := pendingCount(t, l.store); n != 0 {
			t.Errorf("%d intents pending; the delivery must settle not landed", n)
		}
		if rows := history(t, l.store, l.key); len(rows) != 1 || rows[0].Delivered {
			t.Errorf("history = %+v, want the one attempt undelivered", rows)
		}
	})
}

func TestGrownSupersedeLeftAlone(t *testing.T) {
	t.Parallel()
	grow := func() {}
	l := newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
		rename := f.RenameNoReplace
		f.RenameNoReplace = func(dir *os.File, from, to string) error {
			err := rename(dir, from, to)
			if err == nil {
				grow()
			}
			return err
		}
	})
	sup := l.managed(t, "S01E01.en.1.srt", srtOf("old"))
	big := strings.Repeat("x", 16<<20+1)
	grow = func() { writeFile(t, l.path("S01E01.en.1.srt"), big) }
	d := delivery(t, l.inst, "new")
	d.Supersedes = []mediawrite.Superseded{sup}

	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatalf("Deliver whose supersede grew past 16 MiB after the publish: %v", err)
	}
	if rec.Name != "S01E01.en.srt" {
		t.Errorf("landed %q, want S01E01.en.srt", rec.Name)
	}
	if got := readFile(t, l.path("S01E01.en.1.srt")); len(got) != len(big) {
		t.Errorf("the grown supersede holds %d bytes, want it left alone", len(got))
	}
	if n := pendingCount(t, l.store); n != 0 {
		t.Errorf("%d intents pending, want the delivery settled", n)
	}
}

// writeLogLevels are the levels of the writer's "a write ..." records,
// which name how a write ended.
func writeLogLevels(logs *logRecords) []slog.Level {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	var out []slog.Level
	for _, r := range logs.recs {
		if strings.HasPrefix(r.Message, "mediawrite: a write ") {
			out = append(out, r.Level)
		}
	}
	return out
}

func TestFailureLogLevels(t *testing.T) {
	t.Parallel()
	deliver := func(t *testing.T, l *library) error {
		_, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello"))
		return err
	}
	managed := func(t *testing.T, l *library) (mediawrite.FileRef, mediawrite.Expectation) {
		sup := l.managed(t, "S01E01.en.srt", srtOf("ours"))
		return sup.File, sup.Expect
	}
	// pendOnce stores a Delete intent for name of the library's instance
	// the first time it reports true, so the next Begin* refuses.
	pendOnce := func(l **library, name string) func() {
		var done atomic.Bool
		return func() {
			if *l != nil && done.CompareAndSwap(false, true) {
				if err := (*l).store.BeginDelete(context.Background(), (*l).inst, name, [32]byte{}); err != nil {
					panic(err)
				}
			}
		}
	}
	eio := func(name string) error { return &fs.PathError{Op: "openat", Path: name, Err: syscall.EIO} }
	cases := []struct {
		inject func(l **library, armed *atomic.Bool) func(*mediawrite.Faults)
		act    func(t *testing.T, l *library) error
		name   string
		want   slog.Level
	}{
		{name: "invalid delivery", want: slog.LevelError, act: func(t *testing.T, l *library) error {
			d := delivery(t, l.inst, "hello")
			d.Tag = language.Tag{}
			_, err := l.w.Deliver(t.Context(), d)
			return err
		}},
		{name: "Deliver Begin store failure", want: slog.LevelError, act: deliver, inject: func(l **library, armed *atomic.Bool) func(*mediawrite.Faults) {
			pend := pendOnce(l, "S01E01.en.srt")
			return func(f *mediawrite.Faults) {
				lstat := f.Lstat
				f.Lstat = func(dir *os.Root, name string) (fs.FileInfo, error) {
					if armed.Load() {
						pend()
					}
					return lstat(dir, name)
				}
			}
		}},
		{name: "Deliver path error before the intent", want: slog.LevelWarn, act: deliver, inject: func(_ **library, armed *atomic.Bool) func(*mediawrite.Faults) {
			return func(f *mediawrite.Faults) {
				openDir := f.OpenDir
				f.OpenDir = func(parent *os.Root, name string) (*os.Root, error) {
					if armed.Load() {
						return nil, eio(name)
					}
					return openDir(parent, name)
				}
			}
		}},
		{name: "every numbered name taken", want: slog.LevelInfo, act: deliver, inject: func(_ **library, armed *atomic.Bool) func(*mediawrite.Faults) {
			return func(f *mediawrite.Faults) {
				lstat := f.Lstat
				f.Lstat = func(dir *os.Root, name string) (fs.FileInfo, error) {
					if armed.Load() && strings.HasSuffix(name, ".srt") {
						return dir.Stat(".")
					}
					return lstat(dir, name)
				}
			}
		}},
		{name: "publish contended", want: slog.LevelInfo, act: deliver, inject: func(_ **library, _ *atomic.Bool) func(*mediawrite.Faults) {
			return func(f *mediawrite.Faults) {
				f.RenameNoReplace = func(_ *os.File, from, to string) error {
					return &os.LinkError{Op: "renameat2", Old: from, New: to, Err: syscall.EEXIST}
				}
			}
		}},
		{name: "stage write failure", want: slog.LevelWarn, act: deliver, inject: func(_ **library, _ *atomic.Bool) func(*mediawrite.Faults) {
			return func(f *mediawrite.Faults) {
				f.Write = func(*os.File, []byte) error { return syscall.EIO }
			}
		}},
		{name: "End store failure", want: slog.LevelError, act: deliver, inject: func(_ **library, _ *atomic.Bool) func(*mediawrite.Faults) {
			return func(f *mediawrite.Faults) {
				end := f.End
				f.End = func(ctx context.Context, op statedb.Op, inst statedb.InstanceID, name string, landed bool) error {
					if landed {
						return errCommit
					}
					return end(ctx, op, inst, name, landed)
				}
			}
		}},
		{name: "publish of unknown outcome", want: slog.LevelWarn, act: deliver, inject: func(_ **library, _ *atomic.Bool) func(*mediawrite.Faults) {
			return func(f *mediawrite.Faults) {
				rename := f.RenameNoReplace
				f.RenameNoReplace = func(dir *os.File, from, to string) error {
					if err := rename(dir, from, to); err != nil {
						return err
					}
					return &os.LinkError{Op: "renameat2", Old: from, New: to, Err: syscall.EIO}
				}
			}
		}},
		{name: "Delete Begin store failure", want: slog.LevelError, act: func(t *testing.T, l *library) error {
			ref, expect := managed(t, l)
			return l.w.Delete(t.Context(), ref, expect)
		}, inject: func(l **library, armed *atomic.Bool) func(*mediawrite.Faults) {
			pend := pendOnce(l, "S01E01.en.srt")
			return func(f *mediawrite.Faults) {
				openStaged := f.Open
				f.Open = func(dir *os.Root, name string) (*os.File, error) {
					if armed.Load() {
						pend()
					}
					return openStaged(dir, name)
				}
			}
		}},
		{name: "Retime path error before the intent", want: slog.LevelWarn, act: func(t *testing.T, l *library) error {
			ref, expect := managed(t, l)
			_, err := l.w.Retime(t.Context(), ref, expect, timing.Shift{Ms: 1000}, nil)
			return err
		}, inject: func(_ **library, armed *atomic.Bool) func(*mediawrite.Faults) {
			return func(f *mediawrite.Faults) {
				openRead := f.OpenRead
				f.OpenRead = func(dir *os.Root, name string) (*os.File, fs.FileInfo, error) {
					if armed.Load() {
						return nil, nil, eio(name)
					}
					return openRead(dir, name)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			logs := &logRecords{}
			var l *library
			var armed atomic.Bool
			var inject func(*mediawrite.Faults)
			if c.inject != nil {
				inject = c.inject(&l, &armed)
			}
			l = newLibrary(t, enOnly, mediawrite.Deps{Logger: slog.New(logs)}, inject)
			armed.Store(true)
			if err := c.act(t, l); err == nil {
				t.Fatal("the write succeeded, want its failure")
			}
			if got := writeLogLevels(logs); !slices.Equal(got, []slog.Level{c.want}) {
				t.Errorf("write log levels = %v, want [%v]", got, c.want)
			}
		})
	}
}

func TestNameContendedAfterEightTries(t *testing.T) {
	t.Parallel()
	var numbered atomic.Int32
	l := newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
		lstat := f.Lstat
		f.Lstat = func(dir *os.Root, name string) (fs.FileInfo, error) {
			if strings.HasPrefix(name, "S01E01.en.") && name != "S01E01.en.srt" {
				numbered.Add(1)
				return dir.Stat(".")
			}
			return lstat(dir, name)
		}
	})
	d := delivery(t, l.inst, "hello")
	d.Numbered = true
	if _, err := l.w.Deliver(t.Context(), d); !errors.Is(err, mediawrite.ErrNameContended) {
		t.Fatalf("Deliver with every numbered name taken = %v, want ErrNameContended", err)
	}
	if got := numbered.Load(); got != 8 {
		t.Errorf("numbered names tried = %d, want 8", got)
	}
	if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.mkv"}) {
		t.Errorf("directory holds %v, want nothing written", got)
	}
	if rows := history(t, l.store, l.key); len(rows) != 1 || rows[0].Delivered {
		t.Errorf("history = %+v, want the attempt once, undelivered", rows)
	}
}

func TestRedownloadCarriesOwnTiming(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	first := delivery(t, l.inst, "first")
	first.Timing = timing.Shift{Ms: 1500}
	rec, err := l.w.Deliver(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	second := delivery(t, l.inst, "second")
	second.Supersedes = []mediawrite.Superseded{{File: mediawrite.RefOf(rec), Expect: mediawrite.Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}}}
	again, err := l.w.Deliver(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != rec.Name || again.Generation != 2 {
		t.Errorf("re-download = %q at generation %d, want %q at 2", again.Name, again.Generation, rec.Name)
	}
	if again.Timing != (timing.Identity{}) {
		t.Errorf("re-download timing = %#v, want its own Identity, not the replaced file's shift", again.Timing)
	}
	if got := readFile(t, l.path(rec.Name)); got != srtOf("second") {
		t.Errorf("file reads %q, want the second document unshifted", got)
	}
}

func TestSupersedeSafety(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, policy, held, delivered string
		stays                         bool
	}{
		{name: "an Exact file of another target stays", policy: "default:\n  - tag: en\n  - tag: fr\n", held: "en", delivered: "fr", stays: true},
		{name: "the only Substitute of another target stays", policy: "default:\n  - tag: en-GB\n  - tag: fr\n", held: "en-US", delivered: "fr", stays: true},
		{name: "a Substitute the new file replaces goes", policy: "default:\n  - tag: en-GB\n  - tag: en-US\n", held: "en-CA", delivered: "en-GB", stays: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := newLibrary(t, c.policy, mediawrite.Deps{}, nil)
			held := mustTag(c.held)
			if c.held != "en" {
				if g := l.store.Policy().Grade(mustTag("en-GB"), held); g.Class != language.Substitute {
					t.Fatalf("premise: en-GB grades %s %v, want Substitute", c.held, g)
				}
			}
			name, err := subtitleName("S01E01", held)
			if err != nil {
				t.Fatal(err)
			}
			old := l.managedTag(t, name, held, srtOf("held"))
			d := delivery(t, l.inst, "new")
			d.Tag, d.Target = mustTag(c.delivered), mustTag(c.delivered)
			d.Supersedes = []mediawrite.Superseded{old}
			rec, err := l.w.Deliver(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(l.path(name))
			if stayed := statErr == nil; stayed != c.stays {
				t.Errorf("superseded %s on disk = %v, want %v", name, stayed, c.stays)
			}
			if stayed := slices.Contains(recordNames(t, l.store, l.inst), name); stayed != c.stays {
				t.Errorf("superseded %s's record kept = %v, want %v", name, stayed, c.stays)
			}
			if _, err := os.Stat(l.path(rec.Name)); err != nil {
				t.Errorf("the delivery did not land: %v", err)
			}
		})
	}
}

func TestSupersedeSafetyDeleteWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var hold atomic.Bool
		release := make(chan struct{})
		l := newLibrary(t, "default:\n  - tag: en\n  - tag: fr\n", mediawrite.Deps{}, func(f *mediawrite.Faults) {
			f.Step = func(step string) {
				if step == "begin" && hold.CompareAndSwap(true, false) {
					<-release
				}
			}
		})
		held := l.managed(t, "S01E01.en.srt", srtOf("held"))
		d := delivery(t, l.inst, "nouveau")
		d.Tag, d.Target = mustTag("fr"), mustTag("fr")
		d.Supersedes = []mediawrite.Superseded{held}
		hold.Store(true)
		delivered := make(chan error, 1)
		go func() { _, err := l.w.Deliver(t.Context(), d); delivered <- err }()
		synctest.Wait()
		deleted := make(chan error, 1)
		go func() { deleted <- l.w.Delete(t.Context(), held.File, held.Expect) }()
		synctest.Wait()
		select {
		case err := <-deleted:
			t.Fatalf("Delete of the kept Substitute ran while the delivery held the instance: %v", err)
		default:
		}
		close(release)
		if err := <-delivered; err != nil {
			t.Fatal(err)
		}
		if err := <-deleted; err != nil {
			t.Fatalf("Delete after the delivery = %v", err)
		}
		if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.fr.srt", "S01E01.mkv"}) {
			t.Errorf("directory holds %v, want the delivery and the video", got)
		}
	})
}

func TestOrdinalAfterExternalSeven(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	writeFile(t, l.path("S01E01.en.7.srt"), srtOf("someone else's"))
	d := delivery(t, l.inst, "hello")
	d.Numbered = true
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "S01E01.en.8.srt" || rec.Ordinal != 8 {
		t.Errorf("numbered delivery = %q ordinal %d, want S01E01.en.8.srt, 8", rec.Name, rec.Ordinal)
	}
	if got := readFile(t, l.path("S01E01.en.7.srt")); got != srtOf("someone else's") {
		t.Errorf("the external file now reads %q", got)
	}
}

func TestOrdinalNeverReused(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "b")
	video := filepath.Join(root, "Show", "S01E01.mkv")
	writeFile(t, video, "video")
	dbPath := filepath.Join(tmp, "subflux.bolt")
	store := openStoreAt(t, dbPath, enOnly)
	inst := seed(t, store, video)[0]
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(root)}, nil)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	if err := mediawrite.Settled(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	d := delivery(t, inst, "hello")
	d.Numbered = true
	rec, err := w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(video), rec.Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.List(t.Context(), inst, mediawrite.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openStoreAt(t, dbPath, enOnly)
	w2 := newWriter(t, mediawrite.Deps{Store: reopened, Roots: rootsOf(root)}, nil)
	run(t, w2)
	again, err := w2.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Ordinal != 1 || again.Ordinal != 2 {
		t.Errorf("ordinals = %d then %d across a restart, want 1 then 2", rec.Ordinal, again.Ordinal)
	}
}

func TestAttemptsRecordedOnce(t *testing.T) {
	t.Parallel()
	rejected := func(l *library) []statedb.Download {
		return []statedb.Download{{
			At: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Target: enTag, Variant: language.Standard, Items: []media.Key{l.key},
			Inst: l.inst, Run: statedb.RunID{1}, Attempt: 2, Refusal: "different_cut", Candidate: [32]byte{8},
		}}
	}
	t.Run("landed", func(t *testing.T) {
		t.Parallel()
		l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
		d := delivery(t, l.inst, "hello")
		d.Rejected = rejected(l)
		if _, err := l.w.Deliver(t.Context(), d); err != nil {
			t.Fatal(err)
		}
		rows := history(t, l.store, l.key)
		delivered := slices.IndexFunc(rows, func(r statedb.Download) bool { return r.Delivered })
		if len(rows) != 2 || delivered < 0 || rows[delivered].Candidate != d.Candidate || rows[delivered].Attempt != 1 {
			t.Errorf("history = %+v, want the delivered attempt (candidate %x) and the rejected one", rows, d.Candidate[:1])
		}
	})
	t.Run("failed before the intent", func(t *testing.T) {
		t.Parallel()
		l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
		d := delivery(t, l.inst, "hello")
		d.Rejected = rejected(l)
		d.Tag = language.Tag{}
		if _, err := l.w.Deliver(t.Context(), d); err == nil {
			t.Fatal("a delivery with no tag succeeded")
		}
		rows := history(t, l.store, l.key)
		if len(rows) != 2 || slices.ContainsFunc(rows, func(r statedb.Download) bool { return r.Delivered }) {
			t.Errorf("history = %+v, want both attempts once, undelivered", rows)
		}
	})
	t.Run("failed after the intent", func(t *testing.T) {
		t.Parallel()
		l := newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
			f.Write = func(*os.File, []byte) error { return errors.New("disk full") }
		})
		d := delivery(t, l.inst, "hello")
		d.Rejected = rejected(l)
		if _, err := l.w.Deliver(t.Context(), d); err == nil {
			t.Fatal("a delivery whose stage write failed succeeded")
		}
		rows := history(t, l.store, l.key)
		if len(rows) != 2 || slices.ContainsFunc(rows, func(r statedb.Download) bool { return r.Delivered }) {
			t.Errorf("history = %+v, want both attempts once, undelivered", rows)
		}
		if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.mkv"}) {
			t.Errorf("directory holds %v, want the stage removed", got)
		}
	})
	t.Run("no run records nothing", func(t *testing.T) {
		t.Parallel()
		l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
		d := delivery(t, l.inst, "hello")
		d.Run = statedb.RunID{}
		if _, err := l.w.Deliver(t.Context(), d); err == nil {
			t.Fatal("a delivery with no run succeeded")
		}
		if rows := history(t, l.store, l.key); len(rows) != 0 {
			t.Errorf("history = %+v, want nothing for an attempt with no run", rows)
		}
	})
}

func TestRemovesNeverNameChosen(t *testing.T) {
	t.Parallel()
	var staged statedb.PendingDeliver
	var l *library
	l = newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
		f.Step = func(step string) {
			if step != "begin" {
				return
			}
			d, ok, err := l.store.PendingDeliverOf(t.Context(), l.inst, "S01E01.en.srt")
			if err != nil || !ok {
				t.Errorf("PendingDeliverOf at begin = %v, %v", ok, err)
			}
			staged = d
		}
	})
	plain := l.managed(t, "S01E01.en.srt", srtOf("old plain"))
	numbered := l.managed(t, "S01E01.en.1.srt", srtOf("old numbered"))
	d := delivery(t, l.inst, "new")
	d.Supersedes = []mediawrite.Superseded{numbered, plain}
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "S01E01.en.srt" {
		t.Fatalf("delivered to %q, want the plain name its supersede held", rec.Name)
	}
	if len(staged.Removes) != 1 || staged.Removes[0].Name != "S01E01.en.1.srt" {
		t.Errorf("staged removals = %+v, want only S01E01.en.1.srt, never the chosen name", staged.Removes)
	}
	if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.en.srt", "S01E01.mkv"}) {
		t.Errorf("directory holds %v, want the new plain file and the video", got)
	}
	if got := recordNames(t, l.store, l.inst); !slices.Equal(got, []string{"S01E01.en.srt"}) {
		t.Errorf("records = %v, want the new plain file's alone", got)
	}
}

func TestOversizeRefusedBeforeIntent(t *testing.T) {
	t.Parallel()
	const capBytes = 16 << 20
	cases := []struct {
		build func() (doc string, tag language.Tag)
		name  string
		cfg   mediawrite.Config
		tm    timing.Timing
	}{
		{name: "expanded by normalize_utf8", cfg: mediawrite.Config{NormalizeUTF8: true}, tm: timing.Identity{}, build: func() (string, language.Tag) {
			// windows-1252 French: each é is one byte that UTF-8 spells in two.
			line := strings.ReplaceAll(strings.Repeat("Le café était fermé l'été dernier. ", 28), "é", "\xe9")
			return padded(capBytes-100, "00:00:01,000 --> 00:00:02,000", line), mustTag("fr")
		}},
		{name: "longer timestamps", tm: timing.Shift{Ms: 6 * 3_600_000}, build: func() (string, language.Tag) {
			return padded(capBytes-100, "95:00:01,000 --> 95:00:02,000", strings.Repeat("x", 999)), enTag
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := newLibrary(t, enOnly, mediawrite.Deps{Config: c.cfg}, nil)
			doc, tag := c.build()
			cues, err := timing.ParseCues([]byte(doc))
			if err != nil {
				t.Fatal(err)
			}
			d := delivery(t, l.inst, "")
			d.Cues, d.Tag, d.Target, d.Timing = cues, tag, tag, c.tm
			if _, err := l.w.Deliver(t.Context(), d); !errors.Is(err, timing.ErrFormatUnsupported) {
				t.Fatalf("Deliver of a %d-byte document past the cap = %v, want ErrFormatUnsupported", len(doc), err)
			}
			if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.mkv"}) {
				t.Errorf("directory holds %v, want nothing written", got)
			}
			rows := history(t, l.store, l.key)
			if len(rows) != 1 || rows[0].Delivered || rows[0].Refusal != "format_unsupported" {
				t.Errorf("history = %+v, want one undelivered format_unsupported attempt", rows)
			}

			d.Cues, d.Timing = cues, timing.Identity{}
			l2 := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
			d.Inst = l2.inst
			if _, err := l2.w.Deliver(t.Context(), d); err != nil {
				t.Errorf("the same document, neither cleaned nor shifted, is under the cap and must deliver: %v", err)
			}
		})
	}
}

// padded is an SRT document of exactly size bytes: cues stamped stamp,
// each holding line, the last filled with line's first byte to fit.
func padded(size int, stamp, line string) string {
	var b strings.Builder
	for i := 1; ; i++ {
		head := fmt.Sprintf("%d\n%s\n", i, stamp)
		if b.Len()+2*(len(head)+len(line)+2) > size {
			b.WriteString(head)
			b.WriteString(strings.Repeat(line[:1], size-b.Len()-2))
			b.WriteString("\n\n")
			return b.String()
		}
		b.WriteString(head)
		b.WriteString(line)
		b.WriteString("\n\n")
	}
}
