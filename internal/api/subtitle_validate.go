package api

import (
	"bytes"
	"errors"
	"fmt"
)

// --- Subtitle validation (pure functions, no dependencies) ---

// knownArchiveMagic maps archive format names to their magic byte prefixes.
// Used to detect binary archive data that was returned as-is when zip
// extraction failed (e.g. RAR files from HDBits).
var knownArchiveMagic = []struct {
	name  string
	magic []byte
}{
	{"rar4", []byte("Rar!\x1a\x07\x00")},
	{"rar5", []byte("Rar!\x1a\x07\x01\x00")},
	{"7z", []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}},
	{"gzip", []byte{0x1f, 0x8b}},
	{"xz", []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}},
	{"bzip2", []byte("BZh")},
	{"zip", []byte("PK\x03\x04")},
	{"zip-empty", []byte("PK\x05\x06")},
}

// ErrBinaryData indicates the downloaded data is a binary archive that
// could not be extracted, not a subtitle file.
var ErrBinaryData = errors.New("binary archive data, not a subtitle")

// ValidateSubtitleData checks whether data looks like subtitle text rather
// than a binary archive. Returns ErrBinaryData if the data matches a known
// archive magic signature or has too many non-text bytes.
func ValidateSubtitleData(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	for _, m := range knownArchiveMagic {
		if bytes.HasPrefix(data, m.magic) {
			return fmt.Errorf("%w: detected %s archive", ErrBinaryData, m.name)
		}
	}

	// Check that the first 512 bytes are mostly printable text.
	// Subtitle files (SRT, ASS, VTT) are text; binary archives have
	// high concentrations of non-printable bytes.
	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	nonText := CountNonTextBytes(check)
	// More than 10% non-text bytes in the first 512 bytes is suspicious.
	if nonText*10 > len(check) {
		return fmt.Errorf("%w: %d/%d non-text bytes in header",
			ErrBinaryData, nonText, len(check))
	}

	return nil
}

// CountNonTextBytes returns the number of bytes in data that are not
// printable text (control characters below TAB, or between CR and SPACE,
// excluding ESC). Used by ValidateSubtitleData and archive extraction.
func CountNonTextBytes(data []byte) int {
	var n int
	for _, b := range data {
		if b < 0x09 || (b > 0x0D && b < 0x20 && b != 0x1B) {
			n++
		}
	}
	return n
}
