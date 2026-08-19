package filehandlers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cplieger/subflux/internal/subtitleext"
)

// This file owns the subtitle-scoped file deletion gate: the delete
// capability check from the subtitle-extension authority
// (internal/subtitleext) layered in front of the generic media-root
// containment delete (config.RemoveUnderRoot). Generic config containment
// stays generic; subtitle policy lives here.
//
// The gate lives in this package because its callers are exactly the
// filehandlers delete paths (single, bulk, orphan-handle) — it was a
// package of its own until the one-importer boundary was rolled up.
// Reconciliation performs no disk deletes today (it deletes DB rows only),
// so there is no third caller to hunt for.

// errSubtitleExtensionNotAllowed is returned when a deletion target's
// extension does not carry the delete capability in the subtitle-extension
// authority. Handlers map it to 409 subtitle_extension_not_allowed: a
// server-derived extension conflict is stored-state disagreement, not caller
// authorization, and the refusal is loud (WARN log at the handler), never a
// silent skip.
var errSubtitleExtensionNotAllowed = errors.New("extension does not carry the subtitle delete capability")

// remover is the containment-delete seam this gate delegates to: one of the 37
// values the config offers, and the only one the gate itself needs.
type remover interface {
	RemoveUnderRoot(ctx context.Context, path string) error
}

// removeSubtitleUnderRoot deletes path through the subtitle delete gate: the
// extension must carry the delete capability in the subtitle-extension
// authority, then deletion delegates to the generic media-root containment
// delete unchanged.
func removeSubtitleUnderRoot(ctx context.Context, rem remover, path string) error {
	if !subtitleext.Delete(path) {
		return fmt.Errorf("delete %q (ext %q): %w", path, filepath.Ext(path), errSubtitleExtensionNotAllowed)
	}
	return rem.RemoveUnderRoot(ctx, path)
}
