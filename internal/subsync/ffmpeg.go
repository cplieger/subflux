// ffmpeg.go provides the high-level ExtractEmbeddedSRT function that depends
// on subsync's Cue type. Low-level ffmpeg/ffprobe operations live in the
// internal/subsync/ffmpeg subpackage.

package subsync

import (
	"bytes"
	"context"
	"log/slog"

	"subflux/internal/subsync/ffmpeg"
)

// LangMapper is a type alias for ffmpeg.LangMapper, preserving backward
// compatibility for consumers that reference subsync.LangMapper.
type LangMapper = ffmpeg.LangMapper

// ExtractEmbeddedSRT extracts text-based subtitle data from a video file
// and returns it as parsed SRT cues. Uses ffprobe for track detection and
// ffmpeg for subtitle extraction.
//
// Track selection priority:
//  1. Matching language (if lang is non-empty)
//  2. Prefer SRT/subrip over ASS/SSA
//  3. Skip forced tracks (sparse cues)
//  4. Skip tracks matching excludeLang
//
// Returns nil cues if no suitable track is found.
func ExtractEmbeddedSRT(ctx context.Context, videoPath, lang, excludeLang string, mapper ffmpeg.LangMapper) ([]Cue, error) {
	tracks, err := ffmpeg.ProbeStreams(ctx, videoPath, "subtitle")
	if err != nil {
		return nil, err
	}

	best := ffmpeg.SelectBestSubTrack(tracks, lang, excludeLang, mapper)
	if best == nil {
		return nil, nil
	}

	slog.Debug("extracting embedded subtitle",
		"stream", best.Index,
		"codec", best.CodecName,
		"lang", best.Language,
		"path", videoPath)

	// For ASS/SSA codecs, use native ASS extraction with style-based
	// dialogue filtering.
	if best.CodecName == ffmpeg.CodecASS || best.CodecName == ffmpeg.CodecSSA {
		dlgCues, _, err := ffmpegExtractASSDialogue(ctx, videoPath, best.Index)
		if err == nil && len(dlgCues) > 0 {
			slog.Debug("ASS dialogue extraction",
				"dialogue_cues", len(dlgCues))
			return dlgCues, nil
		}
		slog.Debug("ASS extraction failed, falling back to SRT",
			"error", err)
	}

	return extractEmbeddedSRT(ctx, videoPath, best.Index)
}

// extractEmbeddedSRT calls ffmpeg.ExtractRawSubtitle then parses the result.
func extractEmbeddedSRT(ctx context.Context, videoPath string, streamIndex int) ([]Cue, error) {
	raw, err := ffmpeg.ExtractRawSubtitle(ctx, videoPath, streamIndex)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}

	data := NormalizeEncoding(raw)
	cues, err := ParseSRT(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return cues, nil
}
