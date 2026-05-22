// Package archive provides subtitle extraction from ZIP and RAR archives.
// It is the single source of truth for archive-related logic in the provider
// subsystem: format detection, subtitle extension validation, episode matching,
// and decompression-bomb guards.
package archive

import (
	"bytes"
	"log/slog"

	"subflux/internal/api"
)

// SubtitleExts lists file extensions recognized as subtitle formats.
// Includes .vtt which is not in search/existing.go's list because VTT files
// are only encountered inside archives, not as standalone files on disk from
// Sonarr/Radarr media libraries.
var SubtitleExts = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".sub": true,
	".vtt": true,
}

// MaxExtractSize is the maximum size of a single extracted subtitle file (5 MB).
const MaxExtractSize = 5 << 20

// Extract attempts to extract a subtitle file from an archive.
// Tries ZIP first, then RAR. If the data is not a recognized archive,
// returns it only if it looks like valid subtitle content (SRT timing
// arrows, ASS headers, or WebVTT signature). Returns nil for
// unrecognized binary data.
//
// When season > 0 and episode > 0, returns only files matching the target
// episode (S##E## pattern). Returns nil if no match is found.
func Extract(data []byte, season, episode int) []byte {
	// Fast path: if data looks like a subtitle and has no archive magic
	// bytes, skip archive probing entirely. Covers the common case where
	// providers return raw SRT/ASS content.
	if LooksLikeSubtitle(data) && !HasArchiveSignature(data) {
		return data
	}

	// Use magic bytes to skip irrelevant archive probes.
	switch {
	case hasZIPMagic(data):
		// Definitely ZIP — skip RAR probe entirely.
		if extracted := ExtractFromZip(data, season, episode); extracted != nil {
			return extracted
		}
	case hasRARMagic(data):
		// Definitely RAR — skip ZIP probe entirely.
		if extracted := ExtractFromRAR(data, season, episode); extracted != nil {
			return extracted
		}
	default:
		// Unknown magic — try both.
		if extracted := ExtractFromZip(data, season, episode); extracted != nil {
			return extracted
		}
		if extracted := ExtractFromRAR(data, season, episode); extracted != nil {
			return extracted
		}
	}

	// Not an archive. Only return raw data if it looks like a subtitle.
	if LooksLikeSubtitle(data) {
		return data
	}
	slog.Debug("archive extraction failed: no archive match and not a subtitle",
		"data_len", len(data), "season", season, "episode", episode)
	return nil
}

// HasArchiveSignature checks whether data starts with a known archive
// magic number (ZIP or RAR). Used to skip expensive archive probing when
// the data is clearly plain text.
func HasArchiveSignature(data []byte) bool {
	return hasZIPMagic(data) || hasRARMagic(data)
}

// hasZIPMagic returns true if data starts with the ZIP magic bytes (PK\x03\x04).
func hasZIPMagic(data []byte) bool {
	return len(data) >= 4 &&
		data[0] == 'P' && data[1] == 'K' && data[2] == 3 && data[3] == 4
}

// hasRARMagic returns true if data starts with the RAR magic bytes (Rar!\x1a\x07).
func hasRARMagic(data []byte) bool {
	return len(data) >= 6 &&
		data[0] == 'R' && data[1] == 'a' && data[2] == 'r' &&
		data[3] == '!' && data[4] == 0x1a && data[5] == 0x07
}

// subtitleSignatures are byte patterns that identify subtitle formats.
// Checked against the first 4KB of data after UTF-8 BOM stripping.
var subtitleSignatures = [][]byte{
	[]byte(" --> "),        // SRT timing line
	[]byte("[Script Info"), // ASS/SSA header
	[]byte("Dialogue:"),    // ASS/SSA dialogue (no header section)
	[]byte("WEBVTT"),       // WebVTT
}

// LooksLikeSubtitle checks whether data appears to be a text-based
// subtitle file by looking for format-specific signatures in the first
// 4KB. Also rejects data with high concentrations of non-text bytes.
func LooksLikeSubtitle(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	// Strip UTF-8 BOM if present.
	probe := data
	if bytes.HasPrefix(probe, []byte{0xEF, 0xBB, 0xBF}) {
		probe = probe[3:]
	}
	if len(probe) == 0 {
		return false
	}

	// Limit analysis to the first 4KB.
	if len(probe) > 4096 {
		probe = probe[:4096]
	}

	// Reject data with high concentrations of non-text bytes.
	if api.CountNonTextBytes(probe)*10 > len(probe) {
		return false
	}

	// Look for subtitle format signatures.
	for _, sig := range subtitleSignatures {
		if bytes.Contains(probe, sig) {
			return true
		}
	}
	return false
}
