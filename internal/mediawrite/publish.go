package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"syscall"
)

// publishMode is the step of the publish chain a publish ended on.
type publishMode uint8

const (
	modeRenameNoReplace publishMode = iota + 1
	modeLink
	modeChecked
)

var modeText = [...]string{modeRenameNoReplace: "renameat2 RENAME_NOREPLACE", modeLink: "hard link", modeChecked: "checked rename"}

// publish moves stage to the chosen name without replacing a file there,
// walking the chain on every call, since one root can span mounts: a
// no-replace rename, else a hard link and the stage's removal, else an
// Lstat showing the name absent and a plain rename. Each falls through only
// on the errnos that say its mount lacks it. A name found taken is
// ErrNameContended. Rename and link keep the stage's inode, so the
// recorded identity is the final file's.
func (w *Writer) publish(p *staged, stage string) error {
	err := w.fs.RenameNoReplace(p.dirFile, stage, p.name)
	switch {
	case err == nil:
		w.modes.first(w.log, p, modeRenameNoReplace)
		return nil
	case errors.Is(err, fs.ErrExist):
		return contended(p, err)
	case !isErrno(err, syscall.EINVAL, syscall.ENOSYS, syscall.EOPNOTSUPP, syscall.ENOTSUP):
		return err
	}
	err = w.fs.Link(p.l.dir, stage, p.name)
	switch {
	case err == nil:
		w.modes.first(w.log, p, modeLink)
		w.fs.Step("linked")
		if rmErr := w.fs.Remove(p.l.dir, stage); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return fmt.Errorf("mediawrite: remove the linked stage of %q: %w", p.name, rmErr)
		}
		return nil
	case errors.Is(err, fs.ErrExist):
		return contended(p, err)
	case !isErrno(err, syscall.EPERM, syscall.EOPNOTSUPP, syscall.ENOTSUP, syscall.EXDEV):
		return err
	}
	free, err := w.free(p.l, p.name)
	switch {
	case err != nil:
		return err
	case !free:
		return contended(p, fs.ErrExist)
	}
	if err := w.fs.Rename(p.l.dir, stage, p.name); err != nil {
		return err
	}
	w.modes.first(w.log, p, modeChecked)
	return nil
}

func contended(p *staged, err error) error {
	return fmt.Errorf("%w: an outside writer took %q of instance %d: %w", ErrNameContended, p.name, p.l.inst, err)
}

func isErrno(err error, errnos ...syscall.Errno) bool {
	for _, e := range errnos {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// publishModes remembers which publish modes each device has used, so each
// mode's first use on a device is logged once per process.
type publishModes struct {
	seen map[modeUse]bool
	mu   sync.Mutex
}

type modeUse struct {
	dev  uint64
	mode publishMode
}

// first logs mode's first use on p's device: INFO, or WARN for the checked
// rename, which an outside writer racing the instant between its Lstat and
// its rename could see replace its file.
func (m *publishModes) first(log *slog.Logger, p *staged, mode publishMode) {
	m.mu.Lock()
	k := modeUse{dev: p.dirID.dev, mode: mode}
	seen := m.seen[k]
	if m.seen == nil {
		m.seen = map[modeUse]bool{}
	}
	m.seen[k] = true
	m.mu.Unlock()
	if seen {
		return
	}
	level := slog.LevelInfo
	if mode == modeChecked {
		level = slog.LevelWarn
	}
	log.Log(context.Background(), level, "mediawrite: publishing on this device by "+modeText[mode], "device", p.dirID.dev, "dir", p.l.b.state.path)
}
