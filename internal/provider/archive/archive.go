// Package archive extracts a subtitle from a ZIP or RAR container.
//
// One export does the work: Extract. Everything else here is the machinery it
// needs, unexported on purpose, because a symbol only this package's tests reach
// is surface nobody has to maintain.
//
// The shape is three parts, and the split is what keeps the containers from
// disagreeing. Each container supplies a SEQUENCE of members (zip.go, rar.go).
// One selection policy walks any such sequence (member.go): the entry gate, the
// episode match, and the classification of a miss. Extract picks which container
// to open and decides what a refusal means to a caller.
//
// Which member answers for which episode is epmarker's judgement, not this
// package's. Only the filepath.Base narrowing is local, because a directory
// named for a season must not answer for a member inside it.
package archive

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/cplieger/runesafe/v2"
	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/subtitleenc"
	"github.com/cplieger/subflux/internal/subtitlefile"
)

// Archive-member extension acceptance comes from the subtitle-extension
// authority's archiveInput capability view (internal/subtitleext), which
// includes .vtt: VTT files are only encountered inside archives, not as
// standalone files on disk from Sonarr/Radarr media libraries.

// maxExtractSize is the maximum size of a single extracted subtitle file (5 MB).
const maxExtractSize = 5 << 20

// maxNamesInError bounds how many member names a refusal lists, and maxNameBytes
// bounds each one. A real pack holds around 30 members and a crafted one up to
// maxZipEntries, so the whole list has no place in a log attribute; the first few
// show the pack's numbering scheme, which is what the reader needs.
const (
	maxNamesInError = 8
	maxNameBytes    = 128
)

// ErrNotArchive reports that data is not a container this package can open: no
// known archive magic, or a reader that could not parse it. It is the one
// refusal a caller branches on, because it is the one where the raw bytes are
// still the whole story and subtitlefile.Validate is what names them. Every
// other refusal here describes an archive that DID open, so reporting it beats
// blaming the container.
var ErrNotArchive = errors.New("not a readable zip or rar archive")

// Extract returns the subtitle for want from an archive.
//
// Tries ZIP first, then RAR, using magic bytes to skip a probe that cannot
// match. If the data is not a recognized archive, returns it only when it looks
// like subtitle content, which is how a provider that hands back a bare SRT is
// served by the same call.
//
// A fixed target refuses rather than falling back to an unmatched member, so a
// season pack cannot yield the wrong episode. An epmarker.Any target takes the
// first readable subtitle, which is what a movie download wants.
//
// A nil error means the returned bytes are the answer. An error matching
// ErrNotArchive means nothing here could read the data; any other error describes
// an archive that opened and had no subtitle for this target.
func Extract(data []byte, want epmarker.Target) ([]byte, error) {
	// Fast path: data that already looks like a subtitle and carries no archive
	// magic needs no probing. Covers the common case of a raw SRT or ASS body.
	if looksLikeSubtitle(data) && !hasSignature(data) {
		return data, nil
	}

	content, err := extractFromContainer(data, want)
	if err == nil {
		return content, nil
	}

	// Not extractable. Only return raw data if it looks like a subtitle.
	if looksLikeSubtitle(data) {
		return data, nil
	}
	return nil, err
}

// extractFromContainer picks the reader by magic bytes, trying both only when the
// magic is unknown (a self-extracting or prefixed archive carries neither
// signature at offset zero yet still reads).
func extractFromContainer(data []byte, want epmarker.Target) ([]byte, error) {
	switch {
	case hasZIPMagic(data):
		return extractWith(zipMembers, data, want)
	case hasRARMagic(data):
		return extractWith(rarMembers, data, want)
	default:
		content, zipErr := extractWith(zipMembers, data, want)
		if zipErr == nil {
			return content, nil
		}
		content, rarErr := extractWith(rarMembers, data, want)
		if rarErr == nil {
			return content, nil
		}
		return nil, statedRefusal(zipErr, rarErr)
	}
}

// extractWith opens one container and applies the single selection policy to it.
func extractWith(
	open func([]byte) (func(func(member) bool), error),
	data []byte,
	want epmarker.Target,
) ([]byte, error) {
	members, err := open(data)
	if err != nil {
		return nil, err
	}
	return selectMember(members, want)
}

// statedRefusal returns the first refusal that says more than "I could not read
// this". Both readers run when the magic is unknown, so one of them may have
// opened the data and refused for a stated reason while the other simply failed
// to parse it, and that stated reason is the one worth reporting.
func statedRefusal(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, ErrNotArchive) {
			return err
		}
	}
	return ErrNotArchive
}

// checkMemberContent turns a container read into the (content, error) pair the
// selection loop expects, applying the size cap. Shared by both readers so an
// empty member and an oversized one are reported the same way whichever
// container held them.
func checkMemberContent(name string, content []byte, readErr error) ([]byte, error) {
	safe := summarizeNames([]string{name})
	switch {
	case readErr != nil:
		return nil, fmt.Errorf("read member %s: %w", safe, readErr)
	case len(content) == 0:
		return nil, fmt.Errorf("member %s is empty", safe)
	case len(content) > maxExtractSize:
		return nil, fmt.Errorf("member %s is above the %d-byte cap", safe, maxExtractSize)
	}
	return content, nil
}

// summarizeNames renders member names for an error that reaches the operator's
// log. Upstream names the members, so each one is sanitized and capped, and the
// list is truncated because a crafted archive may declare thousands.
func summarizeNames(names []string) string {
	shown := names
	if len(shown) > maxNamesInError {
		shown = shown[:maxNamesInError]
	}
	safe := make([]string, 0, len(shown)+1)
	for _, n := range shown {
		safe = append(safe, runesafe.SanitizeSingleLineBounded(n, maxNameBytes))
	}
	if len(names) > len(shown) {
		safe = append(safe, fmt.Sprintf("(+%d more)", len(names)-len(shown)))
	}
	return strings.Join(safe, ", ")
}

// hasSignature checks whether data starts with a known archive
// magic number (ZIP or RAR). Used to skip expensive archive probing when
// the data is clearly plain text.
func hasSignature(data []byte) bool {
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

// looksLikeSubtitle checks whether data appears to be a text-based subtitle file
// by looking for format-specific signatures in the first 4KB. Also rejects data
// with high concentrations of non-text bytes.
//
// The probe runs against subtitleenc.TextView, so a UTF-16 subtitle is judged on
// its decoded form rather than on NUL-interleaved bytes no ASCII pattern can
// match. TextView decodes only encodings it positively identified, so an
// unidentified payload is probed raw and cannot be talked into looking like text
// by a fallback decoder. The decode is a view: this reports a verdict and never
// converts the caller's bytes.
func looksLikeSubtitle(data []byte) bool {
	return looksLikeSubtitleRaw(subtitleenc.TextView(data))
}

// looksLikeSubtitleRaw is the byte-level probe: a signature in the first 4KB, and
// not mostly non-text.
func looksLikeSubtitleRaw(data []byte) bool {
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
	if subtitlefile.CountNonText(probe)*10 > len(probe) {
		return false
	}

	for _, sig := range subtitleSignatures {
		if bytes.Contains(probe, sig) {
			return true
		}
	}
	return false
}
