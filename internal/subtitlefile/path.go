// Package subtitlefile owns the subtitle file on disk: what subflux names it,
// and what it will accept as its contents.
//
// One package rather than two because both save paths call both halves in the
// same breath — search/search_download.go and manualops/download_exec.go each
// Validate the downloaded bytes and then compute the Path or ManualPath they
// write them to, and media_paths_test.go already tested the two together.
//
// The naming half is the fleet's only reachable cross-OS portability surface
// (ratified row C10): these filenames land on SMB and NFS shares that Windows
// clients read, so the segment layout, the ordinal position and the extension
// are compatibility contract, and ManualOrdinal must stay the exact inverse of
// ManualPath's numbering. Behaviour here is unchanged by the move.
//
// Related but separate: internal/subtitleext decides which extensions COUNT as
// subtitle files, in capability-scoped views. Its coverage test pins ExtSRT to
// the writerOutput view, so the constant and the authority cannot drift.
package subtitlefile

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/api"
)

// ExtSRT is the extension every subflux writer emits. The capability-scoped
// extension authority lives in internal/subtitleext; its coverage test pins this
// constant to the writerOutput view so the two cannot drift.
const ExtSRT = ".srt"

// Tags are the variant tags a subtitle filename carries next to its
// language code (movie.fr.hi.srt, movie.fr.forced.srt).
//
// The fields are named rather than positional because a filename can carry
// either variant, both or neither, so no shape or count distinguishes them: a
// transposed pair writes a forced subtitle to the hearing-impaired target's
// filename, which the next scan reads back as coverage for a language variant
// nothing downloaded, and the operator's own file listing is the only place
// the swap is visible.
//
// Lang lives here rather than beside videoPath on the constructors, for the
// same reason. It renders as part of this segment — suffix() always took it —
// and as a second positional string it was transposable with videoPath, which
// is the hazard this type exists to remove. Only the video path is positional.
type Tags struct {
	// Lang is the subtitle's language code, the segment's first element.
	Lang string
	// HearingImpaired tags the file as a hearing-impaired (SDH) subtitle.
	HearingImpaired bool
	// Forced tags the file as a forced (foreign-parts-only) subtitle.
	Forced bool
}

// suffix renders the language code plus the tags a filename carries, in the
// order parseExternalSubPath reads them back.
func (t Tags) suffix() string {
	s := t.Lang
	if t.HearingImpaired {
		s += ".hi"
	}
	if t.Forced {
		s += ".forced"
	}
	return s
}

// Path computes the subtitle file path for a video.
func Path(videoPath string, tags Tags) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	return base + "." + tags.suffix() + ExtSRT
}

// ManualPath computes a numbered manual subtitle path.
// e.g., movie.fr.1.srt, movie.fr.2.srt for regular subs; movie.fr.hi.1.srt
// or movie.fr.forced.1.srt when the user deliberately downloaded an HI or
// forced variant. The variant tag appears before the number so
// parseExternalSubPath continues to recognize it on the next scan.
func ManualPath(videoPath string, n int, tags Tags) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	return fmt.Sprintf("%s.%s.%d"+ExtSRT, base, tags.suffix(), n)
}

// ManualOrdinal parses the manual sibling number out of a subtitle path
// produced by ManualPath: the all-digit dot segment immediately
// before the extension (movie.fr.2.srt -> 2, movie.fr.forced.1.srt -> 1).
// An unnumbered path (the auto file, movie.fr.srt) returns 0. This is the
// inverse of ManualPath's numbering and the ordinal component of the
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

// VariantFromFlags derives the variant from the HI/forced tags on a picked
// subtitle. HI wins over forced when both happen to be set.
//
// It takes [Tags] rather than two adjacent bools: that is the same pair
// Tags was introduced to make untransposable, and leaving this one
// positional meant both shapes appeared in a single call chain (this function
// and Path were called 70 lines apart from the same two fields). The
// Lang field is unused here — a variant does not depend on it — which is
// harmless and keeps one type describing the whole segment.
func VariantFromFlags(tags Tags) api.Variant {
	switch {
	case tags.HearingImpaired:
		return api.VariantHI
	case tags.Forced:
		return api.VariantForced
	default:
		return api.DefaultVariant
	}
}
