package search

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	"subflux/internal/api"
	"subflux/internal/search/syncing"
)

// SyncAndPostProcess syncs subtitle timing and normalizes content using
// the user's configured SyncConfig. It is the single entry point used by
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
	videoPath, lang string, variant api.Variant) (synced []byte, offsetMs int64) {

	data, offsetMs = e.syncSubtitle(ctx, data, videoPath, lang, e.cfg.SyncConfig())
	pp := e.cfg.PostProcessConfig()
	if variant == api.VariantHI {
		pp.StripHI = false
	}
	return e.syncer.PostProcess(data, pp), offsetMs
}

func (e *Engine) syncSubtitle(ctx context.Context, data []byte, videoPath, lang string, cfg api.SyncConfig) (synced []byte, offsetMs int64) {
	if cfg.SyncSubtitles {
		synced, offsetMs = e.syncer.Sync(ctx, data, videoPath, lang)
		if offsetMs != 0 {
			return synced, offsetMs
		}
	}
	if cfg.AudioSyncFallback {
		return e.syncSubtitleAudio(ctx, data, videoPath)
	}
	return data, 0
}

// syncSubtitleAudio runs audio-based sync on subtitle data.
func (e *Engine) syncSubtitleAudio(ctx context.Context, data []byte, videoPath string) (synced []byte, offsetMs int64) {
	result := syncing.SyncFromAudio(ctx, data, videoPath, "")
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

// downloadAndSave downloads the best subtitle, syncs timing, post-processes,
// saves to disk, and records success in the store.
func (e *Engine) downloadAndSave(ctx context.Context, req *api.SearchRequest,
	best *scoredSub, videoPath string, mediaType api.MediaType, mediaID, lang string, variant api.Variant) (string, error) {

	slog.Debug("downloading subtitle",
		"media", req.MediaLabel(), "lang", lang,
		"provider", best.sub.Provider, "score", best.score,
		"release", best.sub.ReleaseName,
		"matched_by", best.sub.MatchedBy,
		"hi", best.sub.HearingImp)

	data, err := e.downloadFromProvider(ctx, &best.sub)
	if err != nil {
		return "", err
	}

	if len(data) == 0 {
		slog.Warn("downloaded empty subtitle data",
			"media", req.MediaLabel(), "lang", lang,
			"provider", best.sub.Provider)
		return "", fmt.Errorf("%w: %s", ErrEmptyResponse, best.sub.Provider)
	}

	// Reject binary archives that providers returned as-is when zip
	// extraction failed (e.g. RAR files from HDBits).
	if err := api.ValidateSubtitleData(data); err != nil {
		slog.Warn("downloaded data is not a subtitle file",
			"media", req.MediaLabel(), "lang", lang,
			"provider", best.sub.Provider, "error", err)
		return "", fmt.Errorf("%w: %s: %w", ErrInvalidContent,
			best.sub.Provider, err)
	}

	// Skip sync for high-confidence matches: hash-matched subtitles are
	// timed for this exact file, and high-scoring release matches (same
	// group + source) are almost certainly from the same encode.
	// Skip sync for forced subtitles: too few cues (10-30 in a 2-hour
	// movie) for reliable alignment against a full reference or audio.
	var syncOffsetMs int64
	syncCfg := e.cfg.SyncConfig()
	if syncCfg.SyncSubtitles &&
		best.sub.MatchedBy != api.MatchByHash &&
		best.score < syncSkipThreshold(e.cfg.Scores()) &&
		!best.sub.Forced {
		data, syncOffsetMs = e.syncSubtitle(ctx, data, videoPath, lang, syncCfg)
	}
	subPath, saveHI, data := e.postProcessSub(data, best, videoPath, lang, variant)

	if err := e.fileWriter.WriteFile(ctx, subPath, data); err != nil {
		return "", err
	}

	e.persistDownload(ctx, req, best, subPath, videoPath, mediaType, mediaID, lang, syncOffsetMs, saveHI)
	return subPath, nil
}

// persistDownload records the subtitle in the store and updates coverage.
func (e *Engine) persistDownload(ctx context.Context, req *api.SearchRequest,
	best *scoredSub, subPath, videoPath string, mediaType api.MediaType, mediaID, lang string,
	syncOffsetMs int64, saveHI bool) {

	saveVariant := api.VariantFromFlags(saveHI, best.sub.Forced)
	if err := e.store.UpsertSubtitleFile(ctx, mediaType, mediaID, &api.SubtitleFile{
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

	slog.Info("subtitle saved",
		"media", req.MediaLabel(), "media_type", mediaType,
		"lang", lang, "provider", best.sub.Provider,
		"score", best.score, "path", subPath)

	if err := e.store.SaveDownload(ctx, &api.DownloadRecord{
		MediaType:    mediaType,
		MediaID:      mediaID,
		Language:     lang,
		ProviderName: best.sub.Provider,
		ReleaseName:  best.sub.ReleaseName,
		Path:         subPath,
		Score:        best.score,
		Meta: &api.DownloadMeta{
			Title:      req.Title,
			ImdbID:     req.ImdbID,
			Season:     req.Season,
			Episode:    req.Episode,
			ReleaseTag: req.ReleaseName,
			VideoPath:  videoPath,
		},
	}); err != nil {
		slog.Warn("failed to record success", "error", err)
	}
}

// postProcessSub applies variant-aware post-processing and determines the
// save path. Returns the path, the effective HI flag (after strip-HI logic),
// and the processed data.
func (e *Engine) postProcessSub(data []byte, best *scoredSub,
	videoPath, lang string, variant api.Variant) (subPath string, saveHI bool, processed []byte) {

	pp := e.cfg.PostProcessConfig()
	// Strip HI behavior depends on the target variant:
	// - hi variant target: never strip (user explicitly wants HI annotations).
	// - standard/forced target: honor the global strip_hi setting, even if
	//   the best match is an HI fallback. When strip_hi=true the stripped
	//   file is named movie.lang.srt and recorded as variant=standard,
	//   satisfying the target. When strip_hi=false the user has explicitly
	//   asked to preserve HI annotations, so the file is named
	//   movie.lang.hi.srt and recorded as variant=hi; the standard target
	//   coverage stays empty and the next scan keeps looking for a real
	//   standard sub. Respecting strip_hi beats phantom coverage.
	if variant == api.VariantHI {
		pp.StripHI = false
	}
	processed = e.syncer.PostProcess(data, pp)

	// File naming: when strip_hi is enabled and the target is standard
	// or forced, save without .hi even if the best match was HI-flagged
	// (the HI content has been stripped, so the file is effectively regular).
	saveHI = best.sub.HearingImp
	if pp.StripHI && variant != api.VariantHI {
		saveHI = false
	}
	subPath = api.SubtitlePath(videoPath, lang, saveHI, best.sub.Forced)
	return subPath, saveHI, processed
}

// downloadBestCandidate tries candidates in score order with a bounded number
// of attempts. Returns the saved path or empty string if all attempts fail.
func (e *Engine) downloadBestCandidate(ctx context.Context, req *api.SearchRequest,
	candidates []scoredSub, videoPath string, mediaType api.MediaType, mediaID, lang string, variant api.Variant, label string) string {

	maxAttempts := e.cfg.Search().DownloadMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = api.DefaultDownloadMaxAttempts
	}
	limit := min(len(candidates), maxAttempts)
	for i := range limit {
		if ctx.Err() != nil {
			return ""
		}
		path, err := e.downloadAndSave(ctx, req, &candidates[i],
			videoPath, mediaType, mediaID, lang, variant)
		if err != nil {
			slog.Warn("download attempt failed, trying next",
				"media", label, "lang", lang,
				"variant", variant,
				"provider", candidates[i].sub.Provider,
				"score", candidates[i].score,
				"attempt", i+1, "remaining", limit-i-1,
				"error", err)
			continue
		}
		return path
	}
	slog.Error("all download attempts failed",
		"media", label, "lang", lang,
		"variant", variant,
		"candidates", len(candidates),
		"attempted", limit)
	return ""
}
