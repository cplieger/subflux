package adminsock

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"

	"github.com/cplieger/go/filesafe"
)

// The container's socket directory and path.
const (
	Dir  = "/tmp/subflux-admin"
	Path = Dir + "/admin.sock"
)

// Listen takes custody of dir (created 0700; a symlink, a foreign owner, or
// group or other access is refused), removes any entry an unclean exit left
// at path, binds path and makes it 0600. The caller must already hold the
// state store's lock: that proves no live process owns the socket.
func Listen(ctx context.Context, dir, path string) (net.Listener, error) {
	if _, err := filesafe.EnsurePrivateDir(dir); err != nil {
		return nil, fmt.Errorf("adminsock: socket directory %s: %w", dir, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("adminsock: remove the stale socket: %w", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("adminsock: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("adminsock: chmod the socket: %w", err), ln.Close())
	}
	return ln, nil
}
