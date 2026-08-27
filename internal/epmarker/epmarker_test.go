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

// --- The ##x## notation ---

// TestCrossNotation pins Addic7ed's form and, more importantly, the tokens that
// merely look like it. Every entry in the reject half is a real substring of
// real subtitle filenames, which is why the guards exist: a resolution, an
// aspect ratio and a codec tag all contain a digit-x-digit run.
func TestCrossNotation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []epmarker.Marker
	}{
		// Accepted: the real shape, taken from the pack whose ten members were
		// all unreachable before this notation was read.
		{
			"addic7ed member name",
			"Black Sails - 01x08 - VIII..720p 2HD.English.HI.srt",
			[]epmarker.Marker{{Season: 1, Episode: 8}},
		},
		{"unpadded season", "Show - 1x08 - Title.srt", []epmarker.Marker{{Season: 1, Episode: 8}}},
		{"uppercase X", "Show - 01X08 - Title.srt", []epmarker.Marker{{Season: 1, Episode: 8}}},
		{"two-digit season", "Show - 12x08.srt", []epmarker.Marker{{Season: 12, Episode: 8}}},
		{"three-digit episode", "Show - 01x100.srt", []epmarker.Marker{{Season: 1, Episode: 100}}},

		// Rejected: a resolution. The season half is capped at two digits so the
		// whole number cannot be consumed, and the interior "20x108" IS a season
		// and episode in range — what rejects it is that it starts right after a
		// digit.
		{"1080p resolution", "Show.1920x1080.BluRay.srt", nil},
		{"720p resolution", "Show.1280x720.WEB.srt", nil},
		{"4K resolution", "Show.3840x2160.HDR.srt", nil},

		// Rejected: an aspect ratio. Both halves are in range, so what rejects
		// these is the two-digit minimum on the episode: real notation pads.
		{"4:3 aspect", "Show.4x3.DVDRip.srt", nil},
		{"16:9 aspect", "Show.16x9.Widescreen.srt", nil},

		// Rejected: a codec tag has no digits before the x.
		{"x264 codec", "Show.BDRip.x264-GRP.srt", nil},
		{"x265 codec", "Show.1080p.x265.srt", nil},

		// Rejected: mid-word, so not a marker.
		{"letter before the run", "Show.MP4x264.srt", nil},

		// A real name carrying BOTH: the resolution is still refused and the
		// marker still read, which is the case the guards exist to separate.
		{"marker beside a resolution", "Show - 01x08 - 1920x1080.srt", []epmarker.Marker{{Season: 1, Episode: 8}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := epmarker.Find(tt.input); !slices.Equal(got, tt.want) {
				t.Errorf("Find(%q) = %v, want %v", tt.input, got, tt.want)
			}
			if got, want := epmarker.Present(tt.input), len(tt.want) > 0; got != want {
				t.Errorf("Present(%q) = %v, want %v", tt.input, got, want)
			}
		})
	}
}

// TestSeason_reads_the_cross_form guards against the authority contradicting
// itself. Find reading season 1 from "01x08" while Season reported no season at
// all is exactly the disagreement this package exists to prevent, and the
// scorer's wrong-season rejection is gated on Season answering.
func TestSeason_reads_the_cross_form(t *testing.T) {
	t.Parallel()
	const name = "Black Sails - 01x08 - VIII..720p 2HD.English.HI.srt"

	season, ok := epmarker.Season(name)
	if !ok || season != 1 {
		t.Errorf("Season(%q) = (%d, %v), want (1, true)", name, season, ok)
	}
	found := epmarker.Find(name)
	if len(found) == 0 || found[0].Season != season {
		t.Errorf("Find(%q) = %v, want its season to agree with Season's %d", name, found, season)
	}
}

// --- Claims ---

func TestClaims(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []epmarker.Marker
	}{
		{"single marker", "Show.S01E05.720p", []epmarker.Marker{{Season: 1, Episode: 5}}},
		{"cross marker", "Show - 01x05 - Title.srt", []epmarker.Marker{{Season: 1, Episode: 5}}},
		{
			"concatenated range", "Show S01E01E02.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}},
		},
		{
			"dashed range", "Show S01E01-E02.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}},
		},
		{
			"bare dashed range", "Show S01E01-02.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}},
		},
		{
			"wider range expands", "Show S02E04-E06.srt",
			[]epmarker.Marker{
				{Season: 2, Episode: 4}, {Season: 2, Episode: 5}, {Season: 2, Episode: 6},
			},
		},
		// A range needs a season to attach to. Guessing one would risk writing
		// the wrong episode's subtitle under the right episode's name.
		{"range with no season claims nothing", "Show E01-E02.srt", nil},
		// The year-in-title trap: "1923" would span episode 1 to 923.
		{
			"year in title is not a range", "Show.S01E01.1923.REPACK.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}},
		},
		{"no marker", "Movie.2024.BluRay.srt", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := epmarker.Claims(tt.input); !slices.Equal(got, tt.want) {
				t.Errorf("Claims(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- Target ---

func TestTarget(t *testing.T) {
	t.Parallel()

	t.Run("Any matches every name", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{
			"Show.S01E05.srt", "Show - 01x05.srt", "whatever.srt", "",
		} {
			if !epmarker.Any().Matches(name) {
				t.Errorf("Any().Matches(%q) = false, want true", name)
			}
		}
	})

	// A zero on either half is how the wire says "no episode to disambiguate",
	// so For must collapse it rather than looking for episode zero of season one.
	t.Run("For collapses an absent half to Any", func(t *testing.T) {
		t.Parallel()
		for _, m := range []epmarker.Marker{
			{Season: 0, Episode: 0},
			{Season: 1, Episode: 0},
			{Season: 0, Episode: 5},
			{Season: -1, Episode: 5},
		} {
			got := epmarker.For(m)
			if !got.Matches("anything.srt") {
				t.Errorf("For(%+v) should match any name", m)
			}
		}
	})

	t.Run("a fixed target matches only its own episode", func(t *testing.T) {
		t.Parallel()
		target := epmarker.For(epmarker.Marker{Season: 1, Episode: 8})
		for _, name := range []string{
			"Black.Sails.S01E08.BDRip.srt",
			"Black Sails - 01x08 - VIII..srt",
			"Show S01E07-E09.srt",
		} {
			if !target.Matches(name) {
				t.Errorf("For(S01E08).Matches(%q) = false, want true", name)
			}
		}
		for _, name := range []string{
			"Black.Sails.S01E07.BDRip.srt",
			"Black Sails - 02x08 - VIII..srt",
			"Show S01E01-E04.srt",
			"no marker here.srt",
		} {
			if target.Matches(name) {
				t.Errorf("For(S01E08).Matches(%q) = true, want false", name)
			}
		}
	})

	t.Run("renders for a diagnostic", func(t *testing.T) {
		t.Parallel()
		if got, want := epmarker.For(epmarker.Marker{Season: 1, Episode: 8}).String(), "S01E08"; got != want {
			t.Errorf("For(S01E08).String() = %q, want %q", got, want)
		}
		if got, want := epmarker.Any().String(), "any episode"; got != want {
			t.Errorf("Any().String() = %q, want %q", got, want)
		}
		if got, want := (epmarker.Marker{Season: 12, Episode: 100}).String(), "S12E100"; got != want {
			t.Errorf("Marker{12,100}.String() = %q, want %q", got, want)
		}
	})
}

// TestSeparatedMarker pins the S## E## form and the tokens that must not become
// one. The accepted half is real member naming measured on two SubSource packs;
// the rejected half is the audio and edition tags those same filenames carry, so
// each is a substring the separator could otherwise have reached across.
func TestSeparatedMarker(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []epmarker.Marker
	}{
		{
			"bracketed with a space",
			"Firefly [S01 E01] - Serenity.eng.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}},
		},
		{
			"leading with a space",
			"S01 E01 Winter is Coming SDH.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}},
		},
		{"dot separator", "Show.S01.E05.720p.srt", []epmarker.Marker{{Season: 1, Episode: 5}}},
		{"underscore separator", "Show_S01_E05.srt", []epmarker.Marker{{Season: 1, Episode: 5}}},
		{"hyphen separator", "Show-S01-E05.srt", []epmarker.Marker{{Season: 1, Episode: 5}}},
		{"no separator still reads", "Show.S01E05.srt", []epmarker.Marker{{Season: 1, Episode: 5}}},

		// The separator must not reach across unrelated text. Each of these
		// carries an S or an E that is not part of a marker.
		{"audio tag has no digit after its S", "Show.S01E05.DTS 5.1 EAC3.srt", []epmarker.Marker{{Season: 1, Episode: 5}}},
		{"edition word has no digit after its E", "Show.S01 Edition.srt", nil},
		{"separator too long to bridge", "Show.S01 - - - E05.srt", nil},
		{"no marker at all", "Movie.2024.BluRay.srt", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := epmarker.Find(tt.input); !slices.Equal(got, tt.want) {
				t.Errorf("Find(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestSeason_reads_the_word_form pins the spelled-out season. It matters past
// archive extraction: the scorer's wrong-season-pack rejection is gated on a
// season being readable at all, so while this form was unread that check was
// silently inert for every release that spells it out, and these are real
// SubSource release names.
func TestSeason_reads_the_word_form(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		input      string
		wantSeason int
		wantOK     bool
	}{
		{"word form", "Game Of Thrones Season 1 (2011) Added episodes title pahe BR REMUX", 1, true},
		{"word form two digits", "Show - Season 10 - Complete", 10, true},
		{"word form dotted", "Show.Season.02.Complete", 2, true},
		{"compact form still wins", "Black Sails Season 1 S01 (1080p BluRay x265 RCVR)", 1, true},
		// A multi-season bundle names no single season, so it must stay unread
		// rather than claiming the first number it can find.
		{"plural is not one season", "Show Seasons 1-9 Complete", 0, false},
		{"no season anywhere", "Movie.2024.BluRay.1080p-GRP", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			season, ok := epmarker.Season(tt.input)
			if season != tt.wantSeason || ok != tt.wantOK {
				t.Errorf("Season(%q) = (%d, %v), want (%d, %v)",
					tt.input, season, ok, tt.wantSeason, tt.wantOK)
			}
		})
	}
}

// TestBareEpisode pins the reading that carries no season. The rejected half is
// what keeps it from reading a number out of anything that merely starts with
// one, and the last case is the invariant that stops two readings answering for
// one name.
func TestBareEpisode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		wantEp int
		wantOK bool
	}{
		{"number and title", "6 - A Golden Crown..srt", 6, true},
		{"two digits", "10 - Fire And Blood..srt", 10, true},
		{"dot separator", "3.Lord Snow.srt", 3, true},
		{"underscore separator", "7_You Win Or You Die.srt", 7, true},

		// A resolution has a digit where the separator must be.
		{"resolution is not an episode", "1080p.BluRay.srt", 0, false},
		{"four digits", "2011 - Something.srt", 0, false},
		// Not at the start, so it is part of a title rather than a number
		// standing in for the episode.
		{"number not leading", "Show 6 - Title.srt", 0, false},
		{"no number at all", "Winter Is Coming.srt", 0, false},
		{"empty", "", 0, false},

		// A name carrying a readable marker is answered by Claims, never here:
		// two readings for one name is how a caller ends up with two answers.
		{"a real marker disqualifies the bare reading", "1 - Show S02E05.srt", 0, false},
		{"a cross marker disqualifies it too", "1 - Show 02x05.srt", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ep, ok := epmarker.BareEpisode(tt.input)
			if ep != tt.wantEp || ok != tt.wantOK {
				t.Errorf("BareEpisode(%q) = (%d, %v), want (%d, %v)",
					tt.input, ep, ok, tt.wantEp, tt.wantOK)
			}
		})
	}
}

func TestTarget_MatchesBareEpisode(t *testing.T) {
	t.Parallel()

	t.Run("matches its own episode", func(t *testing.T) {
		t.Parallel()
		target := epmarker.For(epmarker.Marker{Season: 1, Episode: 6})
		if !target.MatchesBareEpisode("6 - A Golden Crown..srt") {
			t.Error(`For(S01E06).MatchesBareEpisode("6 - …") = false, want true`)
		}
		if target.MatchesBareEpisode("7 - You Win Or You Die..srt") {
			t.Error(`For(S01E06).MatchesBareEpisode("7 - …") = true, want false`)
		}
	})

	// An Any target has no episode to compare against, so it must never claim a
	// bare number matches: that would make the reading apply to movie downloads,
	// where no season is known at all.
	t.Run("an Any target never matches", func(t *testing.T) {
		t.Parallel()
		if epmarker.Any().MatchesBareEpisode("6 - A Golden Crown..srt") {
			t.Error("Any().MatchesBareEpisode() = true, want false")
		}
	})
}
