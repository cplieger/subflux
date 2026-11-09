package mediawrite_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
)

// folderCalls starts count concurrent FolderState calls on path; collect
// returns the answers given once every call's deadline has passed, the zero
// state standing for a call still blocked.
func folderCalls(t *testing.T, w *mediawrite.Writer, path string, count int) (collect func() []mediawrite.FolderState) {
	t.Helper()
	answers := make(chan mediawrite.FolderState, count)
	for range count {
		go func() { answers <- w.FolderState(t.Context(), path) }()
	}
	return func() []mediawrite.FolderState {
		synctest.Wait()
		time.Sleep(mediawrite.RootProbeTimeout)
		synctest.Wait()
		out := make([]mediawrite.FolderState, count)
		for i := range out {
			select {
			case out[i] = <-answers:
			default:
			}
		}
		return out
	}
}

func allUnavailable(t *testing.T, what string, got []mediawrite.FolderState) {
	t.Helper()
	for i, s := range got {
		if s == 0 {
			t.Errorf("%s: call %d did not answer by its deadline", what, i)
		} else if s != mediawrite.FolderUnavailable {
			t.Errorf("%s: call %d answered %v, want unavailable", what, i, s)
		}
	}
}

func TestFolderStateOneOutstandingPerRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	for _, r := range []string{a, b, c} {
		if err := os.MkdirAll(filepath.Join(r, "Show"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store := openStore(t)
	synctest.Test(t, func(t *testing.T) {
		releaseA, releaseC := make(chan struct{}), make(chan struct{})
		var lstatsA, opensC atomic.Int32
		var holdA, holdC atomic.Bool
		holdA.Store(true)
		holdC.Store(true)
		w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(a, b, c)}, func(f *mediawrite.Faults) {
			stat, openRoot, lstat := f.Stat, f.OpenRoot, f.Lstat
			f.Stat = func(p string) (fs.FileInfo, error) {
				if p == c {
					return nil, &fs.PathError{Op: "stat", Path: p, Err: syscall.EIO}
				}
				return stat(p)
			}
			f.OpenRoot = func(p string) (*os.Root, error) {
				if p == c {
					opensC.Add(1)
					if holdC.Load() {
						<-releaseC
					}
				}
				return openRoot(p)
			}
			f.Lstat = func(root *os.Root, name string) (fs.FileInfo, error) {
				if root.Name() == a {
					lstatsA.Add(1)
					if holdA.Load() {
						<-releaseA
					}
				}
				return lstat(root, name)
			}
		})
		run(t, w)
		showA, showC := filepath.Join(a, "Show"), filepath.Join(c, "Show")

		collect := folderCalls(t, w, showA, 20)
		synctest.Wait()
		if s := w.FolderState(t.Context(), filepath.Join(b, "Show")); s != mediawrite.FolderFound {
			t.Errorf("root b while a hangs: %v, want found", s)
		}
		allUnavailable(t, "20 concurrent calls on hung a", collect())
		for i := range 20 {
			if s := w.FolderState(t.Context(), showA); s != mediawrite.FolderUnavailable {
				t.Errorf("sequential call %d on hung a: %v, want unavailable", i, s)
			}
		}
		if got := lstatsA.Load(); got != 1 {
			t.Errorf("Lstats outstanding on a = %d, want 1", got)
		}

		allUnavailable(t, "20 calls on c, whose open hangs", folderCalls(t, w, showC, 20)())
		if got := opensC.Load(); got != 1 {
			t.Errorf("opens outstanding on c = %d, want 1", got)
		}

		holdA.Store(false)
		close(releaseA)
		synctest.Wait()
		if s := w.FolderState(t.Context(), showA); s != mediawrite.FolderFound {
			t.Errorf("a after its check returned: %v, want found", s)
		}
		if got := lstatsA.Load(); got != 2 {
			t.Errorf("Lstats on a = %d, want a fresh one after the release", got)
		}
		holdC.Store(false)
		close(releaseC)
		synctest.Wait()
	})
}

func TestFolderStateStates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	up, down, noHandle := filepath.Join(dir, "up"), filepath.Join(dir, "down"), filepath.Join(dir, "nohandle")
	writeFile(t, filepath.Join(up, "Show", "S01E01.mkv"), "x")
	writeFile(t, filepath.Join(noHandle, "Show", "S01E01.mkv"), "x")
	var mu sync.Mutex
	var opened []*os.Root
	w := newWriter(t, mediawrite.Deps{Store: openStore(t), Roots: rootsOf(up, down, noHandle)}, func(f *mediawrite.Faults) {
		stat, openRoot := f.Stat, f.OpenRoot
		f.Stat = func(p string) (fs.FileInfo, error) {
			if p == noHandle {
				return nil, &fs.PathError{Op: "stat", Path: p, Err: syscall.EIO}
			}
			return stat(p)
		}
		f.OpenRoot = func(p string) (*os.Root, error) {
			r, err := openRoot(p)
			if err == nil && p == noHandle {
				mu.Lock()
				opened = append(opened, r)
				mu.Unlock()
			}
			return r, err
		}
	})
	run(t, w)

	for _, tc := range []struct {
		name, path string
		want       mediawrite.FolderState
	}{
		{"outside every root", filepath.Join(dir, "elsewhere"), mediawrite.FolderOutside},
		{"the root itself", up, mediawrite.FolderFound},
		{"a directory", filepath.Join(up, "Show"), mediawrite.FolderFound},
		{"absent", filepath.Join(up, "Gone"), mediawrite.FolderMissing},
		{"a regular file", filepath.Join(up, "Show", "S01E01.mkv"), mediawrite.FolderMissing},
		{"below a regular file", filepath.Join(up, "Show", "S01E01.mkv", "x"), mediawrite.FolderMissing},
		{"a root that cannot be opened", filepath.Join(down, "Show"), mediawrite.FolderUnavailable},
		{"a root without a live handle", filepath.Join(noHandle, "Show"), mediawrite.FolderFound},
	} {
		if got := w.FolderState(t.Context(), tc.path); got != tc.want {
			t.Errorf("%s: FolderState = %v, want %v", tc.name, got, tc.want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(opened) != 1 {
		t.Fatalf("handles opened for the root without one = %d, want 1", len(opened))
	}
	if _, err := opened[0].Stat("."); err == nil {
		t.Error("the handle opened for the call was left open")
	}
}

func TestFolderStateText(t *testing.T) {
	t.Parallel()
	for _, s := range []mediawrite.FolderState{mediawrite.FolderOutside, mediawrite.FolderUnavailable, mediawrite.FolderFound, mediawrite.FolderMissing} {
		b, err := s.MarshalText()
		if err != nil {
			t.Errorf("MarshalText(%d): %v", s, err)
			continue
		}
		var back mediawrite.FolderState
		if err := back.UnmarshalText(b); err != nil || back != s {
			t.Errorf("%q read back as %d, %v; want %d", b, back, err, s)
		}
	}
	for _, s := range []mediawrite.FolderState{0, mediawrite.FolderMissing + 1} {
		if b, err := s.MarshalText(); err == nil {
			t.Errorf("MarshalText(%d) = %q, want a refusal", s, b)
		}
	}
	for _, text := range []string{"", "Found", "found ", "present", "1"} {
		s := mediawrite.FolderFound
		if err := s.UnmarshalText([]byte(text)); err == nil || s != mediawrite.FolderFound {
			t.Errorf("UnmarshalText(%q) = %d, %v; want a refusal leaving the value", text, s, err)
		}
	}
	want := map[mediawrite.FolderState]string{
		mediawrite.FolderOutside: "outside", mediawrite.FolderUnavailable: "unavailable",
		mediawrite.FolderFound: "found", mediawrite.FolderMissing: "missing",
	}
	for s, text := range want {
		if b, _ := s.MarshalText(); string(b) != text {
			t.Errorf("MarshalText(%d) = %q, want %q", s, b, text)
		}
	}
}
