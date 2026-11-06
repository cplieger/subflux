package subtitleext_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/subtitleext"
)

func TestLookupTakesExtOfName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc, name string
		want       bool
	}{
		{"dot-slash prefix", "./Show.srt", true},
		{"parent prefix", "../x.srt", true},
		{"hidden directory", ".hidden/x.srt", true},
		{"uppercase", "movie.en.SRT", true},
		{"bare extension as name", ".srt", true},
		{"AppleDouble name", "._English.srt", true},
		{"subtitle extension not last", "movie.srt.bak", false},
		{"no dot", "srt", false},
		{"empty", "", false},
		{"video", "movie.SRT.mkv", false},
		{"dot in directory only", "/media/a.b/sub", false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			if got := subtitleext.OnDisk(tc.name); got != tc.want {
				t.Errorf("OnDisk(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestTableRows(t *testing.T) {
	t.Parallel()
	type row struct{ archive, onDisk, writer bool }
	want := map[string]row{
		".srt": {archive: true, onDisk: true, writer: true},
		".ass": {archive: true, onDisk: true, writer: true},
		".ssa": {archive: true, onDisk: true, writer: true},
		".sub": {onDisk: true},
		".vtt": {archive: true},
		".mkv": {},
		".zip": {},
		".idx": {},
		".txt": {},
	}
	views := []struct {
		name string
		fn   func(string) bool
		want func(row) bool
	}{
		{"ArchiveInput", subtitleext.ArchiveInput, func(r row) bool { return r.archive }},
		{"OnDisk", subtitleext.OnDisk, func(r row) bool { return r.onDisk }},
		{"WriterOutput", subtitleext.WriterOutput, func(r row) bool { return r.writer }},
	}
	for ext, r := range want {
		for _, v := range views {
			t.Run(ext+" "+v.name, func(t *testing.T) {
				t.Parallel()
				name := "Movie (2001)" + ext
				if got := v.fn(name); got != v.want(r) {
					t.Errorf("%s(%q) = %v, want %v", v.name, name, got, v.want(r))
				}
			})
		}
	}
}

func TestExtensionsSorted(t *testing.T) {
	t.Parallel()
	want := []string{".ass", ".srt", ".ssa", ".sub", ".vtt"}
	if got := subtitleext.Extensions(); !slices.Equal(got, want) {
		t.Errorf("Extensions() = %v, want %v", got, want)
	}
}
