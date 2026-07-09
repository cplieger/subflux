package scanning

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/httphelpers"
)

// ScanGuard serializes manual scan requests. Extracted from activity.Log
// to separate coordination concerns from data concerns.
type ScanGuard struct {
	mu sync.Mutex
}

// Lock acquires the scan serialization lock.
func (g *ScanGuard) Lock() { g.mu.Lock() }

// Unlock releases the scan serialization lock.
func (g *ScanGuard) Unlock() { g.mu.Unlock() }

// ScanHandlerSonarr is the Sonarr surface the manual scan HTTP handlers call.
type ScanHandlerSonarr interface {
	GetSeriesByID(ctx context.Context, id int) (arrapi.Series, error)
	GetEpisodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error)
}

// ScanHandlerRadarr is the Radarr surface the manual scan HTTP handlers call.
type ScanHandlerRadarr interface {
	GetMovieByID(ctx context.Context, id int) (arrapi.Movie, error)
}

// Compile-time assertions: the arrapi-backed role clients satisfy the handler
// surfaces.
var (
	_ ScanHandlerSonarr = api.SonarrClient(nil)
	_ ScanHandlerRadarr = api.RadarrClient(nil)
)

// HandlerState holds the runtime state needed by scan HTTP handlers.
// Provided by the server on each request via the StateFunc callback.
type HandlerState struct {
	Cfg    api.ConfigProvider
	Engine api.SearchEngine
	Sonarr ScanHandlerSonarr // nil when sonarr not configured
	Radarr ScanHandlerRadarr // nil when radarr not configured
}

// HandlerDeps holds the dependencies for the scan HTTP handler family.
type HandlerDeps struct {
	// StateFunc returns the current live state snapshot.
	StateFunc func() *HandlerState
	// CtxFunc returns the server-level context (outlives individual requests).
	CtxFunc func() context.Context
	// ScanDeps provides the scanning engine dependencies.
	ScanDeps func() *Deps
	// ScanLiveStateFunc converts HandlerState to scanning.LiveState.
	ScanLiveStateFunc func() *LiveState
	// Activity tracks scan activity lifecycle.
	Activity ActivityTracker
	// ScanGuard serializes manual scan requests.
	ScanGuard *ScanGuard
	// Alerts records alerts visible in the UI.
	Alerts AlertRecorder
	// Events publishes SSE events.
	Events EventPublisher
	// InvalidateStats clears the stats cache after scan completion.
	InvalidateStats func()
	// BGTracker tracks background goroutine lifecycle for graceful shutdown.
	BGTracker BGTracker
}

// BGTracker allows the scanning handler to register background goroutines
// with the server's WaitGroup for graceful shutdown.
type BGTracker interface {
	Add(delta int)
	Done()
}

// Handler provides HTTP handlers for the /api/scan/* endpoints.
type Handler struct {
	deps HandlerDeps
}

// NewHandler creates a scan Handler with the given dependencies.
func NewHandler(deps HandlerDeps) *Handler { //nolint:gocritic // hugeParam: callers pass by value
	return &Handler{deps: deps}
}

// scanItemRequest is the JSON body for POST /api/scan/item.
type scanItemRequest struct {
	MediaType api.MediaType `json:"media_type"` // "episode" or "movie"
	MediaID   int           `json:"media_id"`   // Sonarr series ID or Radarr movie ID
	Season    int           `json:"season"`     // episode only
	Episode   int           `json:"episode"`    // episode only
}

// scanFoundResponse is the JSON response for scan endpoints that report
// only the number of subtitles found.
type scanFoundResponse struct {
	Found int `json:"found"`
}

// scanFullResponse is the JSON response for scan endpoints that report
// both found and searched counts.
type scanFullResponse struct {
	Found    int `json:"found"`
	Searched int `json:"searched"`
}

// extendScanDeadline clears the HTTP write deadline for a scan request so
// the 60-second server-level WriteTimeout doesn't kill long scans before
// they can respond.
func extendScanDeadline(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Debug("scan: failed to clear write deadline", "error", err)
	}
}

// HandleScanSeries scans all missing subtitles for a specific series.
// POST /api/scan/series/{sonarrId}
func (h *Handler) HandleScanSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	idStr := extractSegment(r.URL.Path, "/api/scan/series/")
	if idStr == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "missing series id")
		return
	}
	seriesID, err := strconv.Atoi(idStr)
	if err != nil || seriesID <= 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid series id")
		return
	}

	st := h.deps.StateFunc()
	if st.Sonarr == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgSonarrNotConfigured)
		return
	}

	slog.Info("manual series scan requested", "series_id", seriesID)

	extendScanDeadline(w)
	found := h.scanSeries(h.deps.CtxFunc(), seriesID)
	api.WriteJSON(w, scanFoundResponse{Found: found})
}

// HandleScanSeason scans all missing subtitles for a specific season.
// POST /api/scan/season/{sonarrId}/{seasonNum}
func (h *Handler) HandleScanSeason(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/scan/season/")
	seriesStr, seasonStr, ok := strings.Cut(rest, "/")
	if !ok {
		api.BadRequestC(w, r, api.CodeBadRequest, "expected /api/scan/season/{seriesId}/{season}")
		return
	}
	seriesID, err := strconv.Atoi(seriesStr)
	if err != nil || seriesID <= 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid series id")
		return
	}
	seasonNum, err := strconv.Atoi(seasonStr)
	if err != nil || seasonNum < 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid season number")
		return
	}

	st := h.deps.StateFunc()
	if st.Sonarr == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgSonarrNotConfigured)
		return
	}

	slog.Info("manual season scan requested",
		"series_id", seriesID, "season", seasonNum)

	extendScanDeadline(w)
	found := h.scanSeason(h.deps.CtxFunc(), seriesID, seasonNum)
	api.WriteJSON(w, scanFoundResponse{Found: found})
}

// HandleScanItem scans a single episode or movie.
// POST /api/scan/item with JSON body.
func (h *Handler) HandleScanItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	var req scanItemRequest
	if !httphelpers.DecodeJSONBody(w, r, &req, 0) {
		return
	}
	if req.MediaID <= 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "media_id required")
		return
	}
	if req.MediaType == "" {
		req.MediaType = api.MediaTypeEpisode
	}
	if !req.MediaType.Valid() {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid media_type")
		return
	}

	slog.Info("manual item scan requested",
		"media_type", req.MediaType, "media_id", req.MediaID)

	st := h.deps.StateFunc()
	if req.MediaType == api.MediaTypeMovie {
		if st.Radarr == nil {
			api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgRadarrNotConfigured)
			return
		}
		h.deps.BGTracker.Add(1)
		go func() {
			defer h.deps.BGTracker.Done()
			h.scanSingleMovie(h.deps.CtxFunc(), req.MediaID)
		}()
	} else {
		if st.Sonarr == nil {
			api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgSonarrNotConfigured)
			return
		}
		h.deps.BGTracker.Add(1)
		go func() {
			defer h.deps.BGTracker.Done()
			h.scanSingleEpisode(h.deps.CtxFunc(), req.MediaID,
				req.Season, req.Episode)
		}()
	}
	api.WriteJSONStatus(w, http.StatusAccepted, map[string]string{api.KeyStatus: "item scan started"})
}

// HandleScanMovie scans all missing subtitles for a specific movie.
// POST /api/scan/movie/{radarrId}
func (h *Handler) HandleScanMovie(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	idStr := extractSegment(r.URL.Path, "/api/scan/movie/")
	if idStr == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "missing movie id")
		return
	}
	movieID, err := strconv.Atoi(idStr)
	if err != nil || movieID <= 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid movie id")
		return
	}

	st := h.deps.StateFunc()
	if st.Radarr == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgRadarrNotConfigured)
		return
	}

	slog.Info("manual movie scan requested", "movie_id", movieID)

	extendScanDeadline(w)
	found, searched := h.scanMovieSync(h.deps.CtxFunc(), movieID)
	api.WriteJSON(w, scanFullResponse{Found: found, Searched: searched})
}

// extractSegment extracts the path segment after a prefix.
func extractSegment(path, prefix string) string {
	s := strings.TrimPrefix(path, prefix)
	if s == "" || s == path {
		return ""
	}
	// Reject trailing slashes or sub-paths for single-segment endpoints.
	if strings.Contains(s, "/") {
		return ""
	}
	return s
}

// startScanActivity records a scan activity AND publishes scan:start.
func (h *Handler) startScanActivity(action, detail string) string {
	id := h.deps.Activity.Start(action, detail, activity.SourceManual)
	h.deps.Events.PublishScanStart(action, detail, activity.SourceManual)
	return id
}

// endScanActivity marks a scan activity as done (or failed) and publishes scan:done.
func (h *Handler) endScanActivity(id, action, detail string, ok bool) {
	if ok {
		h.deps.Activity.End(id)
	} else {
		h.deps.Activity.Fail(id)
	}
	h.deps.Events.PublishScanDone(action, detail, activity.SourceManual, ok)
	h.deps.InvalidateStats()
}
