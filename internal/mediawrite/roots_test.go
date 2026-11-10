package mediawrite_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
)

func TestMissingRootDoesNotShiftOthers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeFile(t, filepath.Join(b, "Show", "S01E01.mkv"), "video in b")
	store := openStore(t)
	inst := seed(t, store, filepath.Join(b, "Show", "S01E01.mkv"))[0]
	alerts := &fakeAlerts{}
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(a, b), Alerts: alerts}, nil)
	run(t, w)

	f, err := w.OpenVideo(t.Context(), inst)
	if err != nil {
		t.Fatalf("OpenVideo under b: %v", err)
	}
	defer f.Close()
	if got := readAll(t, f); got != "video in b" {
		t.Errorf("OpenVideo read %q, want the video in b", got)
	}
	if text, ok := alerts.text(status.MediaRootAlert(a)); !ok || !strings.Contains(text, a) {
		t.Errorf("alert for missing a = %q, %v; want active, naming %s", text, ok, a)
	}
	if _, ok := alerts.text(status.MediaRootAlert(b)); ok {
		t.Error("b, which is mounted, raised an alert")
	}
}

func TestWriteErrnoSchedulesProbe(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	video := filepath.Join(b, "Show", "S01E01.mkv")
	writeFile(t, video, "video")
	store := openStore(t)
	inst := seed(t, store, video)[0]
	alerts := &fakeAlerts{}
	var probes atomic.Int32
	var readOnly atomic.Bool
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b), Alerts: alerts}, func(f *mediawrite.Faults) {
		write := f.ProbeWrite
		f.ProbeWrite = func(ctx context.Context, root *os.Root) error {
			probes.Add(1)
			if readOnly.Load() {
				return &fs.PathError{Op: "open", Path: ".probe", Err: syscall.EROFS}
			}
			return write(ctx, root)
		}
		f.CreateStage = func(_ *os.Root, name string) (*os.File, error) {
			if readOnly.Load() {
				return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EROFS}
			}
			return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
		}
	})
	run(t, w)
	if got := probes.Load(); got != 1 {
		t.Fatalf("write probes after boot = %d, want 1", got)
	}

	_, err := w.Deliver(t.Context(), delivery(t, inst, "hello"))
	if !errors.Is(err, syscall.EACCES) || errors.Is(err, mediawrite.ErrRootUnavailable) {
		t.Errorf("EACCES under a writable root = %v, want the path error alone", err)
	}
	if got := probes.Load(); got != 2 {
		t.Errorf("write probes after EACCES = %d, want 2", got)
	}
	if n := alerts.activeCount(); n != 0 {
		t.Errorf("%d alerts active after a path error, want none", n)
	}

	readOnly.Store(true)
	if _, err := w.Deliver(t.Context(), delivery(t, inst, "hello")); !errors.Is(err, mediawrite.ErrRootUnavailable) {
		t.Errorf("EROFS with a failing write probe = %v, want ErrRootUnavailable", err)
	}
	if _, ok := alerts.text(status.MediaRootAlert(b)); !ok {
		t.Error("a read-only root raised no alert")
	}
	if _, err := w.OpenVideo(t.Context(), inst); !errors.Is(err, mediawrite.ErrRootUnavailable) {
		t.Errorf("next call on the read-only root = %v, want ErrRootUnavailable", err)
	}
}

func TestPathErrorRaisesNothing(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	badName := filepath.Join(b, "Show", "S01E01.mkv")
	inUnreadable := filepath.Join(b, "Locked", "S01E02.mkv")
	fine := filepath.Join(b, "Show", "S01E03.mkv")
	for _, v := range []string{badName, inUnreadable, fine} {
		writeFile(t, v, "video")
	}
	store := openStore(t)
	insts := seed(t, store, badName, inUnreadable, fine)
	alerts := &fakeAlerts{}
	var probes atomic.Int32
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b), Alerts: alerts}, func(f *mediawrite.Faults) {
		open, openDir, write := f.Open, f.OpenDir, f.ProbeWrite
		f.Open = func(dir *os.Root, name string) (*os.File, error) {
			if name == "S01E01.mkv" {
				return nil, &fs.PathError{Op: "openat", Path: name, Err: syscall.EILSEQ}
			}
			return open(dir, name)
		}
		f.OpenDir = func(parent *os.Root, name string) (*os.Root, error) {
			if name == "Locked" {
				return nil, &fs.PathError{Op: "openat", Path: name, Err: syscall.EACCES}
			}
			return openDir(parent, name)
		}
		f.ProbeWrite = func(ctx context.Context, root *os.Root) error {
			probes.Add(1)
			return write(ctx, root)
		}
	})
	run(t, w)

	for _, tc := range []struct {
		want error
		name string
		i    int
	}{
		{name: "a name the filesystem refuses", want: syscall.EILSEQ, i: 0},
		{name: "an unreadable subfolder", want: syscall.EACCES, i: 1},
	} {
		_, err := w.OpenVideo(t.Context(), insts[tc.i])
		if !errors.Is(err, tc.want) || errors.Is(err, mediawrite.ErrRootUnavailable) {
			t.Errorf("%s: OpenVideo = %v, want the path error alone", tc.name, err)
		}
	}
	f, err := w.OpenVideo(t.Context(), insts[2])
	if err != nil {
		t.Fatalf("a sibling video beside the failing ones: %v", err)
	}
	_ = f.Close()
	if n := alerts.activeCount(); n != 0 {
		t.Errorf("%d alerts active after path errors, want none", n)
	}
	if !mediawrite.Available(w, b) {
		t.Error("path errors made the root unavailable")
	}
	if got := probes.Load(); got != 1 {
		t.Errorf("write probes = %d, want only the boot probe", got)
	}
}

// hangingStat blocks every stat of path while hang is set, until release
// closes, counting each call.
type hangingStat struct {
	release chan struct{}
	path    string
	hang    atomic.Bool
	calls   atomic.Int32
}

func (h *hangingStat) inject(f *mediawrite.Faults) {
	stat := f.Stat
	f.Stat = func(p string) (fs.FileInfo, error) {
		if p == h.path {
			h.calls.Add(1)
			if h.hang.Load() {
				<-h.release
			}
		}
		return stat(p)
	}
}

func TestHungProbeOneOutstanding(t *testing.T) {
	t.Parallel()
	a := filepath.Join(t.TempDir(), "a")
	writeFile(t, filepath.Join(a, "Show", "S01E01.mkv"), "video")
	store := openStore(t)
	synctest.Test(t, func(t *testing.T) {
		h := &hangingStat{path: a, release: make(chan struct{})}
		h.hang.Store(true)
		alerts := &fakeAlerts{}
		w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(a), Alerts: alerts}, h.inject)
		run(t, w)
		if mediawrite.Available(w, a) {
			t.Fatal("a root whose probe never answered is available")
		}
		if _, ok := alerts.text(status.MediaRootAlert(a)); !ok {
			t.Error("the overdue root raised no alert")
		}
		for range 5 {
			time.Sleep(mediawrite.ProbeInterval)
			synctest.Wait()
		}
		if got := h.calls.Load(); got != 1 {
			t.Errorf("probes outstanding across five intervals = %d, want 1", got)
		}

		h.hang.Store(false)
		close(h.release)
		synctest.Wait()
		time.Sleep(mediawrite.ProbeInterval)
		synctest.Wait()
		if got := h.calls.Load(); got != 2 {
			t.Errorf("probes after the hung one returned = %d, want 2", got)
		}
		if !mediawrite.Available(w, a) {
			t.Error("the root did not return once its probe answered")
		}
		if _, ok := alerts.text(status.MediaRootAlert(a)); ok {
			t.Error("the alert outlived the root's return")
		}
	})
}

func TestPeriodicProbeWritesNothing(t *testing.T) {
	t.Parallel()
	a := filepath.Join(t.TempDir(), "a")
	writeFile(t, filepath.Join(a, "keep"), "x")
	store := openStore(t)
	synctest.Test(t, func(t *testing.T) {
		var writes atomic.Int32
		w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(a)}, func(f *mediawrite.Faults) {
			write := f.ProbeWrite
			f.ProbeWrite = func(ctx context.Context, root *os.Root) error {
				writes.Add(1)
				return write(ctx, root)
			}
		})
		run(t, w)
		for range 3 {
			time.Sleep(mediawrite.ProbeInterval)
			synctest.Wait()
		}
		if got := writes.Load(); got != 1 {
			t.Errorf("write probes = %d, want the boot probe alone", got)
		}
		entries, err := os.ReadDir(a)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "keep" {
			t.Errorf("root holds %v after the probes, want only keep", entries)
		}
	})
}

func TestHandleSwapUnderBorrow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := filepath.Join(dir, "b")
	writeFile(t, filepath.Join(b, "old"), "x")
	w := newWriter(t, mediawrite.Deps{Store: openStore(t), Roots: rootsOf(b)}, nil)
	run(t, w)

	held, release, err := mediawrite.Borrow(t.Context(), w, filepath.Join(b, "x.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(b, filepath.Join(dir, "b.unmounted")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(b, "new"), "x")
	if !mediawrite.ProbeNow(t.Context(), w, b) {
		t.Fatal("the remounted root probed unavailable")
	}
	fresh, releaseFresh, err := mediawrite.Borrow(t.Context(), w, filepath.Join(b, "x.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFresh()
	if _, err := fresh.Stat("new"); err != nil {
		t.Errorf("the new handle does not see the remounted root: %v", err)
	}
	if _, err := held.Stat("old"); err != nil {
		t.Errorf("the borrowed handle closed under its borrow: %v", err)
	}
	release()
	if _, err := held.Stat("old"); err == nil {
		t.Error("the replaced handle stayed open after its last borrow returned")
	}
}

func TestLateProbeAcrossRemoveReAdd(t *testing.T) {
	t.Parallel()
	a := filepath.Join(t.TempDir(), "a")
	writeFile(t, filepath.Join(a, "keep"), "x")
	store := openStore(t)
	synctest.Test(t, func(t *testing.T) {
		h := &hangingStat{path: a, release: make(chan struct{})}
		h.hang.Store(true)
		var mu sync.Mutex
		var opened []*os.Root
		roots := rootsOf(a)
		w := newWriter(t, mediawrite.Deps{Store: store, Roots: roots}, func(f *mediawrite.Faults) {
			h.inject(f)
			openRoot := f.OpenRoot
			f.OpenRoot = func(p string) (*os.Root, error) {
				r, err := openRoot(p)
				if err == nil {
					mu.Lock()
					opened = append(opened, r)
					mu.Unlock()
				}
				return r, err
			}
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		synctest.Wait()

		time.Sleep(mediawrite.RootProbeTimeout)
		synctest.Wait()
		roots.set()
		w.RootsChanged()
		synctest.Wait()
		h.hang.Store(false)
		roots.set(a)
		w.RootsChanged()
		synctest.Wait()
		current, release, err := mediawrite.Borrow(t.Context(), w, filepath.Join(a, "x.mkv"))
		if err != nil {
			t.Fatalf("the re-added root: %v", err)
		}
		release()

		close(h.release)
		synctest.Wait()
		again, release, err := mediawrite.Borrow(t.Context(), w, filepath.Join(a, "x.mkv"))
		if err != nil {
			t.Fatalf("after the late probe: %v", err)
		}
		release()
		if again != current {
			t.Error("the late probe installed its handle over the re-added root's")
		}
		mu.Lock()
		late := slices.DeleteFunc(slices.Clone(opened), func(r *os.Root) bool { return r == current })
		mu.Unlock()
		if len(late) != 1 {
			t.Fatalf("handles opened besides the current one = %d, want the late probe's", len(late))
		}
		if _, err := late[0].Stat("keep"); err == nil {
			t.Error("the late probe's handle was left open")
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
}

func TestLongRootPathsDistinctAlerts(t *testing.T) {
	t.Parallel()
	prefix := t.TempDir()
	for len(prefix) < 600 {
		prefix = filepath.Join(prefix, strings.Repeat("p", 100))
	}
	a, b := filepath.Join(prefix, "a"), filepath.Join(prefix, "b")
	alerts := &fakeAlerts{}
	w := newWriter(t, mediawrite.Deps{Store: openStore(t), Roots: rootsOf(a, b), Alerts: alerts}, nil)
	run(t, w)
	srcA, srcB := status.MediaRootAlert(a), status.MediaRootAlert(b)
	if srcA == srcB {
		t.Fatal("the two roots share an alert source")
	}
	for _, tc := range []struct {
		src  status.AlertSource
		path string
	}{{srcA, a}, {srcB, b}} {
		if text, ok := alerts.text(tc.src); !ok || !strings.Contains(text, tc.path) {
			t.Errorf("alert %s = %q, %v; want active, naming its path", tc.src, text, ok)
		}
	}
	if n := alerts.activeCount(); n != 2 {
		t.Errorf("%d alerts active, want 2", n)
	}
}

func TestCallBeforeRunWaits(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	video := filepath.Join(b, "S01E01.mkv")
	writeFile(t, video, "video")
	store := openStore(t)
	inst := seed(t, store, video)[0]
	synctest.Test(t, func(t *testing.T) {
		w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, nil)
		got := make(chan error, 1)
		go func() {
			f, err := w.OpenVideo(t.Context(), inst)
			if err == nil {
				_ = f.Close()
			}
			got <- err
		}()
		synctest.Wait()
		select {
		case err := <-got:
			t.Fatalf("OpenVideo answered %v before Run probed", err)
		default:
		}
		run(t, w)
		if err := <-got; err != nil {
			t.Errorf("OpenVideo after the first probe: %v", err)
		}
	})
}
