package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subtitlefile"
)

// SyncAndPostProcess syncs subtitle timing and normalizes content using
// the user's configured sync settings. It is the single entry point used by
// manual downloads and any caller that wants the full "match the auto path"
// behavior:
//
//  1. If sync_subtitles is enabled, try reference-based sync (embedded SRT
//     track extracted from the video container).
//  2. If that produces no offset and audio_sync_fallback is enabled, try
//     audio-based sync (PCM extraction + VAD correlation).
//  3. Apply the configured post-processing (HI strip, tag strip, encoding
//     normalization, line-ending normalization, whitespace cleanup).
//
// The variant argument selects the same StripHI policy as the auto path's
// postProcessSub: when the caller is saving an HI-variant file the global
// strip_hi setting is overridden to false, because the user deliberately
// asked for HI annotations. For any other variant (standard/forced) the
// configured strip_hi value is honored. Callers that need just the timing
// step without post-processing should use syncSubtitle directly.
func (e *Engine) SyncAndPostProcess(ctx context.Context, data []byte,
	videoPath, lang string, variant subflux.Variant,
) (synced []byte, offsetMs int64) {
	data, offsetMs = e.syncSubtitle(ctx, data, videoPath, lang, e.cfg.Sync())
	pp := e.cfg.PostProcess()
	if variant == subflux.VariantHI {
		pp.StripHI = false
	}
	return e.syncer.PostProcess(data, pp), offsetMs
}

func (e *Engine) syncSubtitle(ctx context.Context, data []byte, videoPath, lang string, cfg subflux.SyncConfig) (synced []byte, offsetMs int64) {
	if cfg.SyncSubtitles {
		synced, offsetMs = e.syncer.Sync(ctx, data, videoPath, lang)
		// A sync is applied when it produced a constant offset OR changed the
		// bytes: framerate correction and split-aware alignment adjust cues
		// per-segment and deliberately report Offset 0, so gating on the
		// offset alone would discard exactly those corrections. Audio
		// fallback runs only when reference sync changed nothing.
		if offsetMs != 0 || !bytes.Equal(synced, data) {
			return synced, offsetMs
		}
	}
	if cfg.AudioSyncFallback {
		return e.syncSubtitleAudio(ctx, data, videoPath)
	}
	return data, 0
}

// syncSubtitleAudio runs through the configured executor: in-process by
// default, or the sync-worker client in server mode, which process-isolates
// the memory-heavy PCM/alignment work.
func (e *Engine) syncSubtitleAudio(ctx context.Context, data []byte, videoPath string) (synced []byte, offsetMs int64) {
	result := e.syncExec.Audio(ctx, data, videoPath, "")
	if !result.ShouldApply() || result.Cues == nil {
		return data, 0
	}
	var buf bytes.Buffer
	if err := syncing.WriteSRT(&buf, result.Cues); err != nil {
		return data, 0
	}
	synced = buf.Bytes()
	if bytes.Equal(synced, data) {
		return data, 0
	}
	slog.Info("audio sync fallback applied",
		"method", result.Method,
		"offset_ms", result.Offset,
		"confidence", float64(result.Confidence))
	return synced, result.Offset
}

func (e *Engine) downloadAndSave(ctx context.Context, req *subflux.SearchRequest,
	best *scoredSub, videoPath string, mediaType subflux.MediaType, mediaID, lang string, variant subflux.Variant,
) (string, error) {
	slog.Debug("downloading subtitle",
		"media", logsafe.Field(req.MediaLabel()), "lang", lang,
		"provider", best.sub.Provider, "score", best.score,
		"release", logsafe.Field(best.sub.ReleaseName),
		"matched_by", best.sub.MatchedBy,
		"hi", best.sub.HearingImp)

	data, err := e.downloadFromProvider(ctx, &best.sub)
	if err != nil {
		return "", err
	}

	// Rejects a zero-byte body (a provider's empty 200) and a binary
	// archive a provider returned as-is when zip extraction failed (e.g.
	// RAR files from HDBits).
	if err := subtitlefile.Validate(data); err != nil {
		return "", fmt.Errorf("%w: %s: %w", errInvalidContent,
			best.sub.Provider, err)
	}

	// Skip sync for high-confidence matches: hash-matched subtitles are
	// timed for this exact file, and high-scoring release matches (same
	// group + source) are almost certainly from the same encode.
	// Skip sync for forced subtitles: too few cues (10-30 in a 2-hour
	// movie) for reliable alignment against a full reference or audio.
	var syncOffsetMs int64
	syncCfg := e.cfg.Sync()
	if syncCfg.SyncSubtitles &&
		best.sub.MatchedBy != subflux.MatchByHash &&
		best.score < syncSkipThreshold(e.cfg.Scores()) &&
		!best.sub.Forced {
		data, syncOffsetMs = e.syncSubtitle(ctx, data, videoPath, lang, syncCfg)
	}
	subPath, saveHI, data := e.postProcessSub(data, best, videoPath, lang, variant)

	if err := e.media.WriteFile(ctx, subPath, data); err != nil {
		return "", &saveError{err: err}
	}

	e.persistDownload(ctx, req, best, subPath, videoPath, mediaType, mediaID, lang, syncOffsetMs, saveHI)
	return subPath, nil
}

func (e *Engine) persistDownload(ctx context.Context, req *subflux.SearchRequest,
	best *scoredSub, subPath, videoPath string, mediaType subflux.MediaType, mediaID, lang string,
	syncOffsetMs int64, saveHI bool,
) {
	saveVariant := subtitlefile.VariantFromFlags(subtitlefile.Tags{HearingImpaired: saveHI, Forced: best.sub.Forced})
	if err := e.store.UpsertSubtitleFile(ctx, mediaType, mediaID, &subflux.SubtitleFile{
		Language: lang,
		Variant:  saveVariant,
		Source:   sourceExternal,
		Path:     subPath,
	}); err != nil {
		slog.Warn("failed to upsert subtitle file", "error", err)
	}

	if syncOffsetMs != 0 {
		if err := e.store.SetSyncOffset(ctx, subPath, syncOffsetMs); err != nil {
			slog.Warn("failed to record sync offset", "error", err)
		}
	}

	if e.metrics != nil {
		e.metrics.RecordSubtitleSaved(best.sub.Provider)
	}
	slog.Info("subtitle saved",
		"media", logsafe.Field(req.MediaLabel()), "media_type", mediaType,
		"lang", lang, "provider", best.sub.Provider,
		"score", best.score, "path", subPath)

	if err := e.store.SaveDownload(ctx, &subflux.DownloadRecord{
		MediaType:    mediaType,
		MediaID:      mediaID,
		Language:     lang,
		Variant:      saveVariant,
		ProviderName: best.sub.Provider,
		ReleaseName:  best.sub.ReleaseName,
		Path:         subPath,
		Score:        best.score,
		Meta: &subflux.DownloadMeta{
			Title:     req.Title,
			ImdbID:    req.ImdbID,
			Season:    req.Season,
			Episode:   req.Episode,
			VideoPath: videoPath,
		},
	}); err != nil {
		slog.Warn("failed to record success", "error", err)
	}
}

// saveError marks a failure of the subtitle write itself, as opposed to the
// download or the content check before it.
type saveError struct{ err error }

func (e *saveError) Error() string { return "save subtitle: " + e.err.Error() }
func (e *saveError) Unwrap() error { return e.err }

// postProcessSub returns the effective HI flag after strip-HI logic, which
// may differ from the input subtitle's HearingImp flag.
func (e *Engine) postProcessSub(data []byte, best *scoredSub,
	videoPath, lang string, variant subflux.Variant,
) (subPath string, saveHI bool, processed []byte) {
	pp := e.cfg.PostProcess()
	// A standard/forced target with strip_hi=false keeps the HI fallback
	// unstripped and records it as variant=hi rather than faking standard
	// coverage; the next scan keeps looking for a real standard sub.
	if variant == subflux.VariantHI {
		pp.StripHI = false
	}
	processed = e.syncer.PostProcess(data, pp)

	// The HI content has already been stripped, so the file is effectively
	// regular even though the best match was HI-flagged.
	saveHI = best.sub.HearingImp
	if pp.StripHI && variant != subflux.VariantHI {
		saveHI = false
	}
	subPath = subtitlefile.Path(videoPath, subtitlefile.Tags{Lang: lang, HearingImpaired: saveHI, Forced: best.sub.Forced})
	return subPath, saveHI, processed
}

// downloadBestCandidate tries candidates in rank order until one is saved.
// Up to DownloadMaxAttempts ordinary failures (an over-cap file included)
// are spent; a gated candidate costs none, and a refused credential or rate
// limit skips that provider's other candidates. An unwritable folder ends it
// as targetWriteBlocked, with the fault when this save learned it; any other
// write failure is the target path's, which no candidate can change, so it
// ends as targetDownloadFailed. A stop ends it the same way, with nothing
// logged, because no candidate failed.
func (e *Engine) downloadBestCandidate(ctx context.Context, req *subflux.SearchRequest,
	candidates []scoredSub, videoPath string, mediaType subflux.MediaType, mediaID, lang string, variant subflux.Variant, label string,
) (path string, res targetResult, writeFailure *mediawrite.UnwritableError) {
	maxAttempts := e.cfg.Search().DownloadMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = subflux.DefaultDownloadMaxAttempts
	}
	attempts := 0
	skipped := make(map[subflux.ProviderID]bool)
	for i := range candidates {
		if ctx.Err() != nil {
			return "", targetDownloadFailed, nil
		}
		if attempts == maxAttempts {
			break
		}
		c := &candidates[i]
		if skipped[c.sub.Provider] {
			continue
		}
		if _, blocked := e.media.Blocked(videoPath); blocked {
			return "", targetWriteBlocked, nil
		}
		path, err := e.downloadAndSave(ctx, req, c, videoPath, mediaType, mediaID, lang, variant)
		var save *saveError
		var unwritable *mediawrite.UnwritableError
		switch {
		case err == nil:
			return path, targetSaved, nil
		case ctx.Err() != nil:
			return "", targetDownloadFailed, nil
		case errors.As(err, &unwritable):
			return "", targetWriteBlocked, unwritable
		case errors.As(err, &save) && !errors.Is(err, atomicfile.ErrFileTooLarge):
			slog.Debug("subtitle not saved, target retried next cycle",
				"media", label, "lang", lang, "variant", variant, "error", err)
			return "", targetDownloadFailed, nil
		case errors.Is(err, ErrProviderGated):
			skipped[c.sub.Provider] = true
			slog.Debug("download candidate gated, trying next",
				"media", label, "lang", lang, "variant", variant,
				"provider", c.sub.Provider, "error", err)
		case gateOwned(err):
			skipped[c.sub.Provider] = true
			slog.Warn("download refused by provider, skipping its candidates",
				"media", label, "lang", lang, "variant", variant,
				"provider", c.sub.Provider, "error", err)
		default:
			attempts++
			slog.Warn("download attempt failed, trying next",
				"media", label, "lang", lang,
				"variant", variant,
				"provider", c.sub.Provider,
				"release", logsafe.Field(c.sub.ReleaseName),
				"score", c.score,
				"attempt", attempts, "remaining", min(maxAttempts-attempts, len(candidates)-i-1),
				"error", err)
		}
	}
	if attempts == 0 {
		slog.Info("no download candidate could be tried",
			"media", label, "lang", lang, "variant", variant,
			"candidates", len(candidates), "providers_skipped", slices.Sorted(maps.Keys(skipped)))
		return "", targetDownloadFailed, nil
	}
	slog.Error("all download attempts failed",
		"media", label, "lang", lang,
		"variant", variant,
		"candidates", len(candidates),
		"attempted", attempts)
	return "", targetDownloadFailed, nil
}
