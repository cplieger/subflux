package manualops

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/events"
)

// DownloadTimeout is the context timeout for manual downloads.
const DownloadTimeout = 5 * time.Minute

// RunDownload performs the actual download, post-processing, and save.
// Returns true on success.
func RunDownload(ctx context.Context, deps *SearchDeps, ls *LiveState, db DownloadStore,
	prov api.Provider, req *DownloadRequest,
) bool {
	// Download the subtitle.
	sub := api.Subtitle{
		Provider:    req.Provider,
		ID:          req.SubtitleID,
		DownloadURL: req.SubtitleID,
		Language:    req.Language,
		Season:      req.Season,
		Episode:     req.Episode,
		HearingImp:  req.HearingImp,
		Forced:      req.Forced,
	}
	data, err := prov.Download(ctx, &sub)
	if err != nil {
		slog.Error("manual download failed",
			"provider", req.Provider, "subtitle_id", req.SubtitleID, "error", err)
		NotifyError(deps, "manual", "Download failed from "+string(req.Provider),
			"Download failed from "+string(req.Provider))
		return false
	}

	// Reject binary data that isn't a valid subtitle file.
	if err := api.ValidateSubtitleData(data); err != nil {
		slog.Warn("manual download: invalid subtitle data",
			"provider", req.Provider, "subtitle_id", req.SubtitleID, "error", err)
		NotifyError(deps, "manual",
			fmt.Sprintf("Downloaded file from %s is not a valid subtitle (unsupported archive format?)", req.Provider),
			"Downloaded file is not a valid subtitle")
		return false
	}

	// Sync timing against existing reference subtitle.
	variant := api.VariantFromFlags(req.HearingImp, req.Forced)
	data, syncOffsetMs := ls.Engine.SyncAndPostProcess(ctx, data, req.FilePath, req.Language, variant)

	// Resolve media IDs for coverage tracking and history recording.
	mediaType := req.MediaType
	coverageMediaID, historyMediaID := ResolveMediaIDs(ctx, ls, mediaType, req.ArrID, req.Season, req.Episode)

	// Ordinals advance per quad: movie.fr.1.srt and movie.fr.forced.1.srt are
	// independent sequences, matching the variant-aware manual file naming.
	n := db.NextManualNumber(ctx, mediaType, historyMediaID, req.Language, variant)
	subPath := api.ManualSubtitlePath(req.FilePath, req.Language, n, req.HearingImp, req.Forced)

	// Atomic write: temp file + rename prevents corruption on crash.
	if _, err := atomicfile.WriteFile(ctx, subPath, data); err != nil {
		slog.Error("manual download: write failed", "path", subPath, "error", err)
		NotifyError(deps, "manual", "Write failed for manual subtitle download",
			"Write failed for subtitle download")
		return false
	}

	slog.Info("manual download saved", "path", subPath, "number", n)

	// Update coverage and notify arr.
	effectiveMediaID := coverageMediaID
	if effectiveMediaID == "" {
		effectiveMediaID = historyMediaID
	}
	PostDownloadUpdate(ctx, ls, db, req, mediaType, effectiveMediaID, subPath, variant)

	// Record sync offset if sub-to-sub sync was applied.
	if syncOffsetMs != 0 {
		if err := db.SetSyncOffset(ctx, subPath, syncOffsetMs); err != nil {
			slog.Warn("failed to record sync offset", "error", err)
		}
	}

	// Record in history.
	isManual := !req.TopPick
	meta := &api.DownloadMeta{
		Manual:    isManual,
		VideoPath: req.FilePath,
		Season:    req.Season,
		Episode:   req.Episode,
		Title:     LookupMediaTitle(ctx, ls, mediaType, req.ArrID),
	}
	if err := db.SaveDownload(ctx, &api.DownloadRecord{
		MediaType:    mediaType,
		MediaID:      historyMediaID,
		Language:     req.Language,
		Variant:      variant,
		ProviderName: req.Provider,
		ReleaseName:  req.ReleaseName,
		Path:         subPath,
		Score:        max(req.Score, 0),
		Meta:         meta,
	}); err != nil {
		slog.Warn("failed to record manual download", "error", err)
		deps.Alerts.RecordWarn("manual", "Download saved but history recording failed")
	}

	// Publish success events.
	deps.Events.PublishNotify(events.NotifySuccess, "Subtitle downloaded")
	deps.Events.PublishCoverageUpdate(mediaType, effectiveMediaID, req.Language, string(req.Provider), subPath)

	return true
}

// ResolveMediaIDs determines the coverage and history media IDs for a manual
// download.
func ResolveMediaIDs(ctx context.Context, ls *LiveState,
	mediaType api.MediaType, arrID, season, episode int,
) (coverageID, historyID string) {
	if mediaType == api.MediaTypeMovie && arrID > 0 {
		coverageID = LookupMovieMediaID(ctx, ls, arrID)
	} else if mediaType == api.MediaTypeEpisode && arrID > 0 {
		coverageID = LookupEpisodeMediaID(ctx, ls, arrID, season, episode)
	}

	historyID = coverageID
	if historyID == "" && arrID > 0 {
		if mediaType == api.MediaTypeMovie {
			historyID = fmt.Sprintf("radarr-%d", arrID)
		} else {
			historyID = fmt.Sprintf("sonarr-%d-s%02de%02d", arrID, season, episode)
		}
	}
	if historyID == "" {
		historyID = api.BuildMediaID(&api.SearchRequest{MediaType: mediaType})
		slog.Debug("manual download: using fallback media ID",
			"media_type", mediaType, "arr_id", arrID, "history_id", historyID)
	}
	return coverageID, historyID
}

// LookupMovieMediaID finds the tmdb-based media ID for a Radarr movie.
func LookupMovieMediaID(ctx context.Context, ls *LiveState, arrID int) string {
	if ls.Radarr == nil {
		return ""
	}
	m, err := ls.Radarr.GetMovieByID(ctx, arrID)
	if err != nil {
		slog.Warn("failed to look up movie for media ID", "arr_id", arrID, "error", err)
		return ""
	}
	return "tmdb-" + strconv.Itoa(m.TmdbID)
}

// LookupEpisodeMediaID finds the tvdb-based media ID for a Sonarr episode.
func LookupEpisodeMediaID(ctx context.Context, ls *LiveState, seriesID, season, episode int) string {
	if ls.Sonarr == nil {
		return ""
	}
	ser, err := ls.Sonarr.GetSeriesByID(ctx, seriesID)
	if err != nil {
		slog.Warn("failed to look up series for media ID", "series_id", seriesID, "error", err)
		return ""
	}
	return api.BuildEpisodeID(ser.TvdbID, ser.ImdbID, season, episode)
}

// LookupMediaTitle resolves the title for a media item from the arr client.
func LookupMediaTitle(ctx context.Context, ls *LiveState, mediaType api.MediaType, arrID int) string {
	if arrID <= 0 {
		return ""
	}
	if mediaType == api.MediaTypeMovie && ls.Radarr != nil {
		if m, err := ls.Radarr.GetMovieByID(ctx, arrID); err == nil {
			return m.Title
		}
	} else if mediaType == api.MediaTypeEpisode && ls.Sonarr != nil {
		if ser, err := ls.Sonarr.GetSeriesByID(ctx, arrID); err == nil {
			return ser.Title
		}
	}
	return ""
}

// PostDownloadUpdate updates coverage DB and refreshes the arr after a
// successful manual download.
func PostDownloadUpdate(ctx context.Context, ls *LiveState, db DownloadStore,
	req *DownloadRequest, mediaType api.MediaType, coverageMediaID, subPath string, variant api.Variant,
) {
	if coverageMediaID != "" {
		if err := db.UpsertSubtitleFile(ctx, mediaType, coverageMediaID, &api.SubtitleFile{
			Language: req.Language,
			Variant:  variant,
			Source:   api.SourceExternal,
			Path:     subPath,
		}); err != nil {
			slog.Warn("failed to upsert subtitle file", "error", err)
		}
	}

	if mediaType == api.MediaTypeMovie && req.ArrID > 0 && ls.Radarr != nil {
		if err := ls.Radarr.RescanMovie(ctx, req.ArrID); err != nil {
			slog.Warn("failed to refresh movie in radarr",
				"movie_id", req.ArrID, "error", err)
		}
	} else if mediaType == api.MediaTypeEpisode && req.ArrID > 0 && ls.Sonarr != nil {
		if err := ls.Sonarr.RescanSeries(ctx, req.ArrID); err != nil {
			slog.Warn("failed to refresh series in sonarr",
				"series_id", req.ArrID, "error", err)
		}
	}
}
