package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/cplieger/subflux/internal/httpapi"
)

// Handler preludes. Every handler opens with the same pattern:
//
//  1. Check method → 405 if wrong
//  2. (optional) Decode JSON body with size cap → 400 on failure
//  3. (optional) Validate a filesystem path against the allowlist → 403 on failure
//
// These helpers return a bool indicating whether the handler should
// continue. They write the error response themselves.
//
// Pattern mirrors apps/vibekit/web/internal/git/helpers.go:
// tiny, single-purpose, opinionated about the response shape.

// maxDefaultBodySize references the canonical constant from api.
const maxDefaultBodySize = httpapi.MaxDefaultBodySize

// decodeJSONBodyAny is a package-level function matching the signature
// required by manualops.HandlerDeps.DecodeJSON. Uses maxDefaultBodySize
// when maxSize is 0.
func decodeJSONBodyAny(w http.ResponseWriter, r *http.Request, dst any, maxSize int64) bool {
	if maxSize == 0 {
		maxSize = maxDefaultBodySize
	}
	return httpapi.DecodeJSONBody(w, r, dst, maxSize)
}

// deleteSubtitleFiles removes subtitle files from disk through the media-root
// confinement, one warning per refusal.
//
// RemoveUnderRoot resolves and unlinks through the same *os.Root handle that
// authorizes the path, so there is no window in which a component of an
// already-validated path can be swapped for a symlink out of the tree. The
// subtitle-delete path in filehandlers uses the same call; a validate-then-
// os.Remove pair here would leave the app's two delete paths disagreeing about
// their containment guarantee.
func (s *Server) deleteSubtitleFiles(paths []string, logCtx string) {
	ls := s.state()
	ctx := context.Background()
	for _, p := range paths {
		if err := ls.cfg.RemoveUnderRoot(ctx, p); err != nil {
			slog.Warn(logCtx+": failed to delete subtitle",
				"path", p, "error", err)
			continue
		}
		slog.Info(logCtx+": deleted subtitle", "path", p)
	}
}
