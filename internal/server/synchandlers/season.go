// POST /api/sync/season (D2): the server-owned season batch. The season's
// subtitle files are enumerated HERE, at acceptance, with exactly the
// selection contract the retired client pool applied (parity-tested):
// file-bearing episodes of the season, the series' resolved configured
// target pairs (config snapshot at acceptance), EXTERNAL entries only from
// the deduplicated coverage rows, every matching ordinal, the ASS/SSA
// writeback gate per item, and vanished rows skipped and reported.

package synchandlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
)

// SyncSeasonRequest is the typed body for POST /api/sync/season: the Sonarr
// series id plus the season whose subtitle files the server enumerates.
type SyncSeasonRequest struct {
	SeriesID int `json:"series_id"`
	Season   int `json:"season"`
}

// SeasonSyncAccepted is the 202 body for POST /api/sync/season: the batch
// activity id. Item job ids arrive via the registry read
// (GET /api/sync/jobs?batch_activity_id=) and the per-item sync:done events.
type SeasonSyncAccepted struct {
	ActivityID string `json:"activity_id"`
}

// SeasonSonarr is the Sonarr surface season enumeration reads: the cached
// series list (series id → tvdb id, title, original language) and the
// cached episodes-by-series read (task 1's wrapper serves both).
type SeasonSonarr interface {
	Series(ctx context.Context) ([]arrapi.Series, error)
	Episodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error)
}

// seasonCfg is the one config value enumeration reads: the resolved
// configured target pairs for the series' audio language, captured ONCE at
// batch acceptance — the config snapshot the whole batch runs under.
type seasonCfg interface {
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []subflux.SubtitleTarget
}

// SeasonState is the hot-reloadable dependency snapshot for one season
// dispatch. Sonarr is nil when not configured.
type SeasonState struct {
	Cfg    seasonCfg
	Sonarr SeasonSonarr
}

// SeasonFileStore is the subtitle-file inventory the season enumeration
// reads — the same store read the coverage detail endpoint served the
// client pool from, so the batch selects from exactly the rows the pool saw.
type SeasonFileStore interface {
	SubtitleFiles(ctx context.Context, mediaType subflux.MediaType, mediaIDPrefix string) ([]subflux.SubtitleEntry, error)
}

// errSeasonFetch marks an arr read failure during enumeration — an
// upstream-failure 502 on the wire, never a silent empty batch.
var errSeasonFetch = errors.New("fetch season from arr")

// seasonSelection is one enumeration's outcome: the runnable items in
// ordinal order, the skip report, and the batch's aggregate detail line.
type seasonSelection struct {
	detail  string
	items   []syncjobs.ExecInput
	skipped int
}

// HandleSyncSeason handles POST /api/sync/season: enumerate server-side,
// then hand ONE batch to the dispatcher and answer 202 {activity_id}. Every
// per-item fact lives in the job registry (all item records created at
// acceptance); the batch activity entry carries the aggregate only.
func (h *Handler) HandleSyncSeason(w http.ResponseWriter, r *http.Request) {
	if !httpapi.RequirePOST(w, r) {
		return
	}
	var req SyncSeasonRequest
	if !httpapi.DecodeJSONBody(w, r, &req, maxBodySize) {
		return
	}
	if req.SeriesID <= 0 || req.Season < 0 {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest,
			"series_id (positive) and season (non-negative) are required")
		return
	}

	sel, err := h.enumerateSeason(r.Context(), &req)
	if err != nil {
		writeSeasonError(w, r, err)
		return
	}
	if len(sel.items) == 0 && sel.skipped == 0 {
		httpapi.NotFoundC(w, r, subflux.CodeSubtitleNotFound,
			"no subtitle files to sync in this season")
		return
	}

	acc, err := h.jobs.DispatchBatch(&syncjobs.BatchInput{
		Detail:   sel.detail,
		SeriesID: req.SeriesID,
		Season:   req.Season,
		Items:    sel.items,
	})
	switch {
	case errors.Is(err, syncjobs.ErrCapacity):
		httpapi.TooManyRequestsC(w, r, subflux.CodeRateLimited,
			"sync queue is full; wait for a running sync to finish")
		return
	case errors.Is(err, syncjobs.ErrShuttingDown):
		httpapi.ServiceUnavailableC(w, r, subflux.CodeServiceUnavailable,
			"server is shutting down")
		return
	case err != nil:
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError)
		return
	}

	slog.Info("season sync accepted",
		"series_id", req.SeriesID, "season", req.Season,
		"items", len(sel.items), "skipped", sel.skipped,
		"skipped_live", acc.SkippedLive, "existing", acc.Existing,
		"activity_id", acc.ActivityID)

	httpapi.WriteJSONStatus(w, http.StatusAccepted, SeasonSyncAccepted{ActivityID: acc.ActivityID})
}

// enumerateSeason selects the season's sync items: the client pool's
// selection contract, executed server-side against the same data sources
// the pool read over HTTP.
func (h *Handler) enumerateSeason(ctx context.Context, req *SyncSeasonRequest) (*seasonSelection, error) {
	st := h.seasonState()
	if st == nil || st.Sonarr == nil || st.Cfg == nil {
		return nil, fmt.Errorf("%w: sonarr not configured", resolve.ErrMediaNotFound)
	}

	series, err := st.Sonarr.Series(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: series list: %w", errSeasonFetch, err)
	}
	var ser *arrapi.Series
	for i := range series {
		if series[i].ID == req.SeriesID {
			ser = &series[i]
			break
		}
	}
	// A series without a positive TVDB id has no addressable coverage rows
	// (the collections omit it — A2's exclusion parity), so there is
	// nothing a season batch could address either.
	if ser == nil || ser.TvdbID <= 0 {
		return nil, fmt.Errorf("%w: series %d", resolve.ErrMediaNotFound, req.SeriesID)
	}

	episodes, err := st.Sonarr.Episodes(ctx, req.SeriesID)
	if err != nil {
		return nil, fmt.Errorf("%w: episodes for series %d: %w", errSeasonFetch, req.SeriesID, err)
	}
	// File-bearing episodes of the season, ascending — the order the
	// media endpoint served the pool.
	eps := make([]arrapi.Episode, 0, len(episodes))
	for i := range episodes {
		if episodes[i].SeasonNumber == req.Season && episodes[i].HasFile {
			eps = append(eps, episodes[i])
		}
	}
	slices.SortFunc(eps, func(a, b arrapi.Episode) int { return a.EpisodeNumber - b.EpisodeNumber })

	sel := &seasonSelection{}
	if len(eps) > 0 {
		// Config snapshot at acceptance: the target pairs every item is
		// judged against, resolved once.
		targets := st.Cfg.ResolveTargetsWithFallback(arrsvc.OriginalLangCode(ser.OriginalLanguage), nil)

		rows, err := h.files.SubtitleFiles(ctx, subflux.MediaTypeEpisode, mediaid.SeriesPrefix(ser.TvdbID, ser.ImdbID))
		if err != nil {
			return nil, fmt.Errorf("subtitle rows for series %d: %w", req.SeriesID, err)
		}
		// The pool consumed the coverage detail read, which deduplicates
		// (media_id, language, variant, source); selecting from the same
		// shape keeps ordinal choice identical.
		rows = coverage.DeduplicateFileRows(rows)
		idx := make(map[string]map[coverage.Key][]*subflux.SubtitleEntry, len(rows))
		for i := range rows {
			row := &rows[i]
			k := coverage.Key{Lang: row.Language, Variant: row.Variant}
			if idx[row.MediaID] == nil {
				idx[row.MediaID] = make(map[coverage.Key][]*subflux.SubtitleEntry, 4)
			}
			idx[row.MediaID][k] = append(idx[row.MediaID][k], row)
		}

		for i := range eps {
			mediaID := mediaid.Episode(ser.TvdbID, ser.ImdbID,
				mediaid.SeasonEpisode{Season: req.Season, Episode: eps[i].EpisodeNumber})
			subs := idx[mediaID]
			for j := range targets {
				t := &targets[j]
				key := coverage.Key{Lang: t.Code, Variant: string(t.EffectiveVariant())}
				for _, row := range subs[key] {
					if row.Source == string(subflux.SourceEmbedded) {
						continue
					}
					h.appendSeasonItem(ctx, sel, row)
				}
			}
		}
	}

	files := "files"
	if len(sel.items) == 1 {
		files = "file"
	}
	sel.detail = fmt.Sprintf("%s S%02d · %d %s", ser.Title, req.Season, len(sel.items), files)
	if sel.skipped > 0 {
		sel.detail += fmt.Sprintf(" · %d skipped", sel.skipped)
	}
	return sel, nil
}

// appendSeasonItem resolves one selected row into a runnable item, applying
// the per-item gates the single-file accept path applies: path resolution
// (a vanished row is skipped and reported, never a batch failure) and the
// ASS/SSA writeback refusal — the batch never dry-runs, so the gate always
// binds. See decodeSyncAudioRequest for why the gate exists.
func (h *Handler) appendSeasonItem(ctx context.Context, sel *seasonSelection, row *subflux.SubtitleEntry) {
	ref := resolve.FileRef{
		MediaType: subflux.MediaTypeEpisode,
		MediaID:   row.MediaID,
		Language:  row.Language,
		Variant:   row.Variant,
		Source:    row.Source,
		Ordinal:   row.Ordinal,
	}
	skip := func(reason string, err error) {
		sel.skipped++
		slog.Warn("season sync: item skipped",
			"media_id", ref.MediaID, "language", ref.Language,
			"variant", ref.Variant, "ordinal", ref.Ordinal,
			"reason", reason, "error", err)
	}
	subPath, err := h.resolve.SubtitlePath(ctx, &ref)
	if err != nil {
		skip("subtitle path", err)
		return
	}
	if isASSSubtitlePath(subPath) {
		skip("ASS/SSA writeback unsupported", nil)
		return
	}
	videoPath, err := h.resolve.VideoPathForFile(ctx, &ref)
	if err != nil {
		skip("video path", err)
		return
	}
	sel.items = append(sel.items, syncjobs.ExecInput{
		Ref:          ref,
		SubtitlePath: subPath,
		VideoPath:    videoPath,
	})
}

// writeSeasonError maps an enumeration failure onto the wire: unresolvable
// series → 404, an arr read failure → the family's upstream-failure 502, a
// store failure → the generic 500 arm.
func writeSeasonError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, resolve.ErrMediaNotFound):
		httpapi.NotFoundC(w, r, subflux.CodeMediaNotFound, "media not found")
	case errors.Is(err, errSeasonFetch):
		slog.Error("season sync: arr read failed", "error", err)
		httpapi.BadGatewayC(w, r, subflux.CodeBadGateway, "failed to fetch episodes")
	default:
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "stage", "season enumeration")
	}
}
