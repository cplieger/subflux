package manualops

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// HandlerDeps holds the dependencies for the manual search/download HTTP handlers.
type HandlerDeps struct {
	DBFunc       func() DownloadStore
	Activity     ActivityTracker
	Alerts       activity.WarnRecorder
	Events       EventPublisher
	StateFunc    func() *LiveState
	BGTracker    BGTracker
	ServerCtx    func() context.Context
	ValidatePath func(w http.ResponseWriter, r *http.Request, path, label string) bool
	DecodeJSON   func(w http.ResponseWriter, r *http.Request, v any, maxSize int64) bool
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
	req, lang, mediaType, filePath := ParseSearchQuery(r)

	if !IsValidLangCode(lang) {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid language code")
		return
	}

	if !mediaType.Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid media_type")
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

	deps := &SearchDeps{
		DB:       h.deps.DBFunc(),
		Activity: h.deps.Activity,
		Alerts:   h.deps.Alerts,
		Events:   h.deps.Events,
	}

	result := RunSearch(ctx, deps, ls, &req, lang, mediaType, filePath)
	api.WriteJSON(w, result)
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

	deps := &SearchDeps{
		DB:       h.deps.DBFunc(),
		Activity: h.deps.Activity,
		Alerts:   h.deps.Alerts,
		Events:   h.deps.Events,
	}

	if err := RunClearLock(ctx, deps, string(req.MediaType), req.MediaID, req.Language); err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "stage", "clear manual lock",
			"media_type", req.MediaType, "media_id", req.MediaID, "lang", req.Language)
		return
	}

	slog.Info("manual lock cleared",
		"media_type", req.MediaType, "media_id", req.MediaID, "lang", req.Language)

	h.deps.Events.PublishCoverageUpdate(req.MediaType, req.MediaID, req.Language, "", "")

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

	if !h.deps.ValidatePath(w, r, req.FilePath, "file path") {
		return
	}

	// Find the provider before going async so we can return 400 immediately.
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

	slog.Info("manual download requested",
		"provider", req.Provider, "subtitle_id", req.SubtitleID,
		"file", req.FilePath, "lang", req.Language)

	actID := h.deps.Activity.Start("Manual Download",
		fmt.Sprintf("%s %s", req.Provider, req.SubtitleID), activity.SourceManual)

	// Return 202 immediately; run the download in the background.
	api.WriteJSONStatus(w, http.StatusAccepted, downloadAcceptedResponse{
		ActivityID: actID,
		Status:     "accepted",
	})

	h.deps.BGTracker.Add(1)
	go func() {
		defer h.deps.BGTracker.Done()
		h.runManualDownload(ls, prov, &req, actID)
	}()
}

// downloadAcceptedResponse is the typed 202 Accepted response for manual downloads.
type downloadAcceptedResponse struct {
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

	success := RunDownload(ctx, deps, ls, h.deps.DBFunc(), prov, req)
	if success {
		h.deps.Activity.End(actID)
	} else {
		h.deps.Activity.Fail(actID)
	}
}
