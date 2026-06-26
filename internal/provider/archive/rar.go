package archive

import (
	"bytes"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// maxRAREntries caps how many entries FindRARSubtitle will inspect before
// giving up. Bounds worst-case iteration cost on pathological archives.
const maxRAREntries = 4096

// ExtractFromRAR attempts to extract a subtitle file from a RAR archive.
// Supports both RAR4 and RAR5 formats. When season > 0 and episode > 0,
// returns only files matching the target episode (S##E## pattern);
// returns nil if no match is found (no fallback to unmatched files).
// Without episode context, returns the first valid subtitle entry.
// Returns nil if data is not a valid RAR, contains no subtitles, or
// the extracted content exceeds MaxExtractSize (5 MB).
func ExtractFromRAR(data []byte, season, episode int) []byte {
	return findRARSubtitle(data, season, episode)
}

// findRARSubtitle streams through a RAR archive and returns the first
// subtitle matching the target episode, avoiding decompression of
// additional entries.
func findRARSubtitle(data []byte, season, episode int) []byte {
	r, err := rardecode.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}

	episodeCtx := season > 0 && episode > 0
	for range maxRAREntries {
		hdr, err := r.Next()
		if err != nil {
			break
		}
		if !IsValidRAREntry(hdr) {
			continue
		}
		if episodeCtx && !MatchesEpisode(hdr.Name, season, episode) {
			continue
		}
		if content := readRARContent(r); content != nil {
			return content
		}
		// Content was empty or oversized. With episode context the matching
		// entry has already been found, so stop; otherwise keep scanning.
		if episodeCtx {
			return nil
		}
	}
	slog.Debug("rar iteration stopped",
		"episode_ctx", episodeCtx, "season", season, "episode", episode)
	return nil
}

// readRARContent reads the current entry's content with the decompression-bomb
// cap. Returns nil if the read fails or the content is empty or exceeds
// MaxExtractSize.
func readRARContent(r io.Reader) []byte {
	content, err := io.ReadAll(io.LimitReader(r, MaxExtractSize+1))
	if err != nil || len(content) == 0 || len(content) > MaxExtractSize {
		return nil
	}
	return content
}

// IsValidRAREntry checks if a RAR header represents a valid subtitle file.
// Includes a decompression bomb guard consistent with the ZIP extraction path.
func IsValidRAREntry(hdr *rardecode.FileHeader) bool {
	if hdr.IsDir {
		return false
	}
	if hdr.UnKnownSize || hdr.UnPackedSize < 0 {
		return false
	}
	if hdr.PackedSize == 0 && hdr.UnPackedSize > 0 {
		return false
	}
	if hdr.PackedSize > 0 && hdr.UnPackedSize/hdr.PackedSize > 50 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(hdr.Name))
	if !SubtitleExts[ext] {
		return false
	}
	if strings.HasPrefix(filepath.Base(hdr.Name), ".") {
		return false
	}
	return true
}
