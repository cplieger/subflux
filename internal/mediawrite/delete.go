package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

// Delete removes ref's file after confirming expect under the leases, with
// its record, or for an unmanaged file its place among the local files. A
// .sub is ErrFormatUnsupported: removing it alone would orphan its .idx.
func (w *Writer) Delete(ctx context.Context, ref FileRef, expect Expectation) error {
	t, err := w.acquire(ctx, ref)
	if err != nil {
		return w.refused(ref.inst, err)
	}
	defer t.release()
	if strings.EqualFold(filepath.Ext(ref.name), ".sub") {
		return fmt.Errorf("%w: %q is VobSub, whose .idx would be left behind", timing.ErrFormatUnsupported, ref.name)
	}
	if err := w.confirm(ctx, t, expect); err != nil {
		return w.refused(ref.inst, err)
	}
	s := staged{l: t.l, name: t.name, op: statedb.Delete}
	if err := w.openStaged(&s); err != nil {
		return w.refused(ref.inst, err)
	}
	defer s.close()
	if err := w.store.BeginDelete(ctx, ref.inst, ref.name, t.sum); err != nil {
		return w.refused(ref.inst, fmt.Errorf("%w of %q: %w", errBegin, ref.name, err))
	}
	w.fs.Step("begin")
	// Past the intent the call runs to a settled outcome, as Deliver does.
	ctx = context.WithoutCancel(ctx)
	return w.remove(ctx, t, &s)
}

// remove deletes t's file once a re-hash shows the bytes t confirmed; a
// file changed since is an outside write, and the removal settles not
// landed.
func (w *Writer) remove(ctx context.Context, t *target, s *staged) error {
	sum, _, err := w.hashFile(t.l.dir, t.name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return w.abandon(ctx, s, "", fmt.Errorf("%w: %q was removed outside subflux", ErrExpectation, t.name))
	case err != nil:
		return w.abandon(ctx, s, "", fmt.Errorf("mediawrite: re-hash %q: %w", t.name, err))
	case sum != t.sum:
		return w.abandon(ctx, s, "", fmt.Errorf("%w: %q changed before its removal", ErrExpectation, t.name))
	}
	if err := w.fs.Remove(t.l.dir, t.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return w.leave(s, w.writeErr(ctx, t.b, err))
	}
	w.fs.Step("removed")
	if err := w.fs.FsyncDir(s.dirFile); err != nil {
		return w.leave(s, err)
	}
	w.fs.Step("synced")
	if err := w.end(ctx, s, true); err != nil {
		return w.leave(s, err)
	}
	return nil
}
