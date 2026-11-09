package mediawrite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cplieger/subflux/internal/statedb"
)

// OpenVideo opens the instance's video through its root's os.Root, so a
// symlink can resolve only inside the root, and refuses a non-regular file.
// The caller closes it. It answers ErrInvalidRef for an unknown instance,
// ErrOutsideRoot and ErrRootUnavailable as a write does, and any other
// failure as the path's wrapped error.
func (w *Writer) OpenVideo(ctx context.Context, inst statedb.InstanceID) (*os.File, error) {
	in, err := w.instance(ctx, inst)
	if err != nil {
		return nil, err
	}
	b, err := w.borrow(ctx, in.VideoPath)
	if err != nil {
		return nil, err
	}
	defer b.release()
	dir, err := w.fs.OpenDir(b.h.root, filepath.Dir(b.rel))
	if err != nil {
		return nil, fmt.Errorf("mediawrite: open video directory of instance %d: %w", inst, err)
	}
	defer dir.Close()
	f, err := w.fs.Open(dir, filepath.Base(b.rel))
	if err != nil {
		return nil, fmt.Errorf("mediawrite: open video of instance %d: %w", inst, err)
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", in.VideoPath)
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("mediawrite: open video of instance %d: %w", inst, err)
	}
	return f, nil
}

// instance reads inst from the store; an unknown one is ErrInvalidRef.
func (w *Writer) instance(ctx context.Context, inst statedb.InstanceID) (statedb.Instance, error) {
	in, found, err := w.fs.Instance(ctx, inst)
	switch {
	case err != nil:
		return statedb.Instance{}, fmt.Errorf("mediawrite: read instance %d: %w", inst, err)
	case !found:
		return statedb.Instance{}, fmt.Errorf("%w: instance %d is unknown", ErrInvalidRef, inst)
	}
	return in, nil
}
