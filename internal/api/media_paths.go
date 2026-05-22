package api

import (
	"fmt"
	"path/filepath"
	"strings"
)

// --- Subtitle path helpers ---

// SubtitleExtSRT is the default subtitle file extension.
const SubtitleExtSRT = ".srt"

// SubtitleExtsOnDisk is the set of subtitle extensions recognized as
// standalone files on disk (from Sonarr/Radarr libraries). This excludes
// .vtt which only appears inside archives.
var SubtitleExtsOnDisk = map[string]bool{
	".srt": true,
	".ass": true,
	".ssa": true,
	".sub": true,
}

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
