package manualops

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search/release"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/resolve"
)

// HandlerDeps holds the dependencies for the manual search/download HTTP
// handlers. Resolve is the S7 typed-reference resolver: the download verb
// and the manual-search hash computation address the video by MediaRef and
// the server resolves the file path from the arr — no client-supplied paths.
type HandlerDeps struct {
	DBFunc     func() DownloadStore
	Activity   ActivityTracker
	Alerts     activity.WarnRecorder
	Events     EventPublisher
	StateFunc  func() *LiveState
	BGTracker  BGTracker
	ServerCtx  func() context.Context
	Resolve    *resolve.Resolver
	DecodeJSON func(w http.ResponseWriter, r *http.Request, v any, maxSize int64) bool
}

// BGTracker allows the handler to register background goroutines for
// graceful shutdown tracking.
type BGTracker interface {
	Add(delta int)
	Done()
}

// Handler provides HTTP handlers for manual search and download endpoints.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a manual ops Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler { //nolint:gocritic // hugeParam: callers pass by value
	return &Handler{deps: deps}
}

// HandleManualSearch handles GET /api/search?imdb=tt1234567&lang=fr&type=movie
func (h *Handler) HandleManualSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	ls := h.deps.StateFunc()
	req, lang, mediaType, arrID := ParseSearchQuery(r)

	if !IsValidLangCode(lang) {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid language code")
		return
	}

	if !mediaType.Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid media_type")
		return
	}

	// The release parameter is direct user input into the release parser.
	// The parser clamps internally (defense in depth), but a diagnostic
	// request must never be silently truncated: reject oversized names
	// loudly at the HTTP boundary instead.
	if len(req.ReleaseName) > release.MaxNameLen {
		api.BadRequestC(w, r, api.CodeBadRequest,
			"release exceeds "+strconv.Itoa(release.MaxNameLen)+" bytes")
		return
	}

	slog.Info("manual search requested",
		"title", req.Title, "imdb", req.ImdbID,
		"lang", lang, "type", mediaType,
		"season", req.Season, "episode", req.Episode)

	actID := h.deps.Activity.Start("Manual Search",
		fmt.Sprintf("%s %s", req.Title, req.ImdbID), activity.SourceManual)
	defer h.deps.Activity.End(actID)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Resolve the video path server-side from the optional MediaRef
	// (media_id = arr ID). Best-effort: the path only feeds hash computation
	// and the release-name default, so an unresolvable video degrades the
	// search rather than failing it (matching the previous no-file case).
	filePath := h.resolveSearchVideo(ctx, mediaType, arrID, req.Season, req.Episode)
	if filePath != "" && req.ReleaseName == "" {
		req.ReleaseName = filePath
	}

	deps := &SearchDeps{
		DB:       h.deps.DBFunc(),
		Activity: h.deps.Activity,
		Alerts:   h.deps.Alerts,
		Events:   h.deps.Events,
	}

	result := RunSearch(ctx, deps, ls, &req, lang, mediaType, filePath)
	api.WriteJSON(w, result)
}

// resolveSearchVideo resolves the arr-known video path for a manual search's
// hash computation. Returns "" when no arr reference was supplied or the
// item cannot be resolved (logged at debug; the search proceeds hash-less).
func (h *Handler) resolveSearchVideo(ctx context.Context, mediaType api.MediaType, arrID, season, episode int) string {
	if arrID <= 0 {
		return ""
	}
	ref := &resolve.MediaRef{MediaType: mediaType, MediaID: arrID, Season: season, Episode: episode}
	path, err := h.deps.Resolve.VideoPath(ctx, ref)
	if err != nil {
		slog.Debug("manual search: video path resolution failed, searching without hash",
			"media_type", mediaType, "arr_id", arrID, "season", season, "episode", episode,
			"error", err)
		return ""
	}
	return path
}

// HandleClearLock handles POST /api/search/clear-lock.
func (h *Handler) HandleClearLock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	var req struct {
		MediaType api.MediaType `json:"media_type"`
		MediaID   string        `json:"media_id"`
		Language  string        `json:"language"`
		Variant   api.Variant   `json:"variant,omitempty"`
	}
	if !h.deps.DecodeJSON(w, r, &req, 0) {
		return
	}

	if req.MediaType == "" || req.MediaID == "" || req.Language == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "media_type, media_id, and language are required")
		return
	}

	if !IsValidLangCode(req.Language) {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid language code")
		return
	}

	if !req.MediaType.Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid media_type")
		return
	}

	// Variant is optional: empty clears the locks of every variant of the
	// language; a specific variant clears only that quad's lock.
	if !isValidLockVariant(req.Variant) {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid variant (want standard, hi, or forced)")
		return
	}

	deps := &SearchDeps{
		DB:       h.deps.DBFunc(),
		Activity: h.deps.Activity,
		Alerts:   h.deps.Alerts,
		Events:   h.deps.Events,
	}

	if err := RunClearLock(ctx, deps, string(req.MediaType), req.MediaID, req.Language, req.Variant); err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "clear manual lock",
			"media_type", req.MediaType, "media_id", req.MediaID, "lang", req.Language,
			"variant", req.Variant)
		return
	}

	slog.Info("manual lock cleared",
		"media_type", req.MediaType, "media_id", req.MediaID, "lang", req.Language,
		"variant", req.Variant)

	h.deps.Events.PublishCoverageUpdate(req.MediaType, req.MediaID, req.Language, "")

	api.WriteJSON(w, map[string]string{"status": "lock cleared"})
}

// HandleManualDownload handles POST /api/search/download.
func (h *Handler) HandleManualDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	var req DownloadRequest
	if !h.deps.DecodeJSON(w, r, &req, 0) {
		return
	}

	if err := ValidateDownloadRequest(&req); err != nil {
		api.BadRequestC(w, r, api.CodeBadRequest, err.Error())
		return
	}

	ls := h.deps.StateFunc()

	// Find the provider first (free check) so we can return 400 immediately.
	var prov api.Provider
	for _, p := range ls.Providers {
		if p.Name() == req.Provider {
			prov = p
			break
		}
	}
	if prov == nil {
		api.BadRequestC(w, r, api.CodeSearchProviderDisabled, "provider not found")
		return
	}

	// Resolve the video path server-side from the MediaRef before going
	// async, so an unknown item answers a synchronous 404 with a machine
	// code instead of a failed background activity.
	mref := resolve.MediaRef{
		MediaType: req.MediaType, MediaID: req.ArrID,
		Season: req.Season, Episode: req.Episode,
	}
	videoPath, err := h.deps.Resolve.VideoPath(r.Context(), &mref)
	if err != nil {
		resolve.WriteError(w, r, err)
		return
	}
	req.SetVideoPath(videoPath)

	slog.Info("manual download requested",
		"provider", req.Provider, "subtitle_id", req.SubtitleID,
		"file", videoPath, "lang", req.Language)

	actID := h.deps.Activity.Start("Manual Download",
		fmt.Sprintf("%s %s", req.Provider, req.SubtitleID), activity.SourceManual)

	// Return 202 immediately; run the download in the background.
	api.WriteJSONStatus(w, http.StatusAccepted, DownloadAccepted{
		ActivityID: actID,
		Status:     "accepted",
	})

	h.deps.BGTracker.Add(1)
	go func() {
		defer h.deps.BGTracker.Done()
		h.runManualDownload(ls, prov, &req, actID)
	}()
}

// DownloadAccepted is the typed 202 Accepted response for manual downloads.
type DownloadAccepted struct {
	ActivityID string `json:"activity_id"`
	Status     string `json:"status"`
}

// runManualDownload performs the actual download in the background.
func (h *Handler) runManualDownload(ls *LiveState, prov api.Provider,
	req *DownloadRequest, actID string,
) {
	serverCtx := h.deps.ServerCtx()
	ctx, cancel := context.WithTimeout(serverCtx, DownloadTimeout)
	defer cancel()

	// Log if the parent context was cancelled (server shutdown).
	defer func() {
		if ctx.Err() != nil && serverCtx.Err() != nil {
			slog.Warn("manual download interrupted by shutdown",
				"provider", req.Provider, "subtitle_id", req.SubtitleID)
		}
	}()

	deps := &SearchDeps{
		DB:       h.deps.DBFunc(),
		Activity: h.deps.Activity,
		Alerts:   h.deps.Alerts,
		Events:   h.deps.Events,
	}

	success := RunDownload(ctx, deps, ls, h.deps.DBFunc(), prov, req, actID)
	if success {
		h.deps.Activity.End(actID)
	} else {
		h.deps.Activity.Fail(actID)
	}
}
