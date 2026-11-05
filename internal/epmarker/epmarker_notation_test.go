package epmarker_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/epmarker"
)

// notation writes an episode statement after season 2: season, then episode
// rendering one number or a run of them. Every read path of the package has a
// row: a marker (glued, separated, concatenated, dotted), the cross form, the
// season word, E##, EP##, Episode N, " - NN" (with a version), a spaced
// number, a bare range and a bracketed " - " range.
type notation struct {
	name    string
	season  string
	episode func(nums []int) string
	marker  bool // a marker, which carries its season, unless a bracket parts its S## and E##
	bare    bool // the episode part is digits with no letter before them
	spaced  bool // bare, and a single number stands between spaces
	// plain is false where only brackets make the notation a range: outside
	// them a " - " parts a title's number from the episode's, so "S2 01 - 12"
	// claims E12 (TestClaims "spaced hyphen after a season").
	plain bool
}

func run(first, next string) func([]int) string {
	return func(nums []int) string {
		s := fmt.Sprintf(first, nums[0])
		for _, n := range nums[1:] {
			s += fmt.Sprintf(next, n)
		}
		return s
	}
}

var notations = []notation{
	{name: "S##E##", episode: run("S02E%02d", "-E%02d"), marker: true, plain: true},
	{name: "S##E## glued to the title", episode: run("ShowS02E%02d", "-E%02d"), marker: true, plain: true},
	{name: "S##E##E##", episode: run("S02E%02d", "E%02d"), marker: true, plain: true},
	{name: "S##E##.E##", episode: run("S02E%02d", ".E%02d"), marker: true, plain: true},
	{name: "S## E##", season: "S02 ", episode: run("E%02d", "-E%02d"), marker: true, plain: true},
	{name: "S##.E##", season: "S02.", episode: run("E%02d", "-E%02d"), marker: true, plain: true},
	{name: "NxNN", episode: run("2x%02d", "-%02d"), marker: true, plain: true},
	{name: "Season.N.E##", season: "Season.2.", episode: run("E%02d", "-E%02d"), plain: true},
	{name: "Season N Episode N", season: "Season 2 ", episode: run("Episode %d", "-%d"), plain: true},
	{name: "S N EP##", season: "S2 ", episode: run("EP%02d", "-EP%02d"), plain: true},
	{name: "S N EP##-##", season: "S2 ", episode: run("EP%02d", "-%02d"), plain: true},
	{name: "S N - ##", season: "S2 - ", episode: run("%02d", "-%02d"), bare: true, plain: true},
	{name: "S N - ## ~ ##", season: "S2 - ", episode: run("%02d", " ~ %02d"), bare: true, plain: true},
	{name: "S N - ##v#", season: "S2 - ", episode: run("%02dv2", "-%02dv2"), bare: true, plain: true},
	{name: "Season N - ##", season: "Season 2 - ", episode: run("%02d", "-%02d"), bare: true, plain: true},
	{name: "S N ##-##", season: "S2 ", episode: run("%02d", "-%02d"), bare: true, spaced: true, plain: true},
	{name: "S N ## ~ ##", season: "S2 ", episode: run("%02d", " ~ %02d"), bare: true, spaced: true, plain: true},
	{name: "S N [## - ##]", season: "S2 ", episode: run("%02d", " - %02d"), bare: true},
}

// claims lists what each grid row states of season 2's episodes. A lettered
// run that is no range (decreasing, or ending past episode 999) states its
// first number; a range past the span bound claims its first number only.
var claims = []struct {
	name    string
	nums    []int
	want    []int
	isRange bool // non-decreasing, ending by episode 999
	bare    bool // only bare digits write it
}{
	{name: "one episode", nums: []int{5}, want: []int{5}},
	{name: "a range", nums: []int{1, 12}, want: span(1, 12), isRange: true},
	{name: "a range past the span bound", nums: []int{1, 60}, want: []int{1}, isRange: true},
	{name: "a run ending past episode 999", nums: []int{1, 1923}, want: []int{1}},
	{name: "a decreasing run", nums: []int{5, 3}, want: []int{5}},
	{name: "a date", nums: []int{2014, 6, 2}, bare: true},
}

func span(first, last int) []int {
	var out []int
	for ep := first; ep <= last; ep++ {
		out = append(out, ep)
	}
	return out
}

// placements write a notation as it stands, with its episode part in brackets
// ("S2 [01-12]") and wholly in brackets ("[S01 E01]").
var placements = []struct {
	label             string
	bracketed, splits bool // splits: a bracket stands between season and episode part
	write             func(season, episode string) string
}{
	{"", false, false, func(season, episode string) string { return season + episode }},
	{" in brackets", true, true, func(season, episode string) string {
		return strings.TrimLeft(strings.TrimRight(season, " .-")+" [", " ") + episode + "]"
	}},
	{" wholly in brackets", true, false, func(season, episode string) string { return "[" + season + episode + "]" }},
}

// differences are the only places one claim reads differently by notation;
// each states nothing there. The spaced-hyphen range is written only in
// brackets, for the reason on notation.plain.
var differences = []struct {
	reason  string
	applies func(n notation, nums []int, isRange, bracketed bool) bool
}{
	{
		`a spaced number may be a bit depth ("10 bit")`,
		func(n notation, nums []int, _, bracketed bool) bool { return n.spaced && len(nums) == 1 && !bracketed },
	},
	{
		"brackets hold CRCs, bit depths and years, so bare digits there read only as a range",
		func(n notation, nums []int, _, bracketed bool) bool { return n.bare && len(nums) == 1 && bracketed },
	},
	{
		`bare digits that are no range are a date's or a year's ("2014-06-02")`,
		func(n notation, nums []int, isRange, _ bool) bool { return n.bare && len(nums) > 1 && !isRange },
	},
}

// contexts write what stands before the episode part: nothing, an earlier
// season statement, a marker (S02E07), or a counting word before bare digits.
// claims, when set, replaces what the claim states there.
var contexts = []struct {
	label, before, word, why string
	season                   int  // Season's answer: the name's first S##
	bare                     bool // only before a bare episode part
	claims                   func(marker bool, n notation, nums, want []int) []int
}{
	{season: 2},
	{label: " after an earlier season", before: "S01 ", season: 1},
	{
		label: " after a marker", before: "S02E07.", season: 2,
		why:    ": beside a marker only a marker or a lettered run of numbers adds episodes",
		claims: besideMarker,
	},
	{
		label: " after a counting word", word: "Vol.", season: 2, bare: true,
		why:    ": a counting word's digits are a volume's or a part's",
		claims: func(bool, notation, []int, []int) []int { return nil },
	},
}

func besideMarker(marker bool, n notation, nums, want []int) []int {
	if !marker && (n.bare || len(nums) == 1) {
		return []int{7}
	}
	out := append([]int{7}, want...)
	slices.Sort(out)
	return slices.Compact(out)
}

// TestNotationGrid renders each claim in every notation, plain and inside
// brackets, after each context, and asserts StatesEpisode and Claims agree
// but where differences or the context names why. Season reads the context's
// season, and every name's claims fall under season 2.
func TestNotationGrid(t *testing.T) {
	t.Parallel()
	for _, c := range claims {
		for _, n := range notations {
			if c.bare && !n.bare {
				continue
			}
			for _, at := range placements {
				if !at.bracketed && !n.plain {
					continue
				}
				want, why := c.want, ""
				for _, d := range differences {
					if d.applies(n, c.nums, c.isRange, at.bracketed) {
						want, why = nil, ": "+d.reason
					}
				}
				for _, cx := range contexts {
					if cx.bare && !n.bare {
						continue
					}
					name := "[Grp] Show " + cx.before + at.write(n.season, cx.word+n.episode(c.nums)) + " [1080p]"
					want, why := want, why
					if cx.claims != nil {
						marker := n.marker && (!at.splits || n.season == "")
						want, why = cx.claims(marker, n, c.nums, want), cx.why
					}
					t.Run(c.name+" as "+n.name+at.label+cx.label, func(t *testing.T) {
						t.Parallel()
						assertReads(t, name, cx.season, want, why)
					})
				}
			}
		}
	}
}

func assertReads(t *testing.T, name string, season int, episodes []int, why string) {
	t.Helper()
	var want []epmarker.Marker
	for _, ep := range episodes {
		want = append(want, epmarker.Marker{Season: 2, Episode: ep})
	}
	if got, ok := epmarker.Season(name); got != season || !ok {
		t.Fatalf("Season(%q) = (%d, %v), want (%d, true)", name, got, ok, season)
	}
	if got := epmarker.StatesEpisode(name); got != (want != nil) {
		t.Errorf("StatesEpisode(%q) = %v, want %v%s", name, got, want != nil, why)
	}
	if got := epmarker.Claims(name); !slices.Equal(got, want) {
		t.Errorf("Claims(%q) = %v, want %v%s", name, got, want, why)
	}
}
