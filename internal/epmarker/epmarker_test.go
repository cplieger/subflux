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
		{"season pack after an underscore", "Show_S03_1080p-GRP", nil, -1, 3, true},

		// An s and a digit inside a word state no season: audio tags and
		// title words on single-episode names.
		{"audio tag on an absolute episode", "[Group] Show - 05 [BD 1080p HEVC DTS5.1]", nil, -1, 0, false},
		{"opus tag on an absolute episode", "[Group] Show - 105 [1080p x265 Opus2.0]", nil, -1, 0, false},
		{"audio tag after a bare episode", "Show.E05.1080p.WEB-DL.DTS5.1-GRP", nil, -1, 0, false},
		{"title word ending in s and a digit", "Mars2.Show.1080p.WEB-DL-GRP", nil, -1, 0, false},
		// An S## followed by a letter is a word too: a group named S3RL.
		{"group token starting with s and a digit", "Show.1080p.WEB-DL-S3RL", nil, -1, 0, false},
		{"lone season after such a token", "Show.S3RL.S02.Complete", nil, -1, 2, true},
		{"audio tag before a marker", "Show.DTS5.1.S02E03.1080p", []epmarker.Marker{{Season: 2, Episode: 3}}, 12, 2, true},
		// The first season statement decides, a lone one or a marker's.
		{"lone season before a marker", "Show S03 Bonus S01E02.mkv", []epmarker.Marker{{Season: 1, Episode: 2}}, 15, 3, true},
		{"glued marker before a lone season", "TheOfficeS01E05 (S02 subs).srt", []epmarker.Marker{{Season: 1, Episode: 5}}, 9, 1, true},
		// A marker glued to the title is still a marker, and Season reads it.
		{"marker glued to the title", "TheOfficeS01E05.srt", []epmarker.Marker{{Season: 1, Episode: 5}}, 9, 1, true},

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
		if lo, up := epmarker.Claims(lower), epmarker.Claims(upper); !slices.Equal(lo, up) {
			t.Fatalf("Claims(%q) case mismatch: lower=%v upper=%v", name, lo, up)
		}
		if lo, up := epmarker.StatesEpisode(lower), epmarker.StatesEpisode(upper); lo != up {
			t.Fatalf("StatesEpisode(%q) case mismatch: lower=%v upper=%v", name, lo, up)
		}
		loEp, loOK := epmarker.Absolute(lower)
		upEp, upOK := epmarker.Absolute(upper)
		if loEp != upEp || loOK != upOK {
			t.Fatalf("Absolute(%q) case mismatch: lower=(%d,%v) upper=(%d,%v)",
				name, loEp, loOK, upEp, upOK)
		}
	})
}

// FuzzViewsAgree asserts the invariants that let the three sites mix views
// freely: a readable marker implies marker shape, marker shape or a claim
// states an episode, marker shape rules out an absolute number, FirstIndex is a real index into
// the name whenever a marker is present, and no view ever reports a negative
// number.
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
	f.Add("Show.DTS5.1.S02E03.1080p")
	f.Add("[SubsPlease] Show S2 - 05v2 (1080p).ass")
	f.Add("Show [01 - 12] - 03 ~ 04 e05 06 ")
	f.Add("Show.S01.720p.HDTV.x264-E02")
	f.Add("Show.S02.H.264-E05")
	f.Add("[Grp] Show S2 EP01-EP12 [1080p]")
	f.Add("Show.Season.2.E01E02 - 2014-06-02")
	f.Add("[Grp] Show S2 [01 - 12] (E0123456)")
	f.Add("Show.Season.2.E05-E03 S01E01 01-60")
	f.Add("Show S01 - 01-03 S02 [Vol.1-6] (04 ~ 06)")
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
		states := epmarker.StatesEpisode(name)
		if present && !states {
			t.Fatalf("Present(%q) = true but StatesEpisode = false", name)
		}
		if claims := epmarker.Claims(name); len(claims) > 0 && !states {
			t.Fatalf("Claims(%q) = %v but StatesEpisode = false", name, claims)
		}
		if ep, ok := epmarker.Absolute(name); ok && (present || ep < 0) {
			t.Fatalf("Absolute(%q) = %d with Present = %v", name, ep, present)
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
		{
			"three concatenated episodes", "Show S01E01E02E03.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}, {Season: 1, Episode: 3}},
		},
		{
			"dotted range", "Show S01E05.E06.srt",
			[]epmarker.Marker{{Season: 1, Episode: 5}, {Season: 1, Episode: 6}},
		},
		{
			"separated marker range", "Firefly [S01 E01-E02].srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}},
		},
		// A date is no range: its numbers decrease.
		{"date after a marker", "Show.S01E01.2014-06-02.srt", []epmarker.Marker{{Season: 1, Episode: 1}}},
		// A codec's digits open no range.
		{"codec before an E", "Show.S01E01.x264-E02.srt", []epmarker.Marker{{Season: 1, Episode: 1}}},
		// Beside a marker, bare digits are a title's or a part's.
		{"part range after a marker", "Show.S01E05.Part.1-2.srt", []epmarker.Marker{{Season: 1, Episode: 5}}},
		{"title range after a marker", "Show.S01E03.1-800.Ghost.srt", []epmarker.Marker{{Season: 1, Episode: 3}}},
		{"dash range after a marker", "Show - S01E07 - 1-2-3.srt", []epmarker.Marker{{Season: 1, Episode: 7}}},
		// Outside brackets a " - " parts a title's number from the episode's.
		{"spaced hyphen after a season", "[Grp] Show S2 01 - 12 [1080p]", []epmarker.Marker{{Season: 2, Episode: 12}}},

		// With no marker, an episode stated after the season claims under it.
		{"episode word after a season word", "Show.Season.2.Episode.5.srt", []epmarker.Marker{{Season: 2, Episode: 5}}},
		{"dash number after a season", "[SubsPlease] Show S2 - 05 (1080p).ass", []epmarker.Marker{{Season: 2, Episode: 5}}},
		{
			"range in brackets after a season", "[Grp] Show S2 [01-03].ass",
			[]epmarker.Marker{{Season: 2, Episode: 1}, {Season: 2, Episode: 2}, {Season: 2, Episode: 3}},
		},
		{"dash number before the season", "[Grp] Show - 05 S2.ass", nil},
		{"range past the span bound claims its first", "Show.Season.2.E01-E60.srt", []epmarker.Marker{{Season: 2, Episode: 1}}},
		{
			"cross range", "Show - 01x05-06 - Title.srt",
			[]epmarker.Marker{{Season: 1, Episode: 5}, {Season: 1, Episode: 6}},
		},
		// A range claims under its own marker's season, any other run under the
		// latest season statement before it.
		{
			"range after a later marker", "Show S02E01 S01E05-E06.srt",
			[]epmarker.Marker{{Season: 1, Episode: 5}, {Season: 1, Episode: 6}, {Season: 2, Episode: 1}},
		},
		{
			"episode words after two season words", "Season 2 Episode 5 Season 3 Episode 6",
			[]epmarker.Marker{{Season: 2, Episode: 5}, {Season: 3, Episode: 6}},
		},
		{
			"eNN ranges after two season words", "Show.Season.1.E01-E03.Season.2.E04-E06",
			append(seasonEpisodes(1, 1, 3), seasonEpisodes(2, 4, 6)...),
		},
		{
			"dash ranges after two seasons", "Show S01 - 01-03 S02 - 04-06",
			append(seasonEpisodes(1, 1, 3), seasonEpisodes(2, 4, 6)...),
		},
		{
			"one range under each of two season words", "Season 1 - 01-12 Season 2 - 01-12",
			append(seasonEpisodes(1, 1, 12), seasonEpisodes(2, 1, 12)...),
		},

		// Claims come back ordered by season, then episode, whatever order the
		// name states them in.
		{
			"later season stated first", "Show S02E01 S01E05.srt",
			[]epmarker.Marker{{Season: 1, Episode: 5}, {Season: 2, Episode: 1}},
		},
		{
			"later episode stated first", "Show S01E03 S01E01.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 3}},
		},

		// Range bounds: the last episode may reach 999, a span may reach 50,
		// and a range naming one episode still claims it.
		{
			"range ending at episode 999", "Show S01E998-E999.srt",
			[]epmarker.Marker{{Season: 1, Episode: 998}, {Season: 1, Episode: 999}},
		},
		{
			"range late in a season", "Show S01E30-E31.srt",
			[]epmarker.Marker{{Season: 1, Episode: 30}, {Season: 1, Episode: 31}},
		},
		{
			"single-episode range", "Show S01E01 E05-05.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 5}},
		},
		{
			"every range in the name expands", "Show S01E01-E02 E05-E06.srt",
			[]epmarker.Marker{
				{Season: 1, Episode: 1},
				{Season: 1, Episode: 2},
				{Season: 1, Episode: 5},
				{Season: 1, Episode: 6},
			},
		},
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

func seasonEpisodes(season, first, last int) []epmarker.Marker {
	var out []epmarker.Marker
	for ep := first; ep <= last; ep++ {
		out = append(out, epmarker.Marker{Season: season, Episode: ep})
	}
	return out
}

func TestClaims_a_fifty_episode_span_reaches_its_last_episode(t *testing.T) {
	t.Parallel()
	const name = "Show S01E01-E51.srt"
	if !epmarker.For(epmarker.Marker{Season: 1, Episode: 51}).Matches(name) {
		t.Errorf("For(S01E51).Matches(%q) = false, want true", name)
	}
	if epmarker.For(epmarker.Marker{Season: 1, Episode: 52}).Matches("Show S01E01-E52.srt") {
		t.Errorf("For(S01E52).Matches(%q) = true, want false: a 51-episode span is not a range", "Show S01E01-E52.srt")
	}
}

func TestFind_merges_both_notations_in_order_of_appearance(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		want      []epmarker.Marker
		wantIndex int
	}{
		{
			"cross before standard", "Show 01x02 S03E04.srt",
			[]epmarker.Marker{{Season: 1, Episode: 2}, {Season: 3, Episode: 4}},
			5,
		},
		{
			"standard at the start, cross later", "S01E01 01x02.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}},
			0,
		},
		{
			"two cross markers", "Show - 01x01-01x02 - Title.srt",
			[]epmarker.Marker{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}},
			7,
		},
		{"cross marker opening the name", "01x08 - VIII.srt", []epmarker.Marker{{Season: 1, Episode: 8}}, 0},
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
		})
	}
}

// A cross token glued to a preceding letter or digit is the tail of a longer
// word or number. The cases sit on each edge of the alphanumeric ranges.
func TestFind_cross_marker_after_an_alphanumeric_is_not_a_marker(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"001x02", "901x02", "a01x02", "z01x02", "A01x02", "Z01x02"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if got := epmarker.Find(input); got != nil {
				t.Errorf("Find(%q) = %v, want nil", input, got)
			}
		})
	}
}

func TestMarker_String_zero_pads_single_digits_only(t *testing.T) {
	t.Parallel()
	tests := []struct {
		m    epmarker.Marker
		want string
	}{
		{epmarker.Marker{Season: 0, Episode: 10}, "S00E10"},
		{epmarker.Marker{Season: 9, Episode: 100}, "S09E100"},
	}
	for _, tt := range tests {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("%#v.String() = %q, want %q", tt.m, got, tt.want)
		}
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
		// The S## half must be a token of its own: an audio tag is not.
		{"S and a digit inside a token", "Show.DTS5 E05.srt", nil},
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
		{"compact form wins a disagreement", "Show Season 1 S02.srt", 2, true},
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

// TestStatesEpisode pins which names state an episode, the half of a season
// pack the season alone cannot answer.
func TestStatesEpisode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"marker", "Show.S01E02.1080p.WEB-DL-GRP", true},
		{"separated marker", "Show.S01.E05.720p.HDTV-GRP", true},
		{"cross form", "Show - 01x08 - Title.HDTV-GRP", true},
		{"unreadable marker", "Show.S01E99999999999999999999.HDTV-GRP", true},
		{"EP after a season", "Show.S02.EP05.1080p", true},
		{"Ep with a separator", "Show.S02.Ep.05", true},
		{"Episode N", "Show.Season.2.Episode.5", true},
		{"eNN", "Show Season 2 E05 1080p", true},
		{"dash number after a lone season", "[SubsPlease] Mushoku Tensei S2 - 05 (1080p) [ABCD1234].mkv", true},
		{"dash number with a version", "[SubsPlease] Show S2 - 05v2 (1080p).ass", true},
		{"dash number after a season word", "[Grp] Show Season 2 - 05 [1080p]", true},
		// An E## after a codec, a resolution or audio digits is a number of its
		// own, as after any other token; the arrs refuse E02 as a group.
		{"eNN after a codec hyphen", "Show.S01.720p.HDTV.x264-E02", true},
		{"eNN after a dotted codec", "Show.S02.H.264-E05", true},
		{"eNN after audio digits", "Show.S01.DDP5.1-E02", true},
		{"eNN after a resolution", "Show.S01.720p-E02", true},
		{"eNN after a source", "Show.S01.Complete.BluRay-E02", true},
		// A range states an episode set in every notation.
		{"dash range after a season", "[Grp] Show S2 - 01 ~ 12 [1080p]", true},
		{"glued dash range after a season", "[Grp] Show S2 - 01-12 [Batch]", true},
		{"double episode after a season", "[Grp] Show S2 - 05-06 [1080p]", true},
		{"eNN range after a season word", "Show.Season.2.E01-E12.1080p", true},
		{"concatenated eNN after a season word", "Show.Season.2.E01E02.1080p", true},
		{"EP range", "[Grp] Show S2 EP01-EP12 [1080p]", true},
		{"EP range with a bare end", "[Grp] Show S2 EP01-12 [1080p]", true},
		{"one-digit eN range", "Show.S02.E1-E2", true},
		// The span bound limits only how far Claims expands, and brackets
		// hide no range.
		{"span past the range bound", "Show.Season.2.E01-E60", true},
		{"decreasing eNN run", "Show.Season.2.E05-E03", true},
		{"EP range past the span bound", "[Grp] Show S2 EP01-EP60 [1080p]", true},
		{"dash range in brackets", "Show S2 [01 - 12]", true},
		{"range in brackets", "[Grp] Show S2 [01-12] [1080p]", true},
		{"range in parentheses", "[Grp] Show S2 (01-12) [1080p]", true},
		{"bare range", "[Grp] Show S2 01-12 [1080p]", true},
		{"spaced range", "[Grp] Show S2 01 ~ 12 [1080p]", true},
		{"range in brackets after a season word", "[Grp] Show Season 2 [01-12]", true},
		{"range opening a later bracket", "[Grp] Show S2 [1080p] [01-12]", true},
		{"EP in brackets", "[Grp] Show S2 [EP05] [1080p]", true},

		{"season pack", "Show.S04.Complete.1080p.BluRay-GRP", false},
		{"season word pack", "Show.Season.2.720p.WEB-DL-GRP", false},
		{"dash number before the season", "[Grp] Show - 05 S2 Batch", false},
		{"dash number with no season", "[Group] Show - 26 [1080p].mkv", false},
		{"dash range before the season", "[Grp] Show - 01 ~ 12 S2", false},
		{"dash date after a season", "Show S02 - 2014-06-02", false},
		{"dash word after a season word", "[Grp] Show 2nd Season - 05 [1080p]", false},
		{"dash resolution after a season", "Show S02 - 1080p", false},
		{"bare number in brackets", "[Grp] Show S2 [05] [1080p]", false},
		{"digit crc in brackets", "[Grp] Show S2 [1080p][E0123456]", false},
		{"spaced number is no episode", "Show S02 Complete 10 bit-GRP", false},
		{"ep inside a word", "Show.S02.Step.05", false},
		{"e and a digit inside a word", "Show.S02.Release01", false},
		{"crc in brackets", "[Grp] Show S2 [BD 1080p][E5A2C3D1]", false},
		{"one-digit eN", "Show.S03.DDP5.1.Atmos-E1", false},
		// Bare digits state an episode set only right after the season or
		// opening a bracket, and from two digits, as one number does.
		{"volume range", "Show S01 Vol.1-6 [1080p]", false},
		{"disc range", "Show.S01.Disc.1-4", false},
		{"volume range in brackets", "[Grp] Show S2 [BD Vol.1-3] [1080p]", false},
		{"spaced range after a word", "Show S01 Vol 01 ~ 06", false},
		{"one-digit bare range", "[Grp] Show S2 1-2 [1080p]", false},
		{"one-digit dash range", "[Grp] Show S2 - 5-6 [1080p]", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := epmarker.StatesEpisode(tt.input); got != tt.want {
				t.Errorf("StatesEpisode(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestAbsolute pins the absolute-number form: the last " - NN", else the first
// E##, EP## or Episode N, else the first " NN "; never a bare number in
// brackets, never a range and never for a name a marker shapes.
func TestAbsolute(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		wantEp int
		wantOK bool
	}{
		{"dash number", "[Group] Show - 26 [1080p].mkv", 26, true},
		{"dash number at the end", "Show - 08.mkv", 8, true},
		{"three digits", "Show - 100.mkv", 100, true},
		{"version suffix", "[Grp] Show - 05v2 [1080p]", 5, true},
		{"the last dash number", "Mob Psycho 100 - 05 - 26.mkv", 26, true},
		{"dash after a season statement", "[SubsPlease] Show S2 - 05 (1080p)", 5, true},
		{"dash after a season word", "[Grp] Show 2nd Season - 05 [1080p]", 5, true},
		{"spaced number", "Show 08 [720p].mkv", 8, true},
		{"dash before spaced", "Show 03 - 08 [720p].mkv", 8, true},
		{"the first spaced number", "[Miaou] Series Title 02 VOSTFR FHD 10 bits", 2, true},
		{"eNN", "Show e08.mkv", 8, true},
		{"eNN at the start", "e08 - Show Title.mkv", 8, true},
		{"eNN after a multibyte character", "日e08.mkv", 8, true},
		{"eNN after an e inside a word", "Premiere08 e08.mkv", 8, true},
		{"zero", "Show - 00.mkv", 0, true},
		{"eNN after a codec hyphen", "Show.720p.x264-E02.mkv", 2, true},
		{"eNN after a dotted codec", "Show.H.264-E05.mkv", 5, true},
		{"eNN after audio digits", "Show.DDP5.1-E02.mkv", 2, true},
		{"EP number", "Show EP05 [720p].mkv", 5, true},
		{"Ep and a separated number", "Show Ep 05 [720p].mkv", 5, true},
		{"Episode N", "Show Episode 8.mkv", 8, true},
		{"eNN before a spaced number", "Show e08 10 bits.mkv", 8, true},
		{"spaced number with a version", "Show 05v2 [720p].mkv", 5, true},
		{"eNN beside a dash range", "Show - 01-12 x264-E02.mkv", 2, true},
		{"EP in brackets", "Show [EP05] [720p].mkv", 5, true},

		{"dash resolution", "[Group] Show - 1080p.mkv", 0, false},
		{"dash number glued to a letter", "Show - 05x.mkv", 0, false},
		{"dash range", "Show - 01 ~ 12.mkv", 0, false},
		{"glued dash range", "Show - 01-12.mkv", 0, false},
		{"spaced end of a dash range", "[Erai-raws] Series - 0955 ~ 1005 [1080p]", 0, false},
		{"eNN range", "Show E01-E02.srt", 0, false},
		{"concatenated eNN", "Show E01E02.srt", 0, false},
		{"EP range", "Show EP01-EP02.srt", 0, false},
		{"Episodes is no word", "Show Episodes 01-12.mkv", 0, false},
		{"dash date", "The Series Title - 2014-06-02 - Thomas Piketty.mkv", 0, false},
		{"bracketed dash number", "Show [01 - 12].mkv", 0, false},
		{"bracketed spaced number", "Show ( 08 ) x.mkv", 0, false},
		{"bracketed crc", "[Grp] Movie [BD 1080p][E5A2C3D1]", 0, false},
		{"bracketed digit crc", "[Grp] Movie [BD 1080p][E0123456]", 0, false},
		{"bracketed range", "Show [01-12].mkv", 0, false},
		{"spaced range", "Show Vol 01 ~ 06 [720p].mkv", 0, false},
		{"one digit", "Show - 5.mkv", 0, false},
		{"e inside a word", "Release01.mkv", 0, false},
		{"e after a", "Sae08.mkv", 0, false},
		{"e after z", "Blaze08.mkv", 0, false},
		{"e after a digit", "Show2e08.mkv", 0, false},
		{"eNN glued to a letter", "Show e08x.mkv", 0, false},
		{"spaced number needs both spaces", "Show 08.mkv", 0, false},
		{"marker shape", "Show S01E01 - 26.mkv", 0, false},
		{"unreadable marker shape", "Show S99999999999999999999E01 - 26.mkv", 0, false},
		{"cross marker", "Show 01x08 - 26.mkv", 0, false},
		{"unreadable number", "Show - 99999999999999999999.mkv", 0, false},
		{"nothing", "some random file.mkv", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ep, ok := epmarker.Absolute(tt.input)
			if ep != tt.wantEp || ok != tt.wantOK {
				t.Errorf("Absolute(%q) = (%d, %v), want (%d, %v)",
					tt.input, ep, ok, tt.wantEp, tt.wantOK)
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
