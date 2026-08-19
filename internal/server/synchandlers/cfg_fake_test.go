package synchandlers

import (
	"context"
)

// fakePathValidator is the config double the sync-handler harness hands to the
// resolver: ONE method, the containment check resolve runs on a resolved path.
// It accepts every path, which is all these tests need — they assert on the
// sync surface, not on containment. It replaces the shared 28-method
// testsupport.NopConfig, of which this package reached exactly this one.
type fakePathValidator struct{}

func (fakePathValidator) ValidatePath(context.Context, string) error { return nil }
