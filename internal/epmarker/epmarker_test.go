package epmarker_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/epmarker"
	"pgregory.net/rapid"
)

// TestViews pins every marker shape the three consuming sites need, read
// through all four views at once so the views cannot drift apart: the scoring
// layer reads FirstIndex and Season, the archive extractor reads Find, and the
// AnimeTosho provider reads Find plus Present.
func TestViews(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		input      string
		want       []epmarker.Marker
		wantIndex  int
		wantSeason int
		wantOK     bool
	}{
		// Ordinary scene and Sonarr naming (all three sites).
		{"standard marker", "Show.S01E05.720p", []epmarker.Marker{{Season: 1, Episode: 5}}, 5, 1, true},
		{"lowercase marker", "show s02e10.srt", []epmarker.Marker{{Season: 2, Episode: 10}}, 5, 2, true},
		{
			"sonarr format", "Mob Psycho 100 (2016) - S01E12 - Title [Bluray].mkv",
			[]epmarker.Marker{{Season: 1, Episode: 12}},
			24, 1, true,
		},
		{"title before marker", "Breaking.Bad.S01E01.720p", []epmarker.Marker{{Season: 1, Episode: 1}}, 13, 1, true},

		// Every marker is returned in order of appearance; the call site
		// decides precedence (archive and animetosho both accept any match).
		{
			"two markers in order", "Show.S09E09.S01E05.mkv",
			[]epmarker.Marker{{Season: 9, Episode: 9}, {Season: 1, Episode: 5}},
			5, 9, true,
		},
		// A concatenated multi-episode file yields only the S##E## pair; the
		// trailing E02 is the archive extractor's range concern, not a marker.
		{"multi-episode file", "Show S01E01E02.srt", []epmarker.Marker{{Season: 1, Episode: 1}}, 5, 1, true},

		// Wide digit runs: read whole, never truncated, never missed.
		{"three-digit episode", "Show S01E100.srt", []epmarker.Marker{{Season: 1, Episode: 100}}, 5, 1, true},
		{"four-digit episode", "Show.S01E1234.x264", []epmarker.Marker{{Season: 1, Episode: 1234}}, 5, 1, true},
		{"three-digit season", "Show S100E200.srt", []epmarker.Marker{{Season: 100, Episode: 200}}, 5, 100, true},
		{"zero-padded season", "Show.S001E01.720p", []epmarker.Marker{{Season: 1, Episode: 1}}, 5, 1, true},
		{"zero-valued marker", "Show.S00E00.mkv", []epmarker.Marker{{Season: 0, Episode: 0}}, 5, 0, true},

		// Season packs: a season with no episode marker (scoring's IsSeasonPack).
		{"season pack", "Show.S04.Complete.720p-GRP", nil, -1, 4, true},
		{"lowercase season pack", "show.s01.complete-grp", nil, -1, 1, true},
		{"two-digit season pack", "Show.S12.Complete-GRP", nil, -1, 12, true},

		// Names carrying no marker at all (animetosho's absolute-number gate).
		{"movie name", "Movie.2024.BluRay.1080p-GRP", nil, -1, 0, false},
		{"pure absolute anime", "[Group] Show - 26 [1080p].mkv", nil, -1, 0, false},
		{"empty", "", nil, -1, 0, false},

		// A digit run too large for an int is marker-SHAPED but unreadable:
		// Find omits it, Present and FirstIndex still report it, and Season
		// reports "unreadable" rather than a truncated number.
		{"unparseable marker", "Show.S99999999999999999999E01.mkv", nil, 5, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := epmarker.Find(tt.input); !slices.Equal(got, tt.want) {
				t.Errorf("Find(%q) = %v, want %v", tt.input, got, tt.want)
			}
			if got := epmarker.FirstIndex(tt.input); got != tt.wantIndex {
				t.Errorf("FirstIndex(%q) = %d, want %d", tt.input, got, tt.wantIndex)
			}
			if got, want := epmarker.Present(tt.input), tt.wantIndex >= 0; got != want {
				t.Errorf("Present(%q) = %v, want %v", tt.input, got, want)
			}
			season, ok := epmarker.Season(tt.input)
			if ok != tt.wantOK || season != tt.wantSeason {
				t.Errorf("Season(%q) = (%d, %v), want (%d, %v)",
					tt.input, season, ok, tt.wantSeason, tt.wantOK)
			}
		})
	}
}

// TestDivergedFromBoundedScoringReading pins the inputs on which the three
// replaced scanners disagreed. The scoring layer used bounded digit runs
// (S\d{1,2}E\d{1,3}, and S(\d{1,2}) for the season-only form) while the
// archive extractor and the AnimeTosho provider used unbounded S(\d+)E(\d+).
// The unbounded reading won everywhere; each case records what the bounded
// reading produced and why it was wrong.
func TestDivergedFromBoundedScoringReading(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		// bounded records the old scoring reading, for the record only.
		bounded    string
		want       []epmarker.Marker
		wantIndex  int
		wantSeason int
		wantOK     bool
	}{
		{
			name:    "zero-padded season is season 1, not season 0 with no episode",
			input:   "Show.S001E01.720p",
			bounded: "no marker at all; season 0 (from the leading S00)",
			want:    []epmarker.Marker{{Season: 1, Episode: 1}}, wantIndex: 5,
			wantSeason: 1, wantOK: true,
		},
		{
			name:    "three-digit season is a marker, not a season-10 pack",
			input:   "Show S100E200.srt",
			bounded: "no marker at all; season 10 (from the leading S10)",
			want:    []epmarker.Marker{{Season: 100, Episode: 200}}, wantIndex: 5,
			wantSeason: 100, wantOK: true,
		},
		{
			name:    "overflowing digits are unreadable, not silently truncated",
			input:   "Show.S99999999999999999999E01.mkv",
			bounded: "no marker at all; season 99 (first two digits of the run)",
			want:    nil, wantIndex: 5,
			wantSeason: 0, wantOK: false,
		},
		{
			name:    "a four-digit year after s is read whole",
			input:   "Kids.s2019.1080p",
			bounded: "season 20 (first two digits of the year)",
			want:    nil, wantIndex: -1,
			wantSeason: 2019, wantOK: true,
		},
		{
			name:    "the leftmost marker wins even when its season is wide",
			input:   "S123E45 S01E01",
			bounded: "first marker at index 8 (the wide one was skipped); season 12",
			want: []epmarker.Marker{
				{Season: 123, Episode: 45}, {Season: 1, Episode: 1},
			}, wantIndex: 0,
			wantSeason: 123, wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := epmarker.Find(tt.input); !slices.Equal(got, tt.want) {
				t.Errorf("Find(%q) = %v, want %v (bounded reading: %s)",
					tt.input, got, tt.want, tt.bounded)
			}
			if got := epmarker.FirstIndex(tt.input); got != tt.wantIndex {
				t.Errorf("FirstIndex(%q) = %d, want %d (bounded reading: %s)",
					tt.input, got, tt.wantIndex, tt.bounded)
			}
			season, ok := epmarker.Season(tt.input)
			if ok != tt.wantOK || season != tt.wantSeason {
				t.Errorf("Season(%q) = (%d, %v), want (%d, %v) (bounded reading: %s)",
					tt.input, season, ok, tt.wantSeason, tt.wantOK, tt.bounded)
			}
		})
	}
}

// TestPresentWithoutFind pins the one deliberate disagreement between the
// views: Present reports marker SHAPE, Find reports readable markers. The
// AnimeTosho provider's absolute-number fallback depends on this — a name
// carrying an unreadable marker still numbers its episodes explicitly, so it
// must not fall through to absolute numbering.
func TestPresentWithoutFind(t *testing.T) {
	t.Parallel()
	const name = "[Group] Show S99999999999999999999E01 - 26.mkv"
	if got := epmarker.Find(name); len(got) != 0 {
		t.Errorf("Find(%q) = %v, want no readable markers", name, got)
	}
	if !epmarker.Present(name) {
		t.Errorf("Present(%q) = false, want true (the token is marker-shaped)", name)
	}
}

// TestCaseInsensitive_ASCII verifies the views never let a name's letter case
// change their verdict. Restricted to ASCII so ToLower/ToUpper round-trip.
func TestCaseInsensitive_ASCII(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[A-Za-z0-9 .\[\]-]{0,40}`).Draw(t, "name")
		lower, upper := strings.ToLower(name), strings.ToUpper(name)

		if lo, up := epmarker.Find(lower), epmarker.Find(upper); !slices.Equal(lo, up) {
			t.Fatalf("Find(%q) case mismatch: lower=%v upper=%v", name, lo, up)
		}
		if lo, up := epmarker.FirstIndex(lower), epmarker.FirstIndex(upper); lo != up {
			t.Fatalf("FirstIndex(%q) case mismatch: lower=%d upper=%d", name, lo, up)
		}
		loSeason, loOK := epmarker.Season(lower)
		upSeason, upOK := epmarker.Season(upper)
		if loSeason != upSeason || loOK != upOK {
			t.Fatalf("Season(%q) case mismatch: lower=(%d,%v) upper=(%d,%v)",
				name, loSeason, loOK, upSeason, upOK)
		}
	})
}

// FuzzViewsAgree asserts the invariants that let the three sites mix views
// freely: a readable marker implies marker shape, FirstIndex is a real index
// into the name whenever a marker is present, and no view ever reports a
// negative number.
func FuzzViewsAgree(f *testing.F) {
	f.Add("Show.S01E05.720p")
	f.Add("show s02e10.srt")
	f.Add("Show.S04.Complete.720p-GRP")
	f.Add("[Group] Show - 26 [1080p].mkv")
	f.Add("Show.S001E01.720p")
	f.Add("Show S100E200.srt")
	f.Add("Show.S99999999999999999999E01.mkv")
	f.Add("S123E45 S01E01")
	f.Add("Show S01E01E02.srt")
	f.Add("Show.S00E00.mkv")
	f.Add("")

	f.Fuzz(func(t *testing.T, name string) {
		markers := epmarker.Find(name)
		idx := epmarker.FirstIndex(name)
		present := epmarker.Present(name)

		if present != (idx >= 0) {
			t.Fatalf("Present(%q)=%v disagrees with FirstIndex=%d", name, present, idx)
		}
		if len(markers) > 0 && !present {
			t.Fatalf("Find(%q) returned %v but Present=false", name, markers)
		}
		if idx >= 0 && idx >= len(name) {
			t.Fatalf("FirstIndex(%q) = %d, out of range for length %d", name, idx, len(name))
		}
		for _, m := range markers {
			if m.Season < 0 || m.Episode < 0 {
				t.Fatalf("Find(%q) yielded negative marker %v", name, m)
			}
		}
		if season, ok := epmarker.Season(name); ok && season < 0 {
			t.Fatalf("Season(%q) = %d, want >= 0", name, season)
		}
	})
}
