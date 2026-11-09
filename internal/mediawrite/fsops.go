package mediawrite

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/cplieger/go/filesafe"
	"github.com/cplieger/subflux/internal/statedb"
	"golang.org/x/sys/unix"
)

// fsOps is every filesystem call the writer makes, End, the store write
// settling an intent, Instance, the store read mapping an instance to its
// video, and Step, called at each boundary a crash can fall on. Tests
// replace fields through export_test.go to inject faults; production never
// branches on it.
type fsOps struct {
	Stat            func(path string) (fs.FileInfo, error)
	OpenRoot        func(path string) (*os.Root, error)
	OpenDir         func(parent *os.Root, name string) (*os.Root, error)
	Open            func(dir *os.Root, name string) (*os.File, error)
	Lstat           func(dir *os.Root, name string) (fs.FileInfo, error)
	ReadDir         func(dir *os.Root) ([]fs.DirEntry, error)
	OpenRead        func(dir *os.Root, name string) (*os.File, fs.FileInfo, error)
	ProbeWrite      func(ctx context.Context, root *os.Root) error
	DirStat         func(dir *os.Root) (fs.FileInfo, error)
	CreateStage     func(dir *os.Root, name string) (*os.File, error)
	Write           func(f *os.File, b []byte) error
	Futimens        func(f *os.File, mtime time.Time) error
	Fsync           func(f *os.File) error
	Close           func(f *os.File) error
	FsyncDir        func(dir *os.File) error
	RenameNoReplace func(dir *os.File, from, to string) error
	Link            func(dir *os.Root, from, to string) error
	Rename          func(dir *os.Root, from, to string) error
	Remove          func(dir *os.Root, name string) error
	End             func(ctx context.Context, op statedb.Op, inst statedb.InstanceID, name string, landed bool) error
	Instance        func(ctx context.Context, inst statedb.InstanceID) (statedb.Instance, bool, error)
	Step            func(step string)
}

func productionFS(log *slog.Logger, store *statedb.Store) fsOps {
	return fsOps{
		Stat:            os.Stat,
		OpenRoot:        os.OpenRoot,
		OpenDir:         (*os.Root).OpenRoot,
		Open:            (*os.Root).Open,
		Lstat:           (*os.Root).Lstat,
		ReadDir:         readDir,
		OpenRead:        filesafe.OpenRegularInRootNoFollow,
		ProbeWrite:      func(ctx context.Context, root *os.Root) error { return probeWritable(ctx, root, log) },
		DirStat:         func(dir *os.Root) (fs.FileInfo, error) { return dir.Stat(".") },
		CreateStage:     createStage,
		Write:           writeAll,
		Futimens:        futimens,
		Fsync:           (*os.File).Sync,
		Close:           (*os.File).Close,
		FsyncDir:        (*os.File).Sync,
		RenameNoReplace: renameNoReplace,
		Link:            (*os.Root).Link,
		Rename:          (*os.Root).Rename,
		Remove:          (*os.Root).Remove,
		End:             endOf(store),
		Instance:        store.Instance,
		Step:            func(string) {},
	}
}

// endOf settles an intent of op through store's End of that op.
func endOf(store *statedb.Store) func(ctx context.Context, op statedb.Op, inst statedb.InstanceID, name string, landed bool) error {
	return func(ctx context.Context, op statedb.Op, inst statedb.InstanceID, name string, landed bool) error {
		switch op {
		case statedb.Deliver:
			return store.EndDeliver(ctx, inst, name, landed)
		case statedb.Retime:
			return store.EndRetime(ctx, inst, name, landed)
		case statedb.Delete:
			return store.EndDelete(ctx, inst, name, landed)
		}
		return fmt.Errorf("mediawrite: settle %q of instance %d: unknown op %d", name, inst, op)
	}
}

// probeWritable writes, flushes and removes one temp file at the root's top,
// then sweeps that level of its own temps a failed removal left.
func probeWritable(ctx context.Context, root *os.Root, log *slog.Logger) error {
	res, err := filesafe.ProbeWritableInRoot(ctx, root, ".")
	if err != nil {
		return err
	}
	if !res.Writable() {
		return fmt.Errorf("write probe: %s: %w", res.Phase, res.Err)
	}
	if _, err := filesafe.CleanupStaleTempsInRoot(ctx, root, staleTempAge); err != nil {
		log.Warn("mediawrite: sweeping the write probe's temps failed", "root", root.Name(), "err", err)
	}
	return nil
}

// readDir lists dir once, sorted by name.
func readDir(dir *os.Root) ([]fs.DirEntry, error) {
	d, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, err
}

// createStage creates name for writing only if nothing is there; 0o666 so
// the umask, as for any file a media server reads, decides the mode.
func createStage(dir *os.Root, name string) (*os.File, error) {
	return dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
}

func writeAll(f *os.File, b []byte) error {
	_, err := f.Write(b)
	return err
}

// futimens sets f's access and modification times to mtime through f
// itself: x/sys/unix has no futimens, and its Futimes reaches the open file
// through /proc/self/fd.
func futimens(f *os.File, mtime time.Time) error {
	tv := unix.NsecToTimeval(mtime.UnixNano())
	return controlErr(f, func(fd int) error { return unix.Futimes(fd, []unix.Timeval{tv, tv}) })
}

// renameNoReplace renames from to to inside the directory dir has open,
// failing with EEXIST when to exists.
func renameNoReplace(dir *os.File, from, to string) error {
	err := controlErr(dir, func(fd int) error { return unix.Renameat2(fd, from, fd, to, unix.RENAME_NOREPLACE) })
	if err != nil {
		return &os.LinkError{Op: "renameat2", Old: from, New: to, Err: err}
	}
	return nil
}

// controlErr runs op on f's descriptor without taking it out of the
// runtime poller, as Fd would.
func controlErr(f *os.File, op func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var opErr error
	if err := rc.Control(func(fd uintptr) { opErr = op(int(fd)) }); err != nil {
		return err
	}
	return opErr
}
