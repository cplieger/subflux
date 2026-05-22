package archive

import (
	"archive/zip"
	"bytes"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// maxZipEntries caps how many central-directory entries a zip may declare.
// A crafted archive can advertise millions of stub entries and force Go's
// zip.Reader to allocate a large []*File. The cap bounds worst-case memory
// without affecting real subtitle packs (largest observed: ~30 files).
const maxZipEntries = 4096

// ExtractFromZip attempts to extract a subtitle file from a zip archive.
// When season > 0 and episode > 0, returns only files matching the target
// episode (S##E## pattern); returns nil if no match is found (no fallback
// to unmatched files). Without episode context, returns the first subtitle.
// Returns nil if data is not a valid zip, contains no subtitles,
// or the matching subtitle exceeds 5 MB.
// Rejects zip bombs (uncompressed > 50x compressed, or zero compressed with
// non-zero uncompressed) and caps extracted content at 5 MB.
func ExtractFromZip(data []byte, season, episode int) []byte {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil
	}

	// Guard against archives with an implausibly large central directory.
	if len(r.File) > maxZipEntries {
		return nil
	}

	// Collect all valid subtitle entries.
	var candidates []*zip.File
	for _, f := range r.File {
		if IsValidSubtitleEntry(f) {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	// If episode context provided, try to find a matching file.
	if season > 0 && episode > 0 {
		for _, f := range candidates {
			if MatchesEpisode(f.Name, season, episode) {
				if content := ReadZipEntry(f); content != nil {
					return content
				}
			}
		}
		// No episode match found.
		return nil
	}

	// Fallback: first valid subtitle.
	return ReadZipEntry(candidates[0])
}

// IsValidSubtitleEntry checks if a zip entry is a valid subtitle file,
// applying extension, hidden file, and zip bomb checks.
func IsValidSubtitleEntry(f *zip.File) bool {
	ext := strings.ToLower(filepath.Ext(f.Name))
	if !SubtitleExts[ext] {
		return false
	}
	if strings.HasPrefix(filepath.Base(f.Name), ".") {
		return false
	}
	if f.CompressedSize64 == 0 && f.UncompressedSize64 > 0 {
		return false
	}
	if f.CompressedSize64 > 0 &&
		f.UncompressedSize64/f.CompressedSize64 > 50 {
		return false
	}
	return true
}

// ReadZipEntry reads and returns the content of a zip entry, capped at 5 MB.
func ReadZipEntry(f *zip.File) []byte {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	content, err := io.ReadAll(io.LimitReader(rc, MaxExtractSize+1))
	_ = rc.Close()
	if err != nil || len(content) == 0 || len(content) > MaxExtractSize {
		return nil
	}
	return content
}

// episodeRe matches S##E## patterns in filenames.
var episodeRe = regexp.MustCompile(`(?i)S(\d+)E(\d+)`)

// multiEpRe matches multi-episode ranges like E01E02, E01-E02, E01-02.
// Requires either a second E prefix or a separator (- or .) between episode
// numbers to avoid matching single episodes (e.g. E05 as range [0,5]).
var multiEpRe = regexp.MustCompile(`(?i)E(\d+)(?:[-.]E?|E)(\d+)`)

// MatchesMultiEpisodeRange checks if a filename contains a multi-episode
// range (E01E02, E01-E02, E01-02, E01.E02) that includes the target episode.
func MatchesMultiEpisodeRange(base string, episode int) bool {
	for _, m := range multiEpRe.FindAllStringSubmatch(base, -1) {
		ep1, err1 := strconv.Atoi(m[1])
		ep2, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil {
			continue
		}
		// Reject false positives from year numbers in titles.
		if ep2 > 999 || ep2-ep1 > 50 {
			continue
		}
		if episode >= ep1 && episode <= ep2 {
			return true
		}
	}
	return false
}

// MatchesEpisode checks if a filename contains the target season+episode.
// Handles multi-episode files (S01E01E02, S01E01-E02, S01E01-02).
func MatchesEpisode(name string, season, episode int) bool {
	base := filepath.Base(name)

	// Single pass: check standard S##E## and track whether the season matches.
	seasonMatched := false
	for _, m := range episodeRe.FindAllStringSubmatch(base, -1) {
		s, sErr := strconv.Atoi(m[1])
		e, eErr := strconv.Atoi(m[2])
		if sErr != nil || eErr != nil {
			continue
		}
		if s == season {
			seasonMatched = true
			if e == episode {
				return true
			}
		}
	}

	// Check multi-episode ranges only if the season matched but the
	// exact episode didn't.
	if !seasonMatched {
		return false
	}
	return MatchesMultiEpisodeRange(base, episode)
}
