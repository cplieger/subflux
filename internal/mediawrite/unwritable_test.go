package mediawrite_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
)

func TestUnwritableDirSkipsProviderCalls(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tmp := t.TempDir()
		root := filepath.Join(tmp, "b")
		show := filepath.Join(root, "Show")
		videos := []string{filepath.Join(show, "S01E01.mkv"), filepath.Join(show, "S01E02.mkv"), filepath.Join(root, "Other", "S01E03.mkv")}
		for _, v := range videos {
			writeFile(t, v, "video")
		}
		store := openStoreAt(t, filepath.Join(tmp, "subflux.bolt"), enOnly)
		ids := seed(t, store, videos...)
		var denyStage, denyRemove, rootDown atomic.Bool
		var frozen atomic.Pointer[fs.FileInfo]
		w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(root)}, func(f *mediawrite.Faults) {
			create, remove, stat, dirStat := f.CreateStage, f.Remove, f.Stat, f.DirStat
			f.DirStat = func(dir *os.Root) (fs.FileInfo, error) {
				if info := frozen.Load(); info != nil && filepath.Base(dir.Name()) == "Show" {
					return *info, nil
				}
				return dirStat(dir)
			}
			f.CreateStage = func(dir *os.Root, name string) (*os.File, error) {
				if denyStage.Load() && filepath.Base(dir.Name()) == "Show" {
					return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
				}
				return create(dir, name)
			}
			f.Remove = func(dir *os.Root, name string) error {
				if denyRemove.Load() && strings.HasPrefix(name, "S01E01.") {
					return &fs.PathError{Op: "unlinkat", Path: name, Err: syscall.EACCES}
				}
				return remove(dir, name)
			}
			f.Stat = func(p string) (fs.FileInfo, error) {
				if rootDown.Load() && p == root {
					return nil, &fs.PathError{Op: "stat", Path: p, Err: syscall.ENOENT}
				}
				return stat(p)
			}
		})
		run(t, w)
		unwritable := func(inst statedb.InstanceID) bool {
			t.Helper()
			inv, err := w.List(t.Context(), inst, mediawrite.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return inv.Unwritable
		}
		refused := func(what string) {
			t.Helper()
			denyStage.Store(true)
			if _, err := w.Deliver(t.Context(), delivery(t, ids[0], what)); !errors.Is(err, syscall.EACCES) {
				t.Fatalf("Deliver into the refusing folder = %v, want its EACCES", err)
			}
			if !unwritable(ids[0]) || !unwritable(ids[1]) {
				t.Fatalf("after %s, the instances in the folder list writable", what)
			}
		}

		if unwritable(ids[0]) {
			t.Fatal("a folder nothing was refused in lists unwritable")
		}
		refused("a refused stage")
		if unwritable(ids[2]) {
			t.Error("an instance in another folder lists unwritable")
		}

		if err := os.Chmod(show, 0o750); err != nil {
			t.Fatal(err)
		}
		if unwritable(ids[0]) || unwritable(ids[1]) {
			t.Error("a chmod of the folder left it unwritable")
		}

		refused("a second refusal")
		time.Sleep(24 * time.Hour)
		if unwritable(ids[0]) {
			t.Error("the folder still lists unwritable after 24 hours")
		}

		refused("a third refusal")
		rootDown.Store(true)
		if mediawrite.ProbeNow(t.Context(), w, root) {
			t.Fatal("the unmounted root probed available")
		}
		rootDown.Store(false)
		if !mediawrite.ProbeNow(t.Context(), w, root) {
			t.Fatal("the remounted root probed unavailable")
		}
		if unwritable(ids[0]) {
			t.Error("the folder still lists unwritable after its root turned unavailable")
		}

		// From here the folder's fstat holds still, as on a share whose
		// directory ctime does not move when an entry is created, so only
		// the writer's own rules can clear or set the memory.
		info, err := os.Stat(show)
		if err != nil {
			t.Fatal(err)
		}
		frozen.Store(&info)
		refused("a fourth refusal")
		denyStage.Store(false)
		if _, err := w.Deliver(t.Context(), delivery(t, ids[1], "lands")); err != nil {
			t.Fatal(err)
		}
		if unwritable(ids[0]) {
			t.Error("a stage created in the folder left it unwritable")
		}

		first, err := w.Deliver(t.Context(), delivery(t, ids[0], "first"))
		if err != nil {
			t.Fatal(err)
		}
		denyRemove.Store(true)
		d := delivery(t, ids[0], "second")
		d.Numbered = true
		d.Supersedes = []mediawrite.Superseded{{File: mediawrite.RefOf(first), Expect: mediawrite.Expectation{Generation: first.Generation, SHA256: first.Identity.SHA256}}}
		if _, err := w.Deliver(t.Context(), d); !errors.Is(err, syscall.EACCES) {
			t.Fatalf("Deliver whose supersede removal is refused = %v, want its EACCES", err)
		}
		if unwritable(ids[0]) || unwritable(ids[1]) {
			t.Error("a refused supersede removal made the folder unwritable")
		}
	})
}
