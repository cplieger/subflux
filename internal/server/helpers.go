package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/cplieger/subflux/internal/server/httphelpers"
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

// maxDefaultBodySize references the canonical constant from httphelpers.
const maxDefaultBodySize = httphelpers.MaxDefaultBodySize

// decodeJSONBodyAny is a package-level function matching the signature
// required by manualops.HandlerDeps.DecodeJSON. Uses maxDefaultBodySize
// when maxSize is 0.
func decodeJSONBodyAny(w http.ResponseWriter, r *http.Request, dst any, maxSize int64) bool {
	if maxSize == 0 {
		maxSize = maxDefaultBodySize
	}
	return httphelpers.DecodeJSONBody(w, r, dst, maxSize)
}

// deleteSubtitleFiles validates and removes subtitle files from disk.
// Paths that fail media root validation are skipped (logged as warnings).
func (s *Server) deleteSubtitleFiles(paths []string, logCtx string) {
	ls := s.state()
	ctx := context.Background()
	for _, p := range paths {
		if err := ls.cfg.ValidatePath(ctx, p); err != nil {
			slog.Warn(logCtx+": path validation failed, skipping delete",
				"path", p, "error", err)
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			slog.Warn(logCtx+": failed to delete subtitle",
				"path", p, "error", err)
		} else if err == nil {
			slog.Info(logCtx+": deleted subtitle", "path", p)
		}
	}
}
