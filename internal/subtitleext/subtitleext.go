// Package subtitleext is the one answer to which file extensions count as
// subtitles, as three capability views over one table: archive input
// (members extracted from provider archives), on disk (standalone files
// beside a video) and writer output (formats subflux writes). Every lookup
// takes a name and reads its filepath.Ext, case-insensitively.
package subtitleext

import (
	"path/filepath"
	"slices"
	"strings"
)

type caps struct {
	archiveInput bool
	onDisk       bool
	writerOutput bool
}

// .sub is on disk only: no parser reads VobSub or MicroDVD, so none is
// downloaded or written. .vtt is archive input only: the parser converts
// it to SRT before anything is written.
var table = map[string]caps{
	".srt": {archiveInput: true, onDisk: true, writerOutput: true},
	".ass": {archiveInput: true, onDisk: true, writerOutput: true},
	".ssa": {archiveInput: true, onDisk: true, writerOutput: true},
	".sub": {onDisk: true},
	".vtt": {archiveInput: true},
}

func lookup(name string) caps { return table[strings.ToLower(filepath.Ext(name))] }

// ArchiveInput reports whether name is accepted as subtitle content when
// extracting a provider archive.
func ArchiveInput(name string) bool { return lookup(name).archiveInput }

// OnDisk reports whether name is a standalone subtitle file beside a video.
func OnDisk(name string) bool { return lookup(name).onDisk }

// WriterOutput reports whether name has an extension subflux writes.
func WriterOutput(name string) bool { return lookup(name).writerOutput }

// Extensions returns every extension in the table, lowercase with the
// leading dot, sorted.
func Extensions() []string {
	out := make([]string, 0, len(table))
	for ext := range table {
		out = append(out, ext)
	}
	slices.Sort(out)
	return out
}
