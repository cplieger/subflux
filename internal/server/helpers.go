package server

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"subflux/internal/api"
	"subflux/internal/server/httphelpers"
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

// requireGET delegates to httphelpers.RequireGET.
func requireGET(w http.ResponseWriter, r *http.Request) bool {
	return httphelpers.RequireGET(w, r)
}

// decodeJSONBodyAny is a package-level function matching the signature
// required by manualops.HandlerDeps.DecodeJSON. Uses maxDefaultBodySize
// when maxSize is 0.
func decodeJSONBodyAny(w http.ResponseWriter, r *http.Request, dst any, maxSize int64) bool {
	if maxSize == 0 {
		maxSize = maxDefaultBodySize
	}
	return httphelpers.DecodeJSONBody(w, r, dst, maxSize)
}

// validateFSPath runs the configured filesystem path allowlist against
// p. Writes 403 and returns false if the path is invalid. The label is
// used in the error message ("invalid <label>").
func (s *Server) validateFSPath(w http.ResponseWriter, r *http.Request, p, label string) bool {
	if err := s.state().cfg.ValidatePath(r.Context(), p); err != nil {
		api.ForbiddenC(w, r, api.CodePathNotAllowed, "invalid "+label)
		return false
	}
	return true
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

// extractPathSegment extracts the segment between prefix and suffix in a URL path.
// Returns empty string if the path doesn't match the expected pattern.
func extractPathSegment(path, prefix, suffix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	if suffix != "" {
		idx := strings.Index(rest, suffix)
		if idx < 0 {
			return ""
		}
		rest = rest[:idx]
	}
	return rest
}
