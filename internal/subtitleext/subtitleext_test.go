package subtitleext_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/subtitleext"
	"github.com/cplieger/subflux/internal/subtitlefile"
)

// TestSeedTable pins the exact capability table from the design (S16): the
// union of the two constants the package replaced, with .vtt archive-only.
func TestSeedTable(t *testing.T) {
	t.Parallel()
	want := map[string]struct{ archive, onDisk, del bool }{
		".srt": {true, true, true},
		".ass": {true, true, true},
		".ssa": {true, true, true},
		".sub": {true, true, true},
		".vtt": {true, false, true},
	}
	got := subtitleext.Extensions()
	wantExts := make([]string, 0, len(want))
	for ext := range want {
		wantExts = append(wantExts, ext)
	}
	slices.Sort(wantExts)
	if !slices.Equal(got, wantExts) {
		t.Errorf("Extensions() = %v, want %v", got, wantExts)
	}
	for ext, w := range want {
		if g := subtitleext.ArchiveInput(ext); g != w.archive {
			t.Errorf("ArchiveInput(%s) = %v, want %v", ext, g, w.archive)
		}
		if g := subtitleext.OnDisk(ext); g != w.onDisk {
			t.Errorf("OnDisk(%s) = %v, want %v", ext, g, w.onDisk)
		}
		if g := subtitleext.Delete(ext); g != w.del {
			t.Errorf("Delete(%s) = %v, want %v", ext, g, w.del)
		}
	}
}

// TestViewSubsets asserts the S16 coverage invariants: every accept view is
// a subset of the delete view, so nothing subflux accepts or writes can ever
// be refused deletion.
func TestViewSubsets(t *testing.T) {
	t.Parallel()
	for _, ext := range subtitleext.Extensions() {
		if subtitleext.ArchiveInput(ext) && !subtitleext.Delete(ext) {
			t.Errorf("archiveInput ext %s lacks delete capability", ext)
		}
		if subtitleext.OnDisk(ext) && !subtitleext.Delete(ext) {
			t.Errorf("onDisk ext %s lacks delete capability", ext)
		}
	}
}

// TestWriterCoverage asserts every extension the writers emit is deletable:
// subtitlefile.Path / subtitlefile.ManualPath (the two path builders every
// save path routes through) emit subtitlefile.ExtSRT.
func TestWriterCoverage(t *testing.T) {
	t.Parallel()
	if !subtitleext.Delete(subtitlefile.ExtSRT) {
		t.Errorf("subtitlefile.ExtSRT (%s) is not deletable", subtitlefile.ExtSRT)
	}
	// The path builders must produce writer-covered extensions.
	for _, p := range []string{
		subtitlefile.Path("/media/movie.mkv", subtitlefile.Tags{Lang: "fr"}),
		subtitlefile.ManualPath("/media/movie.mkv", 2, subtitlefile.Tags{Lang: "fr", Forced: true}),
	} {
		if !subtitleext.Delete(p) {
			t.Errorf("writer-produced path %s not deletable", p)
		}
	}
}

// TestNonSubtitleRefused pins the negative space: media and archive
// containers never gain subtitle capabilities.
func TestNonSubtitleRefused(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".mkv", ".mp4", ".avi", ".zip", ".rar", ".idx", ".smi", ".sami", ".txt", "", ".SRT.mkv"} {
		if subtitleext.Delete(ext) {
			t.Errorf("Delete(%q) = true, want false", ext)
		}
	}
}

// TestPathAndCaseInsensitivity verifies views accept full paths and
// uppercase extensions.
func TestPathAndCaseInsensitivity(t *testing.T) {
	t.Parallel()
	if !subtitleext.OnDisk("/media/tv/Show S01E01.FR.SRT") {
		t.Error("OnDisk should match uppercase .SRT via path")
	}
	if !subtitleext.Delete("movie.en.VTT") {
		t.Error("Delete should match uppercase .VTT via path")
	}
	if subtitleext.OnDisk("srt") { // no dot, no extension in path form
		t.Error("bare 'srt' (no dot) must not match")
	}
}
