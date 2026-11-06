package subtitlefile_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/subtitlefile"
)

func stemsOf(videos ...string) []string {
	out := make([]string, 0, len(videos))
	for _, v := range videos {
		if subtitlefile.IsVideo(v) {
			out = append(out, subtitlefile.Stem(v))
		}
	}
	return out
}

func TestOwnerLongestStem(t *testing.T) {
	t.Parallel()
	season := stemsOf("Show S01E01.mkv", "Show S01E02.mkv", "Show S01E10.mkv")
	for _, tc := range []struct {
		name   string
		stems  []string
		file   string
		want   string
		wantOK bool
	}{
		{"episode 1", season, "Show S01E01.en.srt", "Show S01E01", true},
		{"episode 2", season, "Show S01E02.fr.sdh.srt", "Show S01E02", true},
		{"episode 10 not episode 1", season, "Show S01E10.en.srt", "Show S01E10", true},
		{"longest stem", stemsOf("Movie.mkv", "Movie.Extended.mkv"), "Movie.Extended.en.srt", "Movie.Extended", true},
		{"shorter stem keeps its own", stemsOf("Movie.mkv", "Movie.Extended.mkv"), "Movie.en.srt", "Movie", true},
		{"shared stem owns none", stemsOf("Movie.mkv", "Movie.mp4"), "Movie.en.srt", "", false},
		{"AppleDouble owns none", stemsOf("English.mkv", "._English.mkv"), "._English.srt", "", false},
		{"no video", stemsOf("Movie.nfo"), "Movie.en.srt", "", false},
		{"prefix without dot", stemsOf("Movie.mkv"), "Movies.en.srt", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := subtitlefile.Owner(tc.stems, tc.file)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Owner(%q, %q) = %q, %v; want %q, %v", tc.stems, tc.file, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestIsVideo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"Movie.mkv", true},
		{"Movie.MP4", true},
		{"Show.m2ts", true},
		{"Movie.en.srt", false},
		{"Movie.nfo", false},
		{"Movie", false},
		{".mkv", false},
		{"._Movie.mkv", false},
		{".unmanic.Movie.mkv", false},
		{".hidden.mkv", true},
	} {
		if got := subtitlefile.IsVideo(tc.name); got != tc.want {
			t.Errorf("IsVideo(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStem(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Movie.mkv":             "Movie",
		"Show.S01E01.1080p.mkv": "Show.S01E01.1080p",
		"Movie":                 "Movie",
	} {
		if got := subtitlefile.Stem(in); got != want {
			t.Errorf("Stem(%q) = %q, want %q", in, got, want)
		}
	}
}
