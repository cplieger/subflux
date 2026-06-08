package search

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/cplieger/subflux/internal/api"
)

// --- Detection ---

// detectExisting scans for existing subtitles around a video file.
// Uses native MKV/MP4 parsing for embedded sub detection (with HI/forced
// flags) and filesystem glob for external subtitle files.
func detectExisting(ctx context.Context, videoPath string, detector TrackDetector, ignoredCodecs map[string]bool) existingSubs {
	var result existingSubs
	result.IgnoredCodecs = ignoredCodecs

	if videoPath == "" {
		return result
	}

	detectEmbeddedTracks(ctx, videoPath, detector, &result)
	scanExternalSubs(videoPath, &result)

	return result
}

// detectEmbeddedTracks runs the track detector and populates result.Embedded.
func detectEmbeddedTracks(ctx context.Context, videoPath string, detector TrackDetector, result *existingSubs) {
	tracks := detector.DetectTracks(ctx, videoPath)
	for _, t := range tracks {
		result.Embedded = append(result.Embedded, embeddedSub{
			Lang:   t.Lang,
			HI:     t.HearingImpaired,
			Forced: t.Forced,
			Codec:  t.Codec,
		})
	}
	if len(tracks) > 0 {
		langs := make([]string, len(tracks))
		for i, t := range tracks {
			tag := t.Lang
			if t.HearingImpaired {
				tag += "(hi)"
			}
			if t.Forced {
				tag += "(forced)"
			}
			langs[i] = tag
		}
		slog.Debug("embedded tracks found",
			"count", len(tracks), "langs", langs)
	}
}

// subtitleExtSet references the canonical set from the api package.
var subtitleExtSet = api.SubtitleExtsOnDisk

// scanExternalSubs finds external subtitle files on disk and populates result.External.
func scanExternalSubs(videoPath string, result *existingSubs) {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	escapedBase := globEscape(base)
	pattern := escapedBase + ".*"
	allMatches, err := filepath.Glob(pattern)
	if err != nil {
		slog.Debug("glob pattern error", "pattern", pattern, "error", err)
		return
	}
	for _, match := range allMatches {
		ext := strings.ToLower(filepath.Ext(match))
		if !subtitleExtSet[ext] {
			continue
		}
		sub := parseExternalSubPath(match, base, ext)
		if sub.Lang != "" {
			result.External = append(result.External, sub)
		}
	}
	if len(result.External) > 0 {
		langs := make([]string, len(result.External))
		for i, s := range result.External {
			tag := s.Lang
			if s.HI {
				tag += "(hi)"
			}
			if s.Forced {
				tag += "(forced)"
			}
			langs[i] = tag
		}
		slog.Debug("external subtitles found",
			"count", len(result.External), "langs", langs)
	}
}

// --- Subtitle file conversion for coverage tracking ---

// existingToSubtitleFiles converts detected subtitles into the flat
// SubtitleFile records stored in the DB for coverage tracking.
func existingToSubtitleFiles(existing existingSubs) []api.SubtitleFile {
	type embKey struct {
		lang    string
		variant api.Variant
		codec   string
	}
	seenEmb := make(map[embKey]bool)
	out := make([]api.SubtitleFile, 0,
		len(existing.Embedded)+len(existing.External))
	for _, emb := range existing.Embedded {
		k := embKey{emb.Lang, api.VariantFromFlags(emb.HI, emb.Forced), emb.Codec}
		if seenEmb[k] {
			continue
		}
		seenEmb[k] = true
		out = append(out, api.SubtitleFile{
			Language: k.lang,
			Variant:  k.variant,
			Source:   api.SourceEmbedded,
			Codec:    emb.Codec,
		})
	}
	for _, ext := range existing.External {
		out = append(out, api.SubtitleFile{
			Language: ext.Lang,
			Variant:  api.VariantFromFlags(ext.HI, ext.Forced),
			Source:   sourceExternal,
			Path:     ext.Path,
		})
	}
	return out
}
