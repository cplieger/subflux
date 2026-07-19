// Package subtitlepath owns the subtitle-scoped file deletion gate: the
// delete capability check from the subtitle-extension authority
// (internal/subtitleext) layered in front of the generic media-root
// containment delete (config.RemoveUnderRoot). Generic config containment
// stays generic; subtitle policy lives here.
//
// Callers are exactly the filehandlers delete paths (single, bulk,
// orphan-handle). Reconciliation performs no disk deletes today (it deletes
// DB rows only), so there is no third caller to hunt for.
package subtitlepath

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cplieger/subflux/internal/subtitleext"
)

// ErrSubtitleExtensionNotAllowed is returned when a deletion target's
// extension does not carry the delete capability in the subtitle-extension
// authority. Handlers map it to 409 subtitle_extension_not_allowed: a
// server-derived extension conflict is stored-state disagreement, not caller
// authorization, and the refusal is loud (WARN log at the handler), never a
// silent skip.
var ErrSubtitleExtensionNotAllowed = errors.New("extension does not carry the subtitle delete capability")

// Remover is the containment-delete seam this gate delegates to; satisfied
// by api.ConfigProvider (config.RemoveUnderRoot's os.Root containment).
type Remover interface {
	RemoveUnderRoot(ctx context.Context, path string) error
}

// RemoveUnderRoot deletes path through the subtitle delete gate: the
// extension must carry the delete capability in the subtitle-extension
// authority, then deletion delegates to the generic media-root containment
// delete unchanged.
func RemoveUnderRoot(ctx context.Context, remover Remover, path string) error {
	if !subtitleext.Delete(path) {
		return fmt.Errorf("delete %q (ext %q): %w", path, filepath.Ext(path), ErrSubtitleExtensionNotAllowed)
	}
	return remover.RemoveUnderRoot(ctx, path)
}
