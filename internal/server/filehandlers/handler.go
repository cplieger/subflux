// Package filehandlers provides HTTP handlers for the /api/files/* endpoints.
//
// S7 addressing contract: the wire carries NO filesystem paths in either
// direction. Listings expose the typed per-file identity (media_type,
// media_id, language, variant, source, ordinal) plus display metadata;
// deletion accepts that FileRef (or a server-minted orphan handle) and
// resolves the path from the store. S16: every disk delete routes through
// the subtitle-scoped gate (subtitlepath.RemoveUnderRoot), which refuses
// non-subtitle extensions loudly with 409 subtitle_extension_not_allowed.
package filehandlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/httphelpers"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/subtitlepath"
	"github.com/cplieger/subflux/internal/subtitleext"
)

// FileStore is the narrow store interface used by file handlers.
type FileStore interface {
	GetSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.SubtitleEntry, error)
	DeleteSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant, source api.SubtitleSource, path string) error
	ManualSubtitlePaths(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) ([]string, error)
	ClearManualLock(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) error
	HistoryMediaIDs(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]string, error)
}

// Compile-time assertion: api.Store satisfies FileStore.
var _ FileStore = api.Store(nil)

// Deps holds the dependencies for file handlers.
type Deps struct {
	Store     FileStore
	Resolve   *resolve.Resolver
	StateFunc func() *LiveState
	Events    EventPublisher
}

// FileSonarrClient is the Sonarr surface the bound orphan fallback needs:
// the series identity for the media_id binding check plus the episodes'
// file paths for the per-episode directory narrowing.
type FileSonarrClient interface {
	GetSeriesByID(ctx context.Context, id int) (arrapi.Series, error)
	GetEpisodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error)
}

// FileRadarrClient is the Radarr surface the bound orphan fallback needs:
// the movie's identity for the media_id binding check plus its file path.
type FileRadarrClient interface {
	GetMovieByID(ctx context.Context, id int) (arrapi.Movie, error)
}

// Compile-time assertions: the live arr client interfaces carry the orphan
// fallback surface, so server_init's interface-to-interface assignment into
// LiveState stays valid.
var (
	_ FileSonarrClient = api.SonarrClient(nil)
	_ FileRadarrClient = api.RadarrClient(nil)
)

// LiveState holds the runtime state needed by file handlers. The arr
// clients (nil when unconfigured) feed the orphan walk's all-orphan
// fallback: a media item whose files are all orphans has no store-derivable
// directory, so its directory is resolved via the arr item — but only after
// the arr item's external identity is verified to match the requested
// media_id (the client-supplied media_id/arr_id pairing is never trusted).
type LiveState struct {
	Cfg    api.ConfigProvider
	Sonarr FileSonarrClient
	Radarr FileRadarrClient
}

// EventPublisher is the narrow interface for publishing events.
type EventPublisher interface {
	Publish(e events.Event)
}

// Handler provides HTTP handlers for the /api/files/* endpoints.
type Handler struct {
	deps    Deps
	orphans *orphanTable
}

// NewHandler creates a file Handler with the given dependencies.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps, orphans: newOrphanTable()}
}

// FileEntry is the JSON shape for the file manager API. It carries no
// filesystem paths: (media_id, language, variant, source, ordinal) is the
// FileRef identity a client echoes back to address the file; Name is the
// display basename. Orphan rows (on disk, no store row) additionally carry
// the single-use OrphanHandle and only Name/Size beside it.
type FileEntry struct {
	MediaID      string `json:"media_id"`
	Language     string `json:"language"`
	Variant      string `json:"variant"`
	Source       string `json:"source"`
	Codec        string `json:"codec,omitempty"`
	Name         string `json:"name,omitempty"`
	OrphanHandle string `json:"orphan_handle,omitempty"`
	Score        int    `json:"score,omitempty"`
	Ordinal      int    `json:"ordinal,omitempty"`
	OffsetMs     int64  `json:"offset_ms,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

// HandleListFiles returns subtitle files for a media item with file sizes,
// including orphans discovered by the server-side directory walk.
// GET /api/files?media_type=movie&media_id=tmdb-1271[&arr_id=42]
func (h *Handler) HandleListFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	mediaType := r.URL.Query().Get("media_type")
	mediaID := r.URL.Query().Get("media_id")
	if mediaType == "" || mediaID == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "media_type and media_id required")
		return
	}
	if !api.MediaType(mediaType).Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid media_type")
		return
	}
	arrID := 0 // absent disables the arr fallback
	if raw := r.URL.Query().Get("arr_id"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			api.BadRequestC(w, r, api.CodeBadRequest, "arr_id must be a positive integer")
			return
		}
		arrID = n
	}

	rows, err := h.deps.Store.GetSubtitleFiles(ctx, api.MediaType(mediaType), mediaID)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "list files")
		return
	}

	ls := h.deps.StateFunc()
	entries := storeEntries(ctx, ls, rows)
	entries, err = h.appendOrphans(ctx, ls, api.MediaType(mediaType), mediaID, arrID, rows, entries)
	if err != nil {
		writeArrBindingError(w, r, err)
		return
	}

	api.WriteJSON(w, entries)
}

// storeEntries maps the store rows onto listing FileEntries, statting each
// contained on-disk path for its size.
func storeEntries(ctx context.Context, ls *LiveState, rows []api.SubtitleEntry) []FileEntry {
	entries := make([]FileEntry, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		e := FileEntry{
			MediaID:  row.MediaID,
			Language: row.Language,
			Variant:  row.Variant,
			Source:   row.Source,
			Codec:    row.Codec,
			Score:    row.Score,
			Ordinal:  row.Ordinal,
			OffsetMs: row.OffsetMs,
		}
		if row.Path != "" {
			e.Name = filepath.Base(row.Path)
			if err := ls.Cfg.ValidatePath(ctx, row.Path); err == nil {
				if fi, statErr := os.Stat(row.Path); statErr == nil { //nolint:gosec // G703: path validated by ValidatePath above
					e.Size = fi.Size()
				}
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// DeleteFileRequest is the typed body for DELETE /api/files: either the
// FileRef fields addressing one stored subtitle file, or a server-minted
// orphan handle from the listing (mutually exclusive; orphan_handle wins).
type DeleteFileRequest struct {
	MediaType    api.MediaType `json:"media_type,omitempty"`
	MediaID      string        `json:"media_id,omitempty"`
	Language     string        `json:"language,omitempty"`
	Variant      string        `json:"variant,omitempty"`
	Source       string        `json:"source,omitempty"`
	OrphanHandle string        `json:"orphan_handle,omitempty"`
	Ordinal      int           `json:"ordinal,omitempty"`
}

// HandleDeleteFile deletes a single external subtitle file addressed by
// FileRef (store-resolved path) or orphan handle (TTL table).
// DELETE /api/files
func (h *Handler) HandleDeleteFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodDelete {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	var req DeleteFileRequest
	if !httphelpers.DecodeJSONBody(w, r, &req, 1<<20) {
		return
	}

	if req.OrphanHandle != "" {
		h.deleteOrphan(ctx, w, r, req.OrphanHandle)
		return
	}

	if req.MediaType == "" || req.MediaID == "" || req.Language == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "media_type, media_id, and language required (or orphan_handle)")
		return
	}
	if !req.MediaType.Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid media_type")
		return
	}
	if req.Variant == "" {
		req.Variant = string(api.VariantStandard)
	}
	if req.Source == "" {
		req.Source = string(api.SourceExternal)
	}

	ref := resolve.FileRef{
		MediaType: req.MediaType,
		MediaID:   req.MediaID,
		Language:  req.Language,
		Variant:   req.Variant,
		Source:    req.Source,
		Ordinal:   req.Ordinal,
	}
	path, err := h.deps.Resolve.SubtitlePath(ctx, &ref)
	if err != nil {
		resolve.WriteError(w, r, err)
		return
	}

	if !h.removeSubtitleFile(ctx, w, r, path) {
		return
	}

	slog.Info("subtitle file deleted", "path", path)

	if err := h.deps.Store.DeleteSubtitleFile(ctx,
		ref.MediaType, ref.MediaID, ref.Language, api.Variant(ref.Variant), api.SourceExternal, path); err != nil {
		slog.Warn("delete file: db cleanup failed", "error", err)
	}

	h.maybeRevertManualLock(ctx, ref.MediaType, ref.MediaID, ref.Language, api.Variant(ref.Variant))

	h.deps.Events.Publish(events.Event{
		Type: events.CoverageUpdate,
		Data: events.CoverageEvent{
			MediaType: ref.MediaType,
			MediaID:   ref.MediaID,
			Language:  ref.Language,
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

// removeSubtitleFile deletes path through the S16 subtitle delete gate,
// writing the response on failure. A refused extension answers 409
// subtitle_extension_not_allowed + WARN (loud, never a silent skip); a
// containment failure answers 403. Returns true when the file is gone.
func (h *Handler) removeSubtitleFile(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) bool {
	ls := h.deps.StateFunc()
	if err := subtitlepath.RemoveUnderRoot(ctx, ls.Cfg, path); err != nil {
		switch {
		case errors.Is(err, subtitlepath.ErrSubtitleExtensionNotAllowed):
			slog.Warn("delete file: extension refused by subtitle delete gate", "path", path, "error", err)
			api.ConflictC(w, r, api.CodeSubtitleExtensionNotAllowed,
				"stored path does not carry a deletable subtitle extension")
		case errors.Is(err, config.ErrPathNotAllowed):
			slog.Warn("delete file: path rejected", "path", path, "error", err)
			api.ForbiddenC(w, r, api.CodePathNotAllowed, "invalid path")
		default:
			api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "delete file", "path", path)
		}
		return false
	}
	return true
}

// BulkDeleteRequest is the typed body for DELETE /api/files/bulk: the media
// item whose external subtitle files are all deleted. MediaID keeps the
// store-ID semantics of the listing (exact ID, or a "tvdb-<id>-" series
// prefix covering every episode).
type BulkDeleteRequest struct {
	MediaType api.MediaType `json:"media_type"`
	MediaID   string        `json:"media_id"`
}

// HandleBulkDeleteFiles deletes all external subtitle files for a media item.
// DELETE /api/files/bulk
func (h *Handler) HandleBulkDeleteFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodDelete {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	var req BulkDeleteRequest
	if !httphelpers.DecodeJSONBody(w, r, &req, 1<<20) {
		return
	}
	if req.MediaType == "" || req.MediaID == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "media_type and media_id required")
		return
	}
	if !req.MediaType.Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid media_type")
		return
	}

	ls := h.deps.StateFunc()

	rows, err := h.deps.Store.GetSubtitleFiles(ctx, req.MediaType, req.MediaID)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "bulk delete: db fetch")
		return
	}

	// S16 preflight: every targeted external row must carry the delete
	// capability BEFORE any mutation. One conflicting stored path refuses
	// the whole bulk with the single-file path's 409 and deletes nothing —
	// never a silent skip that answers 200 with the row left behind.
	if path, refused := refusedBulkPath(rows); refused {
		slog.Warn("bulk delete: extension refused by subtitle delete gate, refusing whole bulk",
			"path", path)
		api.ConflictC(w, r, api.CodeSubtitleExtensionNotAllowed,
			"a stored path does not carry a deletable subtitle extension")
		return
	}

	deleted := 0
	affected := make(map[lockQuad]bool)
	for i := range rows {
		row := &rows[i]
		if h.deleteExternalFile(ctx, ls.Cfg, req.MediaType, row) {
			affected[lockQuad{mediaID: row.MediaID, language: row.Language, variant: api.Variant(row.Variant)}] = true
			deleted++
		}
	}

	for q := range affected {
		h.maybeRevertManualLock(ctx, req.MediaType, q.mediaID, q.language, q.variant)
	}

	slog.Info("bulk delete completed",
		"media_type", req.MediaType,
		"media_id", req.MediaID,
		"examined", len(rows),
		"deleted", deleted)

	h.deps.Events.Publish(events.Event{
		Type: events.CoverageUpdate,
		Data: events.CoverageEvent{
			MediaType: req.MediaType,
			MediaID:   req.MediaID,
		},
	})

	api.WriteJSON(w, map[string]int{"deleted": deleted})
}

// refusedBulkPath returns the first targeted external row path whose
// extension lacks the delete capability in the subtitle-extension authority
// (the S16 bulk preflight). Embedded rows and rows without a path are not
// deletion targets and pass through.
func refusedBulkPath(rows []api.SubtitleEntry) (string, bool) {
	for i := range rows {
		row := &rows[i]
		if row.Source == string(api.SourceEmbedded) || row.Path == "" {
			continue
		}
		if !subtitleext.Delete(row.Path) {
			return row.Path, true
		}
	}
	return "", false
}

// HandleHistoryIDs returns distinct media IDs that have download history.
// GET /api/state/ids?type=movie&prefix=tmdb-1271
func (h *Handler) HandleHistoryIDs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	mediaType := r.URL.Query().Get("type")
	prefix := r.URL.Query().Get("prefix")
	if mediaType == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "type required")
		return
	}
	if mediaType != string(api.MediaTypeEpisode) && mediaType != string(api.MediaTypeMovie) {
		api.BadRequestC(w, r, api.CodeQueryInvalidFilter, "invalid type parameter")
		return
	}
	if prefix != "" && !api.IsValidMediaPrefix(prefix) {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid prefix format")
		return
	}
	ids, err := h.deps.Store.HistoryMediaIDs(ctx, api.MediaType(mediaType), prefix)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "history ids")
		return
	}
	if ids == nil {
		ids = []string{}
	}
	api.WriteJSON(w, ids)
}

// DeleteExternalFile removes a single external subtitle file from disk and DB.
func (h *Handler) DeleteExternalFile(ctx context.Context, cfg api.ConfigProvider, mediaType api.MediaType, row *api.SubtitleEntry) bool {
	return h.deleteExternalFile(ctx, cfg, mediaType, row)
}

// deleteExternalFile removes a single external subtitle file from disk and
// DB through the S16 subtitle delete gate. The bulk handler preflights every
// targeted row's extension and answers 409 before any mutation, so the
// in-sweep refusal branch here is defense-in-depth (it also guards the
// exported DeleteExternalFile): a refused extension is still loud (WARN log
// naming the gate) and the row is left untouched.
func (h *Handler) deleteExternalFile(ctx context.Context, cfg api.ConfigProvider, mediaType api.MediaType, row *api.SubtitleEntry) bool {
	if row.Source == string(api.SourceEmbedded) || row.Path == "" {
		return false
	}
	if err := subtitlepath.RemoveUnderRoot(ctx, cfg, row.Path); err != nil {
		if errors.Is(err, subtitlepath.ErrSubtitleExtensionNotAllowed) {
			slog.Warn("bulk delete: extension refused by subtitle delete gate",
				"path", row.Path, "error", err)
		} else {
			slog.Warn("bulk delete: failed to remove file",
				"path", row.Path, "error", err)
		}
		return false
	}
	slog.Info("subtitle file deleted (bulk)", "path", row.Path)
	if err := h.deps.Store.DeleteSubtitleFile(ctx,
		mediaType, row.MediaID, row.Language,
		api.Variant(row.Variant), api.SourceExternal, row.Path); err != nil {
		slog.Warn("bulk delete: db cleanup failed", "error", err)
	}
	return true
}

// lockQuad identifies a (media_id, language, variant) whose manual lock may
// need reverting after its files were deleted. Locks live per variant, so the
// bulk-delete revert sweep is keyed by the full quad, not just the language.
type lockQuad struct {
	mediaID  string
	language string
	variant  api.Variant
}

// maybeRevertManualLock checks if there are any remaining manual subtitle
// files on disk for a media+language+variant quad. If none remain, clears
// that quad's manual lock (sibling variants keep theirs).
func (h *Handler) maybeRevertManualLock(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) {
	paths, err := h.deps.Store.ManualSubtitlePaths(ctx, mediaType, mediaID, language, variant)
	if err != nil {
		slog.Warn("maybeRevertManualLock: failed to get paths", "error", err)
		return
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	if err := h.deps.Store.ClearManualLock(ctx, mediaType, mediaID, language, variant); err != nil {
		slog.Warn("failed to clear manual lock after delete",
			"media_id", mediaID, "lang", language, "variant", variant, "error", err)
	} else if len(paths) > 0 {
		slog.Info("manual lock cleared (no manual files remain on disk)",
			"media_id", mediaID, "lang", language, "variant", variant)
	}
}
