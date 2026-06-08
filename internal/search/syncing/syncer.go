// Package syncing provides subtitle timing synchronization for the search engine.
package syncing

import (
	"bytes"
	"context"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/subsync"
)

// Subtitle format extensions used for ASS/SSA detection.
const (
	ExtASS = ".ass"
	ExtSSA = ".ssa"
)

// SyncResult is re-exported from subsync for consumer convenience.
type SyncResult = subsync.SyncResult

// Syncer implements SubtitleSyncer using the subsync library.
type Syncer struct {
	// LangMapper maps ISO 639-3 codes to ISO 639-1 for ffprobe track selection.
	LangMapper subsync.LangMapper

	// MinConfidence is the minimum confidence threshold for auto-sync.
	// When zero, defaults to api.DefaultSyncMinConfidence (0.6).
	MinConfidence float64
}

// Sync adjusts subtitle timing against a reference. Returns the

// WriteSRT serializes cues to SRT format. Delegates to subsync.WriteSRT.
func WriteSRT(buf *bytes.Buffer, cues []subsync.Cue) error {
	return subsync.WriteSRT(buf, cues)
}

// Sync returns the (possibly modified) data and the applied offset in milliseconds.
func (s Syncer) Sync(ctx context.Context, data []byte, videoPath, lang string) (synced []byte, offsetMs int64) {
	result := SyncAgainstReference(ctx, data, videoPath, lang, s.LangMapper, s.MinConfidence)
	if !result.Applied() || result.Cues == nil {
		return data, 0
	}
	var buf bytes.Buffer
	if err := subsync.WriteSRT(&buf, result.Cues); err != nil {
		slog.Warn("sync: failed to write adjusted SRT, returning original",
			"error", err, "cues", len(result.Cues))
		return data, 0
	}
	synced = buf.Bytes()
	if bytes.Equal(synced, data) {
		return data, 0
	}
	slog.Info("subtitle timing adjusted",
		"method", result.Method,
		"offset_ms", result.Offset,
		"rate", result.Rate,
		"confidence", float64(result.Confidence),
		"cues", len(result.Cues))
	return synced, result.Offset
}

// PostProcess applies encoding normalization, HI removal, tag stripping, etc.
func (Syncer) PostProcess(data []byte, pp api.PostProcessConfig) []byte {
	opts := subsync.PostProcessOptions{
		StripHI:              pp.StripHI,
		StripTags:            pp.StripTags,
		NormalizeEncoding:    pp.NormalizeUTF8,
		NormalizeLineEndings: pp.NormalizeEndings,
		CleanWhitespace:      pp.CleanWhitespace,
		RemoveEmpty:          pp.RemoveEmpty,
	}

	sizeBefore := len(data)

	data = subsync.PostProcessBytes(data, opts)
	slog.Debug("post-process: byte-level complete",
		"normalize_encoding", opts.NormalizeEncoding,
		"normalize_endings", opts.NormalizeLineEndings,
		"size_before", sizeBefore, "size_after", len(data))

	if opts.StripHI || opts.StripTags || opts.CleanWhitespace || opts.RemoveEmpty {
		cues, err := subsync.ParseSRT(bytes.NewReader(data))
		if err != nil || len(cues) == 0 {
			slog.Debug("post-process: skipping cue-level (parse failed or empty)",
				"error", err)
			return data
		}
		cuesBefore := len(cues)
		cues = subsync.PostProcess(cues, opts)
		slog.Debug("post-process: cue-level complete",
			"strip_hi", opts.StripHI, "strip_tags", opts.StripTags,
			"clean_ws", opts.CleanWhitespace, "remove_empty", opts.RemoveEmpty,
			"cues_before", cuesBefore, "cues_after", len(cues))
		var buf bytes.Buffer
		if err := subsync.WriteSRT(&buf, cues); err != nil {
			slog.Warn("post-process: failed to write SRT after cue-level processing",
				"error", err, "cues", len(cues))
			return data
		}
		data = buf.Bytes()
		// Apply only line-ending normalization on WriteSRT output (already UTF-8/clean).
		if opts.NormalizeLineEndings {
			data = subsync.PostProcessBytes(data, subsync.PostProcessOptions{
				NormalizeLineEndings: true,
			})
		}
	}

	slog.Debug("post-processing complete",
		"size_before", sizeBefore, "size_after", len(data))
	return data
}
