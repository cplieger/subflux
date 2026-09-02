package manualops

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/langcode"
	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search/release"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/subflux"
)

// HandlerDeps holds the dependencies for the manual search/download HTTP
// handlers. Resolve is the typed-reference resolver: the download verb and
// the manual-search hash computation address the video by MediaRef and the
// server resolves the file path from the arr — no client-supplied paths.
type HandlerDeps struct {
	DBFunc     func() DownloadStore
	Activity   ActivityTracker
	Alerts     WarnRecorder
	Events     EventPublisher
	StateFunc  func() *LiveState
	BGTracker  BGTracker
	ServerCtx  func() context.Context
	Resolve    *resolve.Resolver
	DecodeJSON func(w http.ResponseWriter, r *http.Request, v any, maxSize int64) bool
}

// BGTracker registers a background goroutine with the server's WaitGroup
// for graceful-shutdown tracking. One method, mirroring sync.WaitGroup.Go
// (launch and count in one call), so an Add/Done pair can never leak a
// counter via an early return or panic before the defer is installed.
type BGTracker interface {
	Go(f func())
}

// Handler serves the manual search/download HTTP endpoints.
type Handler struct {
	deps HandlerDeps
}

// NewHandler constructs a Handler from its dependencies.
func NewHandler(deps HandlerDeps) *Handler { //nolint:gocritic // hugeParam: callers pass by value
	return &Handler{deps: deps}
}

// HandleManualSearch handles GET /api/search?imdb=tt1234567&lang=fr&type=movie
func (h *Handler) HandleManualSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}

	ls := h.deps.StateFunc()
	req, lang, mediaType, arrID := ParseSearchQuery(r)

	if !langcode.Valid(lang) {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid language code")
		return
	}

	if !mediaType.Valid() {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid media_type")
		return
	}

	// The parser clamps internally (defense in depth), but a diagnostic
	// request must never be silently truncated: reject oversized names
	// loudly at the HTTP boundary instead.
	if len(req.ReleaseName) > release.MaxNameLen {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest,
			"release exceeds "+strconv.Itoa(release.MaxNameLen)+" bytes")
		return
	}

	slog.Info("manual search requested",
		"title", logsafe.Field(req.Title), "imdb", logsafe.Field(req.ImdbID),
		"lang", lang, "type", mediaType,
		"season", req.Season, "episode", req.Episode)

	actID := h.deps.Activity.Start("Manual Search",
		fmt.Sprintf("%s %s", req.Title, req.ImdbID), activity.SourceManual)
	defer h.deps.Activity.End(actID)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Best-effort: the path only feeds hash computation and the release-name
	// default, so an unresolvable video degrades the search rather than
	// failing it.
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
	httpapi.WriteJSON(w, result)
}

// resolveSearchVideo resolves the arr-known video path for a manual
// search's hash computation. Returns "" when no arr reference was supplied
// or the item cannot be resolved; the search then proceeds hash-less.
func (h *Handler) resolveSearchVideo(ctx context.Context, mediaType subflux.MediaType, arrID, season, episode int) string {
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
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}

	// The request body IS the lock key: its four json names are the wire
	// shape, and subflux.ManualLockKey carries exactly those.
	var key subflux.ManualLockKey
	if !h.deps.DecodeJSON(w, r, &key, 0) {
		return
	}

	if key.MediaType == "" || key.MediaID == "" || key.Language == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "media_type, media_id, and language are required")
		return
	}

	if !langcode.Valid(key.Language) {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid language code")
		return
	}

	if !key.MediaType.Valid() {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid media_type")
		return
	}

	if !isValidLockVariant(key.Variant) {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid variant (want standard, hi, or forced)")
		return
	}

	if err := h.deps.DBFunc().ClearManualLock(ctx, key); err != nil {
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "stage", "clear manual lock",
			"media_type", key.MediaType, "media_id", key.MediaID, "lang", key.Language,
			"variant", key.Variant)
		return
	}

	slog.Info("manual lock cleared",
		"media_type", key.MediaType, "media_id", key.MediaID, "lang", key.Language,
		"variant", key.Variant)

	h.deps.Events.PublishCoverageUpdate(&events.CoverageEvent{
		MediaType: key.MediaType, MediaID: key.MediaID, Language: key.Language,
	})

	httpapi.WriteJSON(w, map[string]string{"status": "lock cleared"})
}

// HandleManualDownload handles POST /api/search/download.
func (h *Handler) HandleManualDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}

	var req DownloadRequest
	if !h.deps.DecodeJSON(w, r, &req, 0) {
		return
	}

	if err := ValidateDownloadRequest(&req); err != nil {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, err.Error())
		return
	}

	ls := h.deps.StateFunc()

	var prov provider.Provider
	for _, p := range ls.Providers {
		if p.Name() == req.Provider {
			prov = p
			break
		}
	}
	if prov == nil {
		httpapi.BadRequestC(w, r, subflux.CodeSearchProviderDisabled, "provider not found")
		return
	}

	// Resolved before going async, so an unknown item answers a synchronous
	// 404 with a machine code instead of a failed background activity.
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

	httpapi.WriteJSONStatus(w, http.StatusAccepted, DownloadAccepted{
		ActivityID: actID,
		Status:     "accepted",
	})

	h.deps.BGTracker.Go(func() {
		h.runManualDownload(ls, prov, &req, actID)
	})
}

// DownloadAccepted is the 202 response body for an accepted manual download.
type DownloadAccepted struct {
	ActivityID string `json:"activity_id"`
	Status     string `json:"status"`
}

func (h *Handler) runManualDownload(ls *LiveState, prov provider.Provider,
	req *DownloadRequest, actID string,
) {
	serverCtx := h.deps.ServerCtx()
	ctx, cancel := context.WithTimeout(serverCtx, DownloadTimeout)
	defer cancel()

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
