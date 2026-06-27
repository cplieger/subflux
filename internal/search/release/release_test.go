package release

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func TestParseReleaseName_empty_returns_zero_info(t *testing.T) {
	t.Parallel()
	if got := ParseReleaseName(""); got != (Info{}) {
		t.Errorf("ParseReleaseName(%q) = %#v, want zero Info{}", "", got)
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
