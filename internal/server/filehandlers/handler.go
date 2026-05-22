// Package filehandlers provides HTTP handlers for the /api/files/* endpoints.
package filehandlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"subflux/internal/api"
	"subflux/internal/config"
	"subflux/internal/server/events"
	"subflux/internal/server/httphelpers"
)

// FileStore is the narrow store interface used by file handlers.
type FileStore interface {
	GetSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.SubtitleFileRow, error)
	DeleteSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant, source api.SubtitleSource, path string) error
	ManualSubtitlePaths(ctx context.Context, mediaType api.MediaType, mediaID, language string) ([]string, error)
	ClearManualLock(ctx context.Context, mediaType api.MediaType, mediaID, language string) error
	HistoryMediaIDs(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]string, error)
}

// Compile-time assertion: api.Store satisfies FileStore.
var _ FileStore = api.Store(nil)

// Deps holds the dependencies for file handlers.
type Deps struct {
	Store     FileStore
	StateFunc func() *LiveState
	Events    EventPublisher
}

// LiveState holds the runtime state needed by file handlers.
type LiveState struct {
	Cfg api.ConfigProvider
}

// EventPublisher is the narrow interface for publishing events.
type EventPublisher interface {
	Publish(e events.Event)
}

// Handler provides HTTP handlers for the /api/files/* endpoints.
type Handler struct {
	deps Deps
}

// NewHandler creates a file Handler with the given dependencies.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// FileEntry is the JSON shape for the file manager API.
type FileEntry struct {
	MediaID   string `json:"media_id"`
	Language  string `json:"language"`
	Variant   string `json:"variant"`
	Source    string `json:"source"`
	Codec     string `json:"codec,omitempty"`
	Path      string `json:"path,omitempty"`
	VideoPath string `json:"video_path,omitempty"`
	Score     int    `json:"score,omitempty"`
	OffsetMs  int64  `json:"offset_ms,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

// HandleListFiles returns subtitle files for a media item with file sizes.
// GET /api/files?media_type=movie&media_id=tmdb-1271
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

	rows, err := h.deps.Store.GetSubtitleFiles(ctx, api.MediaType(mediaType), mediaID)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "list files")
		return
	}

	entries := make([]FileEntry, 0, len(rows))
	ls := h.deps.StateFunc()
	for i := range rows {
		row := &rows[i]
		e := FileEntry{
			MediaID:   row.MediaID,
			Language:  row.Language,
			Variant:   row.Variant,
			Source:    row.Source,
			Codec:     row.Codec,
			Path:      row.Path,
			VideoPath: row.VideoPath,
			Score:     row.Score,
			OffsetMs:  row.OffsetMs,
		}
		if row.Path != "" {
			if err := ls.Cfg.ValidatePath(ctx, row.Path); err == nil {
				if fi, statErr := os.Stat(row.Path); statErr == nil {
					e.Size = fi.Size()
				}
			}
		}
		entries = append(entries, e)
	}

	api.WriteJSON(w, entries)
}

// HandleDeleteFile deletes a single external subtitle file.
// DELETE /api/files?path=/media/...srt&media_type=movie&media_id=tmdb-1271&language=en&variant=standard
func (h *Handler) HandleDeleteFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodDelete {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	path := r.URL.Query().Get("path")
	mediaType := r.URL.Query().Get("media_type")
	mediaID := r.URL.Query().Get("media_id")
	language := r.URL.Query().Get("language")
	variant := r.URL.Query().Get("variant")

	if path == "" || mediaType == "" || mediaID == "" || language == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "path, media_type, media_id, and language required")
		return
	}
	if !api.MediaType(mediaType).Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid media_type")
		return
	}
	if variant == "" {
		variant = string(api.VariantStandard)
	}

	ls := h.deps.StateFunc()

	if err := ls.Cfg.RemoveUnderRoot(ctx, path); err != nil {
		if errors.Is(err, config.ErrPathNotAllowed) {
			slog.Warn("delete file: path rejected", "path", path, "error", err)
			api.ForbiddenC(w, r, api.CodePathNotAllowed, "invalid path")
			return
		}
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "delete file", "path", path)
		return
	}

	slog.Info("subtitle file deleted", "path", path)

	if err := h.deps.Store.DeleteSubtitleFile(ctx,
		api.MediaType(mediaType), mediaID, language, api.Variant(variant), api.SourceExternal, path); err != nil {
		slog.Warn("delete file: db cleanup failed", "error", err)
	}

	h.maybeRevertManualLock(ctx, api.MediaType(mediaType), mediaID, language)

	h.deps.Events.Publish(events.Event{
		Type: events.CoverageUpdate,
		Data: events.CoverageEvent{
			MediaType: api.MediaType(mediaType),
			MediaID:   mediaID,
			Language:  language,
		},
	})

	w.WriteHeader(http.StatusNoContent)
}

// HandleBulkDeleteFiles deletes all external subtitle files for a media item.
// DELETE /api/files/bulk
func (h *Handler) HandleBulkDeleteFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodDelete {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	var req struct {
		MediaType api.MediaType `json:"media_type"`
		MediaID   string        `json:"media_id"`
	}
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

	deleted := 0
	langs := make(map[string]bool)
	for i := range rows {
		row := &rows[i]
		if h.deleteExternalFile(ctx, ls.Cfg, req.MediaType, row) {
			langs[row.MediaID+"|"+row.Language] = true
			deleted++
		}
	}

	for key := range langs {
		if i := strings.LastIndexByte(key, '|'); i >= 0 {
			h.maybeRevertManualLock(ctx, req.MediaType, key[:i], key[i+1:])
		}
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
func (h *Handler) DeleteExternalFile(ctx context.Context, cfg api.ConfigProvider, mediaType api.MediaType, row *api.SubtitleFileRow) bool {
	return h.deleteExternalFile(ctx, cfg, mediaType, row)
}

// deleteExternalFile removes a single external subtitle file from disk and DB.
func (h *Handler) deleteExternalFile(ctx context.Context, cfg api.ConfigProvider, mediaType api.MediaType, row *api.SubtitleFileRow) bool {
	if row.Source == string(api.ProviderNameEmbedded) || row.Path == "" {
		return false
	}
	if err := cfg.RemoveUnderRoot(ctx, row.Path); err != nil {
		slog.Warn("bulk delete: failed to remove file",
			"path", row.Path, "error", err)
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

// maybeRevertManualLock checks if there are any remaining manual subtitle
// files on disk for a media+language. If none remain, clears the manual lock.
func (h *Handler) maybeRevertManualLock(ctx context.Context, mediaType api.MediaType, mediaID, language string) {
	paths, err := h.deps.Store.ManualSubtitlePaths(ctx, mediaType, mediaID, language)
	if err != nil {
		slog.Warn("maybeRevertManualLock: failed to get paths", "error", err)
		return
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	if err := h.deps.Store.ClearManualLock(ctx, mediaType, mediaID, language); err != nil {
		slog.Warn("failed to clear manual lock after delete",
			"media_id", mediaID, "lang", language, "error", err)
	} else if len(paths) > 0 {
		slog.Info("manual lock cleared (no manual files remain on disk)",
			"media_id", mediaID, "lang", language)
	}
}
