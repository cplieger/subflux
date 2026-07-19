package subtitleext_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/subtitleext"
)

// TestSeedTable pins the exact capability table from the design (S16): the
// union of the two constants the package replaced, with .vtt archive-only
// and .srt the only writer output.
func TestSeedTable(t *testing.T) {
	t.Parallel()
	want := map[string]struct{ archive, onDisk, writer, del bool }{
		".srt": {true, true, true, true},
		".ass": {true, true, false, true},
		".ssa": {true, true, false, true},
		".sub": {true, true, false, true},
		".vtt": {true, false, false, true},
	}
	got := subtitleext.Extensions()
	wantExts := make([]string, 0, len(want))
	for ext := range want {
		wantExts = append(wantExts, ext)
	}
	slices.Sort(wantExts)
	if !slices.Equal(got, wantExts) {
		t.Fatalf("Extensions() = %v, want %v", got, wantExts)
	}
	for ext, w := range want {
		if g := subtitleext.ArchiveInput(ext); g != w.archive {
			t.Errorf("ArchiveInput(%s) = %v, want %v", ext, g, w.archive)
		}
		if g := subtitleext.OnDisk(ext); g != w.onDisk {
			t.Errorf("OnDisk(%s) = %v, want %v", ext, g, w.onDisk)
		}
		if g := subtitleext.WriterOutput(ext); g != w.writer {
			t.Errorf("WriterOutput(%s) = %v, want %v", ext, g, w.writer)
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
		if subtitleext.WriterOutput(ext) && !subtitleext.Delete(ext) {
			t.Errorf("writerOutput ext %s lacks delete capability", ext)
		}
	}
}

// TestWriterCoverage asserts every extension the writers emit is in the
// writerOutput view: api.SubtitlePath / api.ManualSubtitlePath (the two path
// builders every save path routes through) emit api.SubtitleExtSRT, which
// must carry writerOutput (and therefore delete).
func TestWriterCoverage(t *testing.T) {
	t.Parallel()
	if !subtitleext.WriterOutput(api.SubtitleExtSRT) {
		t.Errorf("api.SubtitleExtSRT (%s) is not a writerOutput extension", api.SubtitleExtSRT)
	}
	// The path builders must produce writer-covered extensions.
	for _, p := range []string{
		api.SubtitlePath("/media/movie.mkv", "fr", false, false),
		api.ManualSubtitlePath("/media/movie.mkv", "fr", 2, false, true),
	} {
		if !subtitleext.WriterOutput(p) {
			t.Errorf("writer-produced path %s not covered by writerOutput view", p)
		}
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
