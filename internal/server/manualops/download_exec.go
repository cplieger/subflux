package manualops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/cplieger/atomicfile/v3"
	"github.com/cplieger/subflux/internal/httpwire"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subtitlefile"
)

// DownloadTimeout bounds a single manual download's run.
const DownloadTimeout = 5 * time.Minute

// RunDownload performs the download, post-processing, and save. actID is
// the download's activity entry: on success its detail is updated with the
// saved subtitle path, which is how the remote CLI's poll loop learns where
// the file landed. Returns true on success.
func RunDownload(ctx context.Context, deps *SearchDeps, ls *LiveState, db DownloadStore,
	prov provider.Provider, req *DownloadRequest, actID string,
) bool {
	sub := subflux.Subtitle{
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
		NotifyError(deps, ErrorNotice{
			Source: alertSourceManual,
			Alert:  "Download failed from " + string(req.Provider),
			UI:     "Download failed from " + string(req.Provider),
		})
		return false
	}

	// subtitlefile.Validate owns both refusals; only the operator-facing text
	// distinguishes them, because blaming the archive format for a body
	// that had no bytes misdiagnoses it.
	if err := subtitlefile.Validate(data); err != nil {
		slog.Warn("manual download: invalid subtitle data",
			"provider", req.Provider, "subtitle_id", req.SubtitleID, "error", err)
		alert := fmt.Sprintf("Downloaded file from %s is not a valid subtitle (unsupported archive format?)", req.Provider)
		if errors.Is(err, subtitlefile.ErrEmpty) {
			alert = fmt.Sprintf("%s returned an empty file for this subtitle", req.Provider)
		}
		NotifyError(deps, ErrorNotice{
			Source: alertSourceManual,
			Alert:  alert,
			UI:     "Downloaded file is not a valid subtitle",
		})
		return false
	}

	variant := subtitlefile.VariantFromFlags(subtitlefile.Tags{HearingImpaired: req.HearingImp, Forced: req.Forced})
	data, syncOffsetMs := ls.Engine.SyncAndPostProcess(ctx, data, req.VideoPath(), req.Language, variant)

	mediaType := req.MediaType
	coverageMediaID, historyMediaID := ResolveMediaIDs(ctx, ls, mediaType, req.ArrID, req.Season, req.Episode)

	// title is an arr HTTP lookup; resolve it BEFORE taking the per-quad path
	// reservation so no remote call runs under the gate.
	title := LookupMediaTitle(ctx, ls, mediaType, req.ArrID)

	subPath, ok := commitNumberedSubtitle(ctx, deps, db, req, historyMediaID, title, variant, data)
	if !ok {
		return false
	}

	// Progress counters stay zero — downloads have no progress steps; the
	// call is here only to record the saved path as the activity detail.
	deps.Activity.Progress(actID, 0, 0, subPath)

	effectiveMediaID := coverageMediaID
	if effectiveMediaID == "" {
		effectiveMediaID = historyMediaID
	}
	PostDownloadUpdate(ctx, ls, db, req, mediaType, effectiveMediaID, subPath, variant)

	if syncOffsetMs != 0 {
		if err := db.SetSyncOffset(ctx, subPath, syncOffsetMs); err != nil {
			slog.Warn("failed to record sync offset", "error", err)
		}
	}

	deps.Events.PublishNotify(events.NotifySuccess, "Subtitle downloaded")
	deps.Events.PublishCoverageUpdate(&events.CoverageEvent{
		MediaType: mediaType,
		MediaID:   effectiveMediaID,
		Language:  req.Language,
		Source:    string(req.Provider),
	})

	return true
}

// commitNumberedSubtitle allocates the quad's next ordinal, writes the
// subtitle bytes to the numbered path, and records the download in
// history, all under the quad's downloadPathGate reservation. Holding the
// gate across allocation, atomic write, AND history insertion is the
// point: ordinal discovery (NextManualNumber reads the recorded paths)
// always sees the previous holder's committed row, so two concurrent
// downloads for the same quad can never claim the same number and
// overwrite each other's file. Only local disk and bbolt work runs under
// the gate. Returns ok=false only when the file write failed; a
// history-recording failure warns and keeps the saved file.
func commitNumberedSubtitle(ctx context.Context, deps *SearchDeps, db DownloadStore,
	req *DownloadRequest, historyMediaID, title string, variant subflux.Variant, data []byte,
) (subPath string, ok bool) {
	unlock := downloadPathGate.lock(downloadQuadKey(req.MediaType, historyMediaID, req.Language, variant))
	defer unlock()

	// Ordinals advance per quad: movie.fr.1.srt and movie.fr.forced.1.srt are
	// independent sequences.
	n := db.NextManualNumber(ctx, subflux.ManualLockKey{
		MediaType: req.MediaType, MediaID: historyMediaID,
		Language: req.Language, Variant: variant,
	})
	subPath = subtitlefile.ManualPath(req.VideoPath(), n,
		subtitlefile.Tags{Lang: req.Language, HearingImpaired: req.HearingImp, Forced: req.Forced})

	// WithMaxBytes mirrors the read bound: the sync handlers load subtitles
	// with ReadBounded(MaxSyncSubSize == httpwire.MaxDownloadBytes), so a
	// post-processed payload the read path would refuse must fail here,
	// loudly, instead of landing on disk.
	if _, err := atomicfile.WriteFile(ctx, subPath, data,
		atomicfile.WithMaxBytes(httpwire.MaxDownloadBytes)); err != nil {
		slog.Error("manual download: write failed", "path", subPath, "error", err)
		NotifyError(deps, ErrorNotice{
			Source: alertSourceManual,
			Alert:  "Write failed for manual subtitle download",
			UI:     "Write failed for subtitle download",
		})
		return "", false
	}

	slog.Info("manual download saved", "path", subPath, "number", n)

	// A top pick records as auto (manual=false) but still occupies a
	// numbered path, which is why ordinal discovery scans every row's path
	// regardless of the Manual flag.
	meta := &subflux.DownloadMeta{
		Manual:    !req.TopPick,
		VideoPath: req.VideoPath(),
		Season:    req.Season,
		Episode:   req.Episode,
		Title:     title,
	}
	if err := db.SaveDownload(ctx, &subflux.DownloadRecord{
		MediaType:    req.MediaType,
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
		deps.Alerts.RecordWarn(alertSourceManual, "Download saved but history recording failed")
	}
	return subPath, true
}

// ResolveMediaIDs resolves the coverage and history media IDs for a manual download.
func ResolveMediaIDs(ctx context.Context, ls *LiveState,
	mediaType subflux.MediaType, arrID, season, episode int,
) (coverageID, historyID string) {
	if mediaType == subflux.MediaTypeMovie && arrID > 0 {
		coverageID = LookupMovieMediaID(ctx, ls, arrID)
	} else if mediaType == subflux.MediaTypeEpisode && arrID > 0 {
		coverageID = LookupEpisodeMediaID(ctx, ls, arrID, season, episode)
	}

	historyID = coverageID
	if historyID == "" && arrID > 0 {
		if mediaType == subflux.MediaTypeMovie {
			historyID = fmt.Sprintf("radarr-%d", arrID)
		} else {
			historyID = fmt.Sprintf("sonarr-%d-s%02de%02d", arrID, season, episode)
		}
	}
	if historyID == "" {
		historyID = mediaid.Build(&subflux.SearchRequest{MediaType: mediaType})
		slog.Debug("manual download: using fallback media ID",
			"media_type", mediaType, "arr_id", arrID, "history_id", historyID)
	}
	return coverageID, historyID
}

// LookupMovieMediaID resolves a Radarr movie's stable media ID by arr ID.
func LookupMovieMediaID(ctx context.Context, ls *LiveState, arrID int) string {
	if ls.Radarr == nil {
		return ""
	}
	m, err := ls.Radarr.MovieByID(ctx, arrID)
	if err != nil {
		slog.Warn("failed to look up movie for media ID", "arr_id", arrID, "error", err)
		return ""
	}
	return "tmdb-" + strconv.Itoa(m.TmdbID)
}

// LookupEpisodeMediaID resolves a Sonarr episode's stable media ID by series ID, season, and episode.
func LookupEpisodeMediaID(ctx context.Context, ls *LiveState, seriesID, season, episode int) string {
	if ls.Sonarr == nil {
		return ""
	}
	ser, err := ls.Sonarr.SeriesByID(ctx, seriesID)
	if err != nil {
		slog.Warn("failed to look up series for media ID", "series_id", seriesID, "error", err)
		return ""
	}
	return mediaid.Episode(ser.TvdbID, ser.ImdbID, mediaid.SeasonEpisode{Season: season, Episode: episode})
}

// LookupMediaTitle resolves a media item's title by arr ID.
func LookupMediaTitle(ctx context.Context, ls *LiveState, mediaType subflux.MediaType, arrID int) string {
	if arrID <= 0 {
		return ""
	}
	if mediaType == subflux.MediaTypeMovie && ls.Radarr != nil {
		if m, err := ls.Radarr.MovieByID(ctx, arrID); err == nil {
			return m.Title
		}
	} else if mediaType == subflux.MediaTypeEpisode && ls.Sonarr != nil {
		if ser, err := ls.Sonarr.SeriesByID(ctx, arrID); err == nil {
			return ser.Title
		}
	}
	return ""
}

// PostDownloadUpdate records the coverage file and triggers an arr rescan after a manual download.
func PostDownloadUpdate(ctx context.Context, ls *LiveState, db DownloadStore,
	req *DownloadRequest, mediaType subflux.MediaType, coverageMediaID, subPath string, variant subflux.Variant,
) {
	if coverageMediaID != "" {
		if err := db.UpsertSubtitleFile(ctx, mediaType, coverageMediaID, &subflux.SubtitleFile{
			Language: req.Language,
			Variant:  variant,
			Source:   subflux.SourceExternal,
			Path:     subPath,
		}); err != nil {
			slog.Warn("failed to upsert subtitle file", "error", err)
		}
	}

	if mediaType == subflux.MediaTypeMovie && req.ArrID > 0 && ls.Radarr != nil {
		if err := ls.Radarr.RescanMovie(ctx, req.ArrID); err != nil {
			slog.Warn("failed to refresh movie in radarr",
				"movie_id", req.ArrID, "error", err)
		}
	} else if mediaType == subflux.MediaTypeEpisode && req.ArrID > 0 && ls.Sonarr != nil {
		if err := ls.Sonarr.RescanSeries(ctx, req.ArrID); err != nil {
			slog.Warn("failed to refresh series in sonarr",
				"series_id", req.ArrID, "error", err)
		}
	}
}
