package filehandlers

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// fakePathGuard is the config double for this package's tests, sized to
// pathGuard: TWO methods, the containment check and the confined remove. It
// replaces the shared 28-method testsupport.NopConfig, of which these tests set
// exactly one field — the injected error, kept here as pathErr.
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
