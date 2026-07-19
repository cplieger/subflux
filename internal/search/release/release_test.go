package release

import (
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func TestParseReleaseName_empty_returns_zero_info(t *testing.T) {
	t.Parallel()
	if got := ParseReleaseName(""); got != (Info{}) {
		t.Errorf("ParseReleaseName(%q) = %#v, want zero Info{}", "", got)
	}
}

// TestParseReleaseName_clamps_oversized_input pins the parser's own
// MaxNameLen defense-in-depth clamp (the provider boundary's ClampName is
// the primary enforcement): a 1 MiB input must parse in bounded time and
// be treated as its MaxNameLen-byte prefix. The recognizable metadata sits
// entirely BEYOND the bound, so any leak of post-bound bytes into the
// result is visible.
func TestParseReleaseName_clamps_oversized_input(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("A.", MaxNameLen/2) // exactly MaxNameLen bytes of neutral filler
	marker := ".The.Matrix.1999.1080p.BluRay.x264-GRP"
	tail := strings.Repeat(".z", ((1<<20)-len(head)-len(marker))/2)
	huge := head + marker + tail
	if len(huge) < 1<<20-1 {
		t.Fatalf("test input is %d bytes, want ~1 MiB", len(huge))
	}

	start := time.Now()
	got := ParseReleaseName(huge)
	elapsed := time.Since(start)

	if want := ParseReleaseName(huge[:MaxNameLen]); got != want {
		t.Errorf("ParseReleaseName(1MiB input) = %#v, want the MaxNameLen-prefix result %#v", got, want)
	}
	if got.Source != "" {
		t.Errorf("ParseReleaseName(1MiB input).Source = %q, want \"\" (the BluRay marker beyond MaxNameLen must not be parsed)", got.Source)
	}
	// Clamped work is microseconds; a generous ceiling keeps the bound
	// meaningful without inviting CI flakes.
	if elapsed > 10*time.Second {
		t.Errorf("ParseReleaseName(1MiB input) took %s, want bounded time via the MaxNameLen clamp", elapsed)
	}
}

func TestParseReleaseName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		release     string
		wantSource  string
		wantEdition string
		wantGroup   string
	}{
		{
			name:       "bluray movie with scene group",
			release:    "Some.Movie.2020.1080p.BluRay.x264-RARBG",
			wantSource: "bluray",
			wantGroup:  "RARBG",
		},
		{
			name:        "edition keyword detected",
			release:     "The.Movie.2021.Remastered.1080p.BluRay.x264-GRP",
			wantSource:  "bluray",
			wantEdition: "remastered",
			wantGroup:   "GRP",
		},
		{
			name:       "no edition keyword",
			release:    "The.Movie.2021.1080p.BluRay.x264-GRP",
			wantSource: "bluray",
			wantGroup:  "GRP",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseReleaseName(tc.release)
			if got.Source != tc.wantSource {
				t.Errorf("ParseReleaseName(%q).Source = %q, want %q", tc.release, got.Source, tc.wantSource)
			}
			if got.Edition != tc.wantEdition {
				t.Errorf("ParseReleaseName(%q).Edition = %q, want %q", tc.release, got.Edition, tc.wantEdition)
			}
			if got.ReleaseGroup != tc.wantGroup {
				t.Errorf("ParseReleaseName(%q).ReleaseGroup = %q, want %q", tc.release, got.ReleaseGroup, tc.wantGroup)
			}
		})
	}
}

func TestParseReleaseGroup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		release string
		want    string
	}{
		{"scene group suffix", "Some.Movie.2020.1080p.BluRay.x264-RARBG", "RARBG"},
		{"no group", "Plain Title 2020 1080p", ""},
		{"anime bracket group", "[HorribleSubs] Show - 01 [1080p].mkv", "HorribleSubs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseReleaseGroup(tc.release); got != tc.want {
				t.Errorf("ParseReleaseGroup(%q) = %q, want %q", tc.release, got, tc.want)
			}
		})
	}
}

func TestCompareSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"same family (webdl/webrip)", NormWebDL, NormWebRip, true},
		{"different family (webdl/bluray)", NormWebDL, NormBluray, false},
		{"empty first operand", "", NormWebDL, false},
		{"empty second operand", NormWebDL, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ms api.MatchSet
			CompareSource(&ms, tc.a, tc.b)
			if ms.Source != tc.want {
				t.Errorf("CompareSource(%q, %q).Source = %v, want %v", tc.a, tc.b, ms.Source, tc.want)
			}
		})
	}
}
