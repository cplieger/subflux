package api

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// --- Subtitle path helpers ---

// SubtitleExtSRT is the extension every subflux writer emits. The
// capability-scoped extension authority lives in internal/subtitleext (api
// stays stdlib-only); its coverage test pins this constant to the
// writerOutput view so the two cannot drift.
const SubtitleExtSRT = ".srt"

// SubtitlePath computes the subtitle file path for a video.
func SubtitlePath(videoPath, lang string, hi, forced bool) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	suffix := lang
	if hi {
		suffix += ".hi"
	}
	if forced {
		suffix += ".forced"
	}
	return base + "." + suffix + SubtitleExtSRT
}

// ManualSubtitlePath computes a numbered manual subtitle path.
// e.g., movie.fr.1.srt, movie.fr.2.srt for regular subs; movie.fr.hi.1.srt
// or movie.fr.forced.1.srt when the user deliberately downloaded an HI or
// forced variant. The variant tag appears before the number so
// parseExternalSubPath continues to recognize it on the next scan.
func ManualSubtitlePath(videoPath, lang string, n int, hi, forced bool) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	suffix := lang
	if hi {
		suffix += ".hi"
	}
	if forced {
		suffix += ".forced"
	}
	return fmt.Sprintf("%s.%s.%d"+SubtitleExtSRT, base, suffix, n)
}

// ManualOrdinal parses the manual sibling number out of a subtitle path
// produced by ManualSubtitlePath: the all-digit dot segment immediately
// before the extension (movie.fr.2.srt -> 2, movie.fr.forced.1.srt -> 1).
// An unnumbered path (the auto file, movie.fr.srt) returns 0. This is the
// inverse of ManualSubtitlePath's numbering and the ordinal component of the
// wire FileRef: together with (media_type, media_id, language, variant,
// source) it uniquely addresses one stored subtitle file, so manual numbered
// siblings sharing a quad stay distinguishable without a client-supplied
// path.
func ManualOrdinal(path string) int {
	base := strings.TrimSuffix(path, filepath.Ext(path))
	i := strings.LastIndex(base, ".")
	if i < 0 {
		return 0
	}
	seg := base[i+1:]
	if seg == "" {
		return 0
	}
	for _, r := range seg {
		if r < '0' || r > '9' {
			return 0
		}
	}
	n, err := strconv.Atoi(seg)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
