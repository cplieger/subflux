package scanning

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// This file holds the background runner bodies of the manual scan endpoints.
// The accept sequence (preflight, hoisted Activity.Start, stop registration,
// 202) lives in handler_http.go; every runner here executes under the
// server-derived context, receives the accept-time operation snapshot (op —
// the runners never re-read live state, so one operation never mixes
// arr/config/engine generations), honours the graceful stop signal between
// items, and returns the four-valued outcome that startBackgroundScan
// applies.

// scanEpisodes fetches the episodes of an already-resolved series and scans
// those matching the filter. Used by both the series and season runners.
// op.st.Sonarr is non-nil by construction: the handler verified it on this
// same snapshot before preflight.
func (h *Handler) scanEpisodes(ctx context.Context, stop <-chan struct{}, actID string,
	op *opState, series *arrapi.Series, label string, filterEp func(*arrapi.Episode) bool,
) activity.Outcome {
	episodes, err := op.st.Sonarr.GetEpisodes(ctx, series.ID)
	if err != nil {
		slog.Error("scan: failed to fetch episodes",
			"series", series.Title, "error", err)
		h.deps.Alerts.Record("scan", label+" scan failed: "+err.Error())
		return activity.OutcomeFailed
	}

	withFiles := filterEpisodesWithFiles(episodes, filterEp)
	if len(withFiles) == 0 {
		slog.Debug("scan: no episodes with files", "series", series.Title)
		h.deps.Activity.Progress(actID, 0, 0, series.Title+": no episodes with files")
		return activity.OutcomeCompleted
	}
	h.deps.Activity.Progress(actID, 0, len(withFiles),
		fmt.Sprintf("%s (%d episodes)", series.Title, len(withFiles)))

	if outcome := h.acquireScanSlot(ctx, actID, stop); outcome != "" {
		slog.Debug("scan ended while queued",
			"series_id", series.ID, "label", label, "outcome", outcome)
		return outcome
	}
	defer h.deps.ScanGuard.Release()

	scanDelay := op.st.Cfg.Search().ScanDelay
	found, searched, outcome := h.runEpisodeScans(ctx, stop, op, series, withFiles, actID, scanDelay)
	if outcome != "" {
		slog.Info(label+" scan ended early",
			"series", series.Title, "searched", searched, "found", found,
			"outcome", outcome)
		return outcome
	}

	slog.Info(label+" scan complete",
		"series", series.Title, "searched", searched, "found", found)
	h.deps.Activity.Progress(actID, len(withFiles), len(withFiles),
		fmt.Sprintf("%s: %d/%d found", series.Title, found, searched))
	return activity.OutcomeCompleted
}

// filterEpisodesWithFiles returns pointers to the episodes that match filterEp
// and have a downloaded file.
func filterEpisodesWithFiles(episodes []arrapi.Episode,
	filterEp func(*arrapi.Episode) bool,
) []*arrapi.Episode {
	var withFiles []*arrapi.Episode
	for i := range episodes {
		if filterEp(&episodes[i]) && episodes[i].HasFile && episodes[i].EpisodeFile != nil {
			withFiles = append(withFiles, &episodes[i])
		}
	}
	return withFiles
}

// acquireScanSlot marks the activity queued, acquires the scan slot so only
// one manual scan runs at a time, then clears the queued flag. Acquisition
// is interruptible: the guard selects the slot against the stop signal and
// the server context, so a queued scan whose cancellation (or shutdown)
// arrives reaches its terminal outcome immediately instead of waiting for
// the running scan to release the slot. It returns a non-empty outcome when
// the scan must not proceed: cancelled while queued (via the dismiss path's
// activity.Cancel OR the stop signal), or shutdown. In those cases the slot
// is not held. On an empty outcome the caller owns the slot and must
// Release it.
func (h *Handler) acquireScanSlot(ctx context.Context, actID string, stop <-chan struct{}) activity.Outcome {
	h.deps.Activity.SetQueued(actID, true)
	acquired := h.deps.ScanGuard.Acquire(ctx, stop)
	h.deps.Activity.SetQueued(actID, false)
	if !acquired {
		// Gave up without the slot: shutdown wins over stop so a process
		// exit is never reported as a user cancellation.
		if ctx.Err() != nil {
			return activity.OutcomeShutdown
		}
		return activity.OutcomeCancelled
	}
	// The slot may have freed at the same instant a signal fired (or the
	// dismiss path cancelled the queued entry, which has no signal to
	// select on): re-check before running.
	if h.deps.Activity.IsCancelled(actID) || stopRequested(stop) {
		h.deps.ScanGuard.Release()
		return activity.OutcomeCancelled
	}
	if ctx.Err() != nil {
		h.deps.ScanGuard.Release()
		return activity.OutcomeShutdown
	}
	return ""
}

// runEpisodeScans scans each episode in order, reporting progress and
// honouring shutdown (context) and the graceful stop signal between items —
// the episode in flight always completes, INCLUDING the final one: after it
// returns, shutdown and stop are checked once more so a stop landing during
// the last in-flight episode terminates as cancelled, never as a false
// success. It returns the number of episodes for which a subtitle was
// found, the number searched, and a non-empty outcome when the scan must
// not be reported as completed.
func (h *Handler) runEpisodeScans(ctx context.Context, stop <-chan struct{},
	op *opState, series *arrapi.Series, withFiles []*arrapi.Episode, actID string,
	scanDelay time.Duration,
) (found, searched int, outcome activity.Outcome) {
	for i, ep := range withFiles {
		if ctx.Err() != nil {
			return found, searched, activity.OutcomeShutdown
		}
		if stopRequested(stop) {
			return found, searched, activity.OutcomeCancelled
		}
		h.deps.Activity.Progress(actID, i+1, len(withFiles),
			fmt.Sprintf("%s S%02dE%02d (%d/%d)",
				series.Title, ep.SeasonNumber, ep.EpisodeNumber,
				i+1, len(withFiles)))
		o, _, queried := ScanEpisode(ctx, op.deps, op.ls, series, ep, true)
		searched++
		if o == ScanFound {
			found++
		}
		// Pace only after episodes that actually queried providers (and
		// never after the last): the delay spaces provider traffic, and a
		// covered/locked/backed-off episode generates none.
		if queried && i < len(withFiles)-1 {
			if early := waitOrStop(ctx, stop, scanDelay); early != "" {
				return found, searched, early
			}
		}
	}
	// The final episode has no next-iteration boundary check: shutdown
	// FIRST (a process exit must never read as a user cancellation), stop
	// SECOND, before the caller publishes success.
	if ctx.Err() != nil {
		return found, searched, activity.OutcomeShutdown
	}
	if stopRequested(stop) {
		return found, searched, activity.OutcomeCancelled
	}
	return found, searched, ""
}

// scanSingleEpisode scans a single episode of an already-resolved series.
// op.st.Sonarr is non-nil by construction (verified by the handler on this
// snapshot).
func (h *Handler) scanSingleEpisode(ctx context.Context, stop <-chan struct{}, actID string,
	op *opState, series *arrapi.Series, seasonNum, episodeNum int,
) activity.Outcome {
	episodes, err := op.st.Sonarr.GetEpisodes(ctx, series.ID)
	if err != nil {
		slog.Error("scan episode: failed to fetch episodes",
			"series", series.Title, "error", err)
		h.deps.Alerts.Record("scan", "Episode scan failed: "+err.Error())
		return activity.OutcomeFailed
	}

	var ep *arrapi.Episode
	for i := range episodes {
		if episodes[i].SeasonNumber == seasonNum &&
			episodes[i].EpisodeNumber == episodeNum &&
			episodes[i].HasFile && episodes[i].EpisodeFile != nil {
			ep = &episodes[i]
			break
		}
	}
	label := fmt.Sprintf("%s S%02dE%02d", series.Title, seasonNum, episodeNum)
	if ep == nil {
		slog.Warn("scan episode: episode not found or no file",
			"series", series.Title, "season", seasonNum,
			"episode", episodeNum)
		h.deps.Activity.Progress(actID, 0, 0, label+": episode not found or no file")
		return activity.OutcomeFailed
	}

	// Join the same serialization as the series/season/movie manual scans so
	// only one MANUAL scan runs at a time (queueing UX). Same-item collisions
	// with the scheduled scan or the poller are handled one layer down by
	// the engine's per-media gate, which serializes SearchTargets by
	// (media_type, media_id) across all execution paths.
	if outcome := h.acquireScanSlot(ctx, actID, stop); outcome != "" {
		slog.Debug("scan ended while queued", "media", label, "outcome", outcome)
		return outcome
	}
	defer h.deps.ScanGuard.Release()

	outcome, _, _ := ScanEpisode(ctx, op.deps, op.ls, series, ep, true)
	// For a single-item scope the in-flight item IS the whole scan: after it
	// returns, check shutdown FIRST and stop SECOND before publishing
	// success — a stop during the item must terminate as cancelled.
	if ctx.Err() != nil {
		return activity.OutcomeShutdown
	}
	if stopRequested(stop) {
		slog.Info("episode scan stopped", "media", label)
		return activity.OutcomeCancelled
	}
	slog.Info("episode scan complete", "media", label, "outcome", outcome)
	return activity.OutcomeCompleted
}

// runMovieScan scans a single already-resolved movie, reporting per-target
// found counts. The one shared movie skeleton: both POST /api/scan/movie/{id}
// and POST /api/scan/item (movie) run through it — the async conversion
// dissolved the sync/async split that used to justify two near-identical
// functions. op.st.Radarr is non-nil by construction (verified by the
// handler on this snapshot).
func (h *Handler) runMovieScan(ctx context.Context, stop <-chan struct{}, actID string,
	op *opState, movie *arrapi.Movie,
) activity.Outcome {
	label := fmt.Sprintf("%s (%d)", movie.Title, movie.Year)
	if movie.MovieFile == nil {
		slog.Warn("scan movie: movie has no file", "id", movie.ID)
		h.deps.Activity.Progress(actID, 0, 0, label+": movie has no file")
		return activity.OutcomeFailed
	}

	// Serialize with the other manual scans (see scanSingleEpisode).
	if outcome := h.acquireScanSlot(ctx, actID, stop); outcome != "" {
		slog.Debug("movie scan ended while queued", "movie_id", movie.ID, "outcome", outcome)
		return outcome
	}
	defer h.deps.ScanGuard.Release()

	origLang := api.OriginalLangCode(movie.OriginalLanguage)
	audioLangs := api.AudioLanguages(movie.MovieFile.MediaInfo)
	targets := op.st.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)
	total := len(targets)

	// Derive found from per-target outcomes: a movie with several language
	// targets can download several subtitles in one scan, and reporting the
	// single ScanFound outcome as 1/N understated that.
	_, found, _ := scanMovieDetail(ctx, op.deps, op.ls, movie, true)
	// Single-item scope: the item in flight is the whole scan — shutdown
	// FIRST, stop SECOND, before publishing success.
	if ctx.Err() != nil {
		return activity.OutcomeShutdown
	}
	if stopRequested(stop) {
		slog.Info("movie scan stopped", "media", label)
		return activity.OutcomeCancelled
	}
	slog.Info("movie scan complete",
		"media", label, "found", found, "targets", total)
	h.deps.Activity.Progress(actID, total, total,
		fmt.Sprintf("%s: %d/%d found", label, found, total))
	return activity.OutcomeCompleted
}
