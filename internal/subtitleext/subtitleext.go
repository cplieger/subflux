// Package subtitleext is the single authority for which file extensions
// count as subtitle files, exposed as capability-scoped views rather than one
// flat union. Every accept path (archive extraction, on-disk scanning) and
// the delete gate consume the same table, so accept and delete can never
// disagree about what a subtitle file is.
//
// Capability views:
//
//   - ArchiveInput: extensions accepted when extracting subtitle content
//     from provider archives (ZIP/RAR members).
//   - OnDisk: extensions recognized as standalone subtitle files beside
//     media files during library scans.
//   - WriterOutput: extensions subflux itself writes (every writer emits
//     .srt today; see api.SubtitleExtSRT).
//   - Delete: the union view — anything an accept path could have produced
//     or recognized must be deletable through the subtitle delete gate.
//
// Seed evidence (2026-07-18 inventory): the table is the union of the two
// constants it replaced — the on-disk set (.srt .ass .ssa .sub, formerly
// api.SubtitleExtsOnDisk) and the archive set (adds .vtt, formerly
// archive.SubtitleExts). .idx/.smi/.sami/.txt are deliberately NOT seeded:
// no writer produces them and no reader recognizes them; admission requires
// positive inventory evidence, and .idx/.sub pairing would need explicit
// two-file delete semantics first.
package subtitleext

import (
	"path/filepath"
	"slices"
	"strings"
)

// caps records which capability views an extension participates in. The
// delete capability is not stored: it is defined as the union of the accept
// capabilities (see Delete), so a new row can never be accepted-but-not-
// deletable by construction.
type caps struct {
	archiveInput bool
	onDisk       bool
	writerOutput bool
}

// table is the one extension table. Keys are lowercase extensions including
// the leading dot.
var table = map[string]caps{
	".srt": {archiveInput: true, onDisk: true, writerOutput: true},
	".ass": {archiveInput: true, onDisk: true},
	".ssa": {archiveInput: true, onDisk: true},
	".sub": {archiveInput: true, onDisk: true},
	".vtt": {archiveInput: true},
}

// norm lowercases an extension for table lookup. Accepts either a bare
// extension (".srt") or a full path (the extension is extracted).
func norm(ext string) string {
	if !strings.HasPrefix(ext, ".") {
		ext = filepath.Ext(ext)
	}
	return strings.ToLower(ext)
}

// ArchiveInput reports whether ext (an extension or a path) is accepted as
// subtitle content when extracting from provider archives.
func ArchiveInput(ext string) bool { return table[norm(ext)].archiveInput }

// OnDisk reports whether ext (an extension or a path) is recognized as a
// standalone subtitle file on disk during library scans.
func OnDisk(ext string) bool { return table[norm(ext)].onDisk }

// WriterOutput reports whether ext (an extension or a path) is an extension
// subflux's own writers emit.
func WriterOutput(ext string) bool { return table[norm(ext)].writerOutput }

// Delete reports whether ext (an extension or a path) carries the delete
// capability: the union of every accept view, since anything an accept path
// could have produced or recognized must be deletable.
func Delete(ext string) bool {
	c := table[norm(ext)]
	return c.archiveInput || c.onDisk || c.writerOutput
}

// Extensions returns every extension in the table, sorted, for coverage
// tests and consumers that need to enumerate a view via the predicates.
func Extensions() []string {
	out := make([]string, 0, len(table))
	for ext := range table {
		out = append(out, ext)
	}
	slices.Sort(out)
	return out
}
