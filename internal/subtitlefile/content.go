package subtitlefile

import (
	"bytes"
	"errors"
	"fmt"
)

// content.go answers one question about a downloaded blob: is this subtitle
// text, or is it a binary archive an extraction step failed to unpack and
// returned as-is? Every provider download and both save paths ask it.

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

// ErrEmpty indicates the download carried no bytes at all. Exported because
// it is the one refusal a caller must be able to tell apart: a zero-byte body
// is an absent subtitle, not a malformed one, so blaming the archive format in
// the operator-facing text would misdiagnose it.
var ErrEmpty = errors.New("empty subtitle data")

// errBinary indicates the downloaded data is a binary archive that could not be
// extracted, not a subtitle file. Unexported: ErrEmpty is the only refusal a
// caller distinguishes, so this one is its residue — no consumer branches on it.
var errBinary = errors.New("binary archive data, not a subtitle")

// Validate checks whether data is subtitle text: non-empty, and not a binary
// archive. Returns ErrEmpty for a zero-length payload and errBinary when the
// data matches a known archive magic signature or has too many non-text bytes.
//
// Rejecting empty is a contract, not a convenience: every caller is a download
// boundary, and accepting zero bytes turns a provider's empty 200 into a
// successful download of no subtitle — a file on disk and a coverage row that
// both claim a subtitle nothing can read.
func Validate(data []byte) error {
	if len(data) == 0 {
		return ErrEmpty
	}

	for _, m := range knownArchiveMagic {
		if bytes.HasPrefix(data, m.magic) {
			return fmt.Errorf("%w: detected %s archive", errBinary, m.name)
		}
	}

	// Check that the first 512 bytes are mostly printable text.
	// Subtitle files (SRT, ASS, VTT) are text; binary archives have
	// high concentrations of non-printable bytes.
	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	nonText := CountNonText(check)
	// More than 10% non-text bytes in the first 512 bytes is suspicious.
	if nonText*10 > len(check) {
		return fmt.Errorf("%w: %d/%d non-text bytes in header",
			errBinary, nonText, len(check))
	}

	return nil
}

// CountNonText returns the number of bytes in data that are not
// printable text (control characters below TAB, or between CR and SPACE,
// excluding ESC). Used by Validate and archive extraction.
func CountNonText(data []byte) int {
	var n int
	for _, b := range data {
		if b < 0x09 || (b > 0x0D && b < 0x20 && b != 0x1B) {
			n++
		}
	}
	return n
}
