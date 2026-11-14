package adminsock

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/go/filesafe"
)

// sockPaths answers a socket directory, not yet created, and its socket,
// under a short temporary root so the path fits a sun_path.
func sockPaths(t *testing.T) (dir, path string) {
	t.Helper()
	root, err := os.MkdirTemp("", "as")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir = filepath.Join(root, "d")
	return dir, filepath.Join(dir, "s.sock")
}

func listens(t *testing.T, dir, path string) net.Listener {
	t.Helper()
	ln, err := Listen(t.Context(), dir, path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func dials(t *testing.T, path string) {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial the socket: %v", err)
	}
	_ = c.Close()
}

func TestListenCustody(t *testing.T) {
	t.Run("a fresh directory is 0700 and the socket 0600", func(t *testing.T) {
		dir, path := sockPaths(t)
		listens(t, dir, path)
		for p, want := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
			fi, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode().Perm(); got != want {
				t.Errorf("%s mode = %v, want %v", filepath.Base(p), got, want)
			}
		}
		dials(t, path)
	})
	t.Run("a leftover file from a crash is removed and bound", func(t *testing.T) {
		dir, path := sockPaths(t)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		listens(t, dir, path)
		dials(t, path)
	})
	t.Run("a leftover socket of a dead process is removed and bound", func(t *testing.T) {
		dir, path := sockPaths(t)
		old := listens(t, dir, path)
		old.(*net.UnixListener).SetUnlinkOnClose(false)
		_ = old.Close()
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("the abandoned socket file is gone: %v", err)
		}
		listens(t, dir, path)
		dials(t, path)
	})
	for _, mode := range []os.FileMode{0o750, 0o770} {
		t.Run("a "+mode.String()+" directory is refused unchanged", func(t *testing.T) {
			dir, path := sockPaths(t)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := Listen(t.Context(), dir, path); !errors.Is(err, filesafe.ErrModeTooOpen) {
				t.Errorf("Listen: %v, want ErrModeTooOpen", err)
			}
			if fi, err := os.Lstat(dir); err != nil || fi.Mode().Perm() != mode {
				t.Errorf("the refused directory changed: %v %v", fi.Mode(), err)
			}
			if _, err := os.Lstat(path); err == nil {
				t.Error("a socket was bound in a refused directory")
			}
		})
	}
	t.Run("a symlinked directory is refused", func(t *testing.T) {
		dir, path := sockPaths(t)
		target := dir + "-target"
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, dir); err != nil {
			t.Fatal(err)
		}
		if _, err := Listen(t.Context(), dir, path); !errors.Is(err, filesafe.ErrSymlinkTarget) {
			t.Errorf("Listen: %v, want ErrSymlinkTarget", err)
		}
	})
	t.Run("a directory of another owner is refused", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("needs root to chown; the mode and symlink cases are its unprivileged witnesses")
		}
		dir, path := sockPaths(t)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		if _, err := Listen(t.Context(), dir, path); !errors.Is(err, filesafe.ErrNotOwned) {
			t.Errorf("Listen: %v, want ErrNotOwned", err)
		}
	})
}
