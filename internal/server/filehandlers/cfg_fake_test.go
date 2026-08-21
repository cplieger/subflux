package filehandlers

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// fakePathGuard is the config double for this package's tests, sized to
// pathGuard: TWO methods out of the 37 a *config.Config offers — the
// containment check and the confined remove. One knob, pathErr, which is the
// only thing these tests ever varied.
//
// RemoveUnderRoot really removes, tolerating an already-missing path, because
// the delete-gate tests assert on what is left on disk. The tolerated errors
// mirror the production accessor's loose semantics.
type fakePathGuard struct {
	pathErr error
}

var _ pathGuard = (*fakePathGuard)(nil)

func (c *fakePathGuard) ValidatePath(context.Context, string) error { return c.pathErr }

func (c *fakePathGuard) RemoveUnderRoot(_ context.Context, path string) error {
	if c.pathErr != nil {
		return c.pathErr
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
		return err
	}
	return nil
}
