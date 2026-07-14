package syncing

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
)

// SyncAgainstReference aligns subtitle timing against an embedded reference
// subtitle in the video container. This is the only strategy tried here;
// audio-based sync enters the auto path separately via the engine's
// audio_sync_fallback (see Engine.syncSubtitle), and external SRT sync is
// manual-only from the web UI.
func SyncAgainstReference(ctx context.Context, data []byte, videoPath, lang string, mapper subsync.LangMapper, minConf ...float64) subsync.SyncResult {
	threshold := float64(api.DefaultSyncMinConfidence)
	if len(minConf) > 0 && minConf[0] > 0 {
		threshold = minConf[0]
	}

	noChange := subsync.SyncResult{
		Cues:   nil,
		Method: subsync.MethodNone,
	}

	// Only strategy: embedded subtitle reference.
	refCues := ExtractEmbeddedReference(ctx, videoPath, lang, mapper)
	if len(refCues) < subsync.MinCuesForSync {
		return noChange
	}

	slog.Debug("sync: trying embedded reference",
		"video", filepath.Base(videoPath), "cues", len(refCues))
	result := SyncFromCues(ctx, data, refCues, videoPath)

	if float64(result.Confidence) < threshold {
		if result.Method != subsync.MethodNone {
			slog.Debug("sync: rejected low-confidence result",
				"video", filepath.Base(videoPath),
				"method", result.Method,
				"confidence", float64(result.Confidence),
				"threshold", threshold)
		}
		return noChange
	}
	return result
}

// SyncFromAudio runs audio-based sync on subtitle data using PCM extraction
// and onset correlation. Detects ASS format for native parsing, falls back
// to SRT otherwise.
func SyncFromAudio(ctx context.Context, data []byte, videoPath, subtitlePath string) subsync.SyncResult {
	noChange := subsync.SyncResult{Method: subsync.MethodNone}

	// Build hints from available metadata.
	var hints subsync.AudioSyncHints
	var isASS bool
	if subtitlePath != "" {
		ext := strings.ToLower(filepath.Ext(subtitlePath))
		isASS = ext == ExtASS || ext == ExtSSA
	} else {
		// No path available (automatic fallback); detect ASS by content.
		isASS = subsync.IsASSContent(data)
	}
	hints.IsASS = isASS

	// Get media duration for adaptive strategy selection.
	if durMs, err := ffmpeg.ProbeDuration(ctx, videoPath); err == nil {
		hints.DurationSec = int(durMs / 1000)
	}

	// Parse cues: use native ASS parser for ASS files, SRT parser otherwise.
	var incCues []subsync.Cue
	if isASS {
		dialogueCues, _, err := subsync.ParseASSDialogue(data)
		if err != nil || len(dialogueCues) < subsync.MinCuesForSync {
			slog.Debug("sync: ASS parse failed or too few cues, falling back to SRT",
				"error", err, "dialogue_cues", len(dialogueCues))
			// Fall back to SRT parsing.
			isASS = false
			hints.IsASS = false
		} else {
			hints.DialogueCues = dialogueCues
			incCues = dialogueCues
		}
	}
	if !isASS {
		data = subsync.NormalizeEncoding(data)
		parsed, err := subsync.ParseSRT(bytes.NewReader(data))
		if err != nil {
			slog.Debug("sync: cannot parse incoming SRT for audio sync", "error", err)
			return noChange
		}
		incCues = parsed
	}
	if len(incCues) < subsync.MinCuesForSync {
		slog.Debug("sync: incoming subtitle too short for audio sync", "cues", len(incCues))
		return noChange
	}

	opts := subsync.SyncOptions{
		VideoPath:     videoPath,
		EnableAudio:   true,
		MinConfidence: subsync.ShouldApplyThreshold,
		AudioHints:    hints,
	}
	result := subsync.SyncWithOptions(ctx, nil, incCues, &opts)

	slog.Debug("sync: audio strategy complete",
		"method", result.Method,
		"offset_ms", result.Offset,
		"confidence", float64(result.Confidence),
		"is_ass", isASS)

	return result
}

// ExtractEmbeddedReference extracts an embedded subtitle track from the
// video container to use as a sync reference, excluding the target language.
func ExtractEmbeddedReference(ctx context.Context, videoPath, excludeLang string, mapper subsync.LangMapper) []subsync.Cue {
	if videoPath == "" {
		return nil
	}
	cues, err := subsync.ExtractEmbeddedSRT(ctx, videoPath, "", excludeLang, mapper)
	if err != nil {
		slog.Debug("sync: embedded extraction failed",
			"video", filepath.Base(videoPath), "error", err)
		return nil
	}
	if len(cues) > 0 {
		slog.Debug("sync: extracted embedded subtitle",
			"video", filepath.Base(videoPath),
			"cues", len(cues))
	}
	return cues
}

// SyncFromCues aligns incoming SRT data against pre-parsed reference cues.
func SyncFromCues(ctx context.Context, data []byte, refCues []subsync.Cue, videoPath string) subsync.SyncResult {
	noChange := subsync.SyncResult{Method: subsync.MethodNone}

	incCues, err := subsync.ParseSRT(bytes.NewReader(data))
	if err != nil {
		slog.Debug("sync: cannot parse incoming SRT", "error", err)
		return noChange
	}
	if len(incCues) < subsync.MinCuesForSync {
		slog.Debug("sync: incoming subtitle too short", "cues", len(incCues))
		return noChange
	}

	opts := subsync.DefaultSyncOptions()
	opts.VideoPath = videoPath
	result := subsync.SyncWithOptions(ctx, refCues, incCues, &opts)

	slog.Debug("sync: strategy complete (embedded ref)",
		"method", result.Method,
		"offset_ms", result.Offset,
		"rate", result.Rate,
		"confidence", float64(result.Confidence),
		"ref_cues", len(refCues),
		"inc_cues", len(incCues))

	return result
}
