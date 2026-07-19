package scanning

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/httphelpers"
)

// ScanGuard serializes manual scan requests. Extracted from activity.Log
// to separate coordination concerns from data concerns. The slot is a
// capacity-1 channel semaphore rather than a mutex so acquisition can be
// SELECTED against the stop signal and the server context: a queued scan
// whose stop arrives reaches its terminal cancelled state immediately, and
// shutdown never blocks behind a running scan — a bare mutex Lock could
// observe neither. The zero value is ready to use (the Server embeds one).
type ScanGuard struct {
	token chan struct{}
	once  sync.Once
}

// sem returns the capacity-1 token channel, created lazily so the zero
// value stays usable.
func (g *ScanGuard) sem() chan struct{} {
	g.once.Do(func() { g.token = make(chan struct{}, 1) })
	return g.token
}

// Acquire takes the scan slot, blocking until it is free — or gives up when
// the stop signal fires or ctx (the server context) is cancelled first, in
// which case the slot is NOT held and the caller must not Release. It
// reports whether the slot was acquired. When the slot frees at the same
// instant a signal fires, either case may win; callers re-check the signals
// after a successful acquisition.
func (g *ScanGuard) Acquire(ctx context.Context, stop <-chan struct{}) bool {
	select {
	case g.sem() <- struct{}{}:
		return true
	case <-stop:
		return false
	case <-ctx.Done():
		return false
	}
}

// Release frees the scan slot. Only a successful Acquire may Release; a
// release without a held slot is a programming error and panics (matching
// sync.Mutex.Unlock semantics).
func (g *ScanGuard) Release() {
	select {
	case <-g.sem():
	default:
		panic("scanning: ScanGuard.Release without a held slot")
	}
}

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
	// StateFunc returns the current live state snapshot as BOTH views one
	// operation needs — the handler view and the scanning-engine view —
	// derived from ONE generation read, so the two views can never straddle
	// a hot reload.
	StateFunc func() (*HandlerState, *LiveState)
	// CtxFunc returns the server-level context (outlives individual requests).
	CtxFunc func() context.Context
	// ScanDeps provides the scanning engine dependencies (stable server
	// singletons, not generation state).
	ScanDeps func() *Deps
	// Activity tracks scan activity lifecycle.
	Activity ActivityTracker
	// Stops registers the graceful stop callbacks of running scans.
	Stops *activity.StopRegistry
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

// ScanAccepted is the typed 202 Accepted response for scan starts. The scan
// executes in a server-owned background goroutine; GET /api/activity is the
// status monitor for the returned activity id (RFC 9110 202 guidance).
// Deliberately a separate type from manualops.DownloadAccepted: the two docs
// diverge.
type ScanAccepted struct {
	ActivityID string `json:"activity_id"`
	Status     string `json:"status"`
}

// scanRunner is a background scan body: it runs under the server-derived
// context (shutdown is the only hard kill), honours the graceful stop signal
// between items, and reports the four-valued terminal outcome the caller
// applies to actID.
type scanRunner func(ctx context.Context, stop <-chan struct{}, actID string) activity.Outcome

// opState is the single immutable state snapshot one scan operation runs
// against, captured ONCE at accept time and threaded through the queue to
// the runner. Preflight, slot acquisition, and the scan body therefore see
// the SAME arr clients / config / engine generation: a hot reload landing
// between accept and (possibly much later, after queueing) execution can no
// longer mix generations — e.g. apply a media id resolved against arr
// generation A to the clients of arr generation B. The stable singletons
// (Activity, Stops, ScanGuard, Events, Alerts, BGTracker) are not
// generation state and stay on HandlerDeps.
type opState struct {
	st   *HandlerState
	deps *Deps
	ls   *LiveState
}

// snapshotOp captures the per-operation state. The generation callback is
// consulted exactly once per operation, here at the accept boundary — never
// again after queue admission — and returns both views from one read.
func (h *Handler) snapshotOp() *opState {
	st, ls := h.deps.StateFunc()
	return &opState{
		st:   st,
		deps: h.deps.ScanDeps(),
		ls:   ls,
	}
}

// startBackgroundScan owns the accept sequence shared by all manual scan
// starts: idempotent same-scope dedupe + hoisted Activity.Start (one atomic
// step), stop registration BEFORE the 202 (no cancel-before-start window for
// the requester), background spawn via BGTracker, and the 202 ScanAccepted
// body. Preflight (arr lookup → 404/502, validation → 400) has already run
// synchronously in the calling handler.
//
// Every scan in this handler family is per-item and therefore cancellable by
// any configured user (auth.RoleUser, single-household policy); the
// admin-only full scan takes the scheduler.PrepareFullScan path instead.
func (h *Handler) startBackgroundScan(w http.ResponseWriter, action, detail string,
	scope activity.ScanScope, run scanRunner,
) {
	actID, existing := h.deps.Activity.StartScan(action, detail, activity.SourceManual, scope, auth.RoleUser)
	if existing {
		// Same-scope scan already running or queued: return ITS id — a
		// re-click after a lost 202 must not double-start the work.
		api.WriteJSONStatus(w, http.StatusAccepted,
			ScanAccepted{ActivityID: actID, Status: "scan already running"})
		return
	}
	stopCh := make(chan struct{})
	unregister := h.deps.Stops.RegisterStop(actID, func() { close(stopCh) })
	h.deps.Events.PublishScanStart(action, detail, activity.SourceManual, actID)

	h.deps.BGTracker.Add(1)
	go func() {
		defer h.deps.BGTracker.Done()
		// Panic fallback only: FinishScanActivity releases the registration
		// explicitly BEFORE the terminal transition on every normal return,
		// so a done entry never reports cancellable. The defer covers a
		// panicking runner — a leaked registration would keep reporting
		// cancellable forever. Unregister is idempotent.
		defer unregister()
		outcome := run(h.deps.CtxFunc(), stopCh, actID)
		h.finishScan(unregister, actID, action, detail, outcome)
	}()

	api.WriteJSONStatus(w, http.StatusAccepted,
		ScanAccepted{ActivityID: actID, Status: "scan started"})
}

// finishScan applies the runner outcome to the activity entry (releasing the
// stop registration first; see FinishScanActivity), publishes scan:done, and
// invalidates the stats cache (skipped on shutdown: the process is exiting).
func (h *Handler) finishScan(unregister func(), actID, action, detail string, outcome activity.Outcome) {
	FinishScanActivity(unregister, h.deps.Activity, h.deps.Events, actID, action, detail,
		activity.SourceManual, outcome)
	if outcome != activity.OutcomeShutdown {
		h.deps.InvalidateStats()
	}
}

// preflightSeries synchronously resolves the series for a scan start: the
// arr existence lookup that must answer 404 BEFORE the 202 is written.
// Lookup failures that aren't "not found" surface as 502 (upstream proxy
// failure). Runs on the REQUEST context — the scan itself has not started.
func (h *Handler) preflightSeries(w http.ResponseWriter, r *http.Request,
	st *HandlerState, seriesID int,
) (arrapi.Series, bool) {
	series, err := st.Sonarr.GetSeriesByID(r.Context(), seriesID)
	if err != nil {
		if arrapi.IsNotFound(err) {
			api.NotFoundC(w, r, api.CodeMediaNotFound, "series not found")
		} else {
			slog.Error("scan: failed to fetch series", "id", seriesID, "error", err)
			api.BadGatewayC(w, r, api.CodeArrUnreachable, "sonarr lookup failed")
		}
		return arrapi.Series{}, false
	}
	return series, true
}

// preflightMovie is preflightSeries for Radarr movies.
func (h *Handler) preflightMovie(w http.ResponseWriter, r *http.Request,
	st *HandlerState, movieID int,
) (arrapi.Movie, bool) {
	movie, err := st.Radarr.GetMovieByID(r.Context(), movieID)
	if err != nil {
		if arrapi.IsNotFound(err) {
			api.NotFoundC(w, r, api.CodeMediaNotFound, "movie not found")
		} else {
			slog.Error("scan: failed to fetch movie", "id", movieID, "error", err)
			api.BadGatewayC(w, r, api.CodeArrUnreachable, "radarr lookup failed")
		}
		return arrapi.Movie{}, false
	}
	return movie, true
}

// HandleScanSeries scans all missing subtitles for a specific series.
// POST /api/scan/series/{sonarrId} — 202 + activity_id at accept time.
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

	op := h.snapshotOp()
	if op.st.Sonarr == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgSonarrNotConfigured)
		return
	}
	series, ok := h.preflightSeries(w, r, op.st, seriesID)
	if !ok {
		return
	}

	slog.Info("manual series scan requested", "series_id", seriesID)

	h.startBackgroundScan(w, "Series Search", series.Title,
		activity.ScanScope{Kind: activity.ScanKindSeries, MediaID: seriesID},
		func(ctx context.Context, stop <-chan struct{}, actID string) activity.Outcome {
			return h.scanEpisodes(ctx, stop, actID, op, &series, "Series",
				func(*arrapi.Episode) bool { return true })
		})
}

// HandleScanSeason scans all missing subtitles for a specific season.
// POST /api/scan/season/{sonarrId}/{seasonNum} — 202 + activity_id.
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

	op := h.snapshotOp()
	if op.st.Sonarr == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgSonarrNotConfigured)
		return
	}
	series, ok := h.preflightSeries(w, r, op.st, seriesID)
	if !ok {
		return
	}

	slog.Info("manual season scan requested",
		"series_id", seriesID, "season", seasonNum)

	h.startBackgroundScan(w,
		fmt.Sprintf("Season S%02d Search", seasonNum),
		fmt.Sprintf("%s S%02d", series.Title, seasonNum),
		activity.ScanScope{Kind: activity.ScanKindSeason, MediaID: seriesID, Season: seasonNum},
		func(ctx context.Context, stop <-chan struct{}, actID string) activity.Outcome {
			return h.scanEpisodes(ctx, stop, actID, op, &series,
				fmt.Sprintf("Season S%02d", seasonNum),
				func(ep *arrapi.Episode) bool { return ep.SeasonNumber == seasonNum })
		})
}

// HandleScanItem scans a single episode or movie.
// POST /api/scan/item with JSON body — 202 + activity_id.
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

	op := h.snapshotOp()
	if req.MediaType == api.MediaTypeMovie {
		if op.st.Radarr == nil {
			api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgRadarrNotConfigured)
			return
		}
		movie, ok := h.preflightMovie(w, r, op.st, req.MediaID)
		if !ok {
			return
		}
		h.startBackgroundScan(w, "Movie Search",
			fmt.Sprintf("%s (%d)", movie.Title, movie.Year),
			activity.ScanScope{Kind: activity.ScanKindItem, MediaType: api.MediaTypeMovie, MediaID: req.MediaID},
			func(ctx context.Context, stop <-chan struct{}, actID string) activity.Outcome {
				return h.runMovieScan(ctx, stop, actID, op, &movie)
			})
		return
	}

	if op.st.Sonarr == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgSonarrNotConfigured)
		return
	}
	series, ok := h.preflightSeries(w, r, op.st, req.MediaID)
	if !ok {
		return
	}
	season, episode := req.Season, req.Episode
	h.startBackgroundScan(w, "Episode Search",
		fmt.Sprintf("%s S%02dE%02d", series.Title, season, episode),
		activity.ScanScope{
			Kind: activity.ScanKindItem, MediaType: api.MediaTypeEpisode,
			MediaID: req.MediaID, Season: season, Episode: episode,
		},
		func(ctx context.Context, stop <-chan struct{}, actID string) activity.Outcome {
			return h.scanSingleEpisode(ctx, stop, actID, op, &series, season, episode)
		})
}

// HandleScanMovie scans all missing subtitles for a specific movie.
// POST /api/scan/movie/{radarrId} — 202 + activity_id.
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

	op := h.snapshotOp()
	if op.st.Radarr == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, api.ErrMsgRadarrNotConfigured)
		return
	}
	movie, ok := h.preflightMovie(w, r, op.st, movieID)
	if !ok {
		return
	}

	slog.Info("manual movie scan requested", "movie_id", movieID)

	h.startBackgroundScan(w, "Movie Search",
		fmt.Sprintf("%s (%d)", movie.Title, movie.Year),
		activity.ScanScope{Kind: activity.ScanKindMovie, MediaID: movieID},
		func(ctx context.Context, stop <-chan struct{}, actID string) activity.Outcome {
			return h.runMovieScan(ctx, stop, actID, op, &movie)
		})
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
