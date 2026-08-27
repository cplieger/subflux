// Package epmarker is the single authority for reading episode markers out of
// release names and archive member filenames. Every site that asks "which season
// and episode does this name claim?" reads the same scanner, so the scoring
// layer, the archive extractor and the AnimeTosho provider cannot disagree about
// what a marker is.
//
// Notations, all case-insensitive:
//
//   - S##E##, and S## E## with a short separator — the dominant forms, and the
//     unseparated one was all this package read until a real SubSource pack
//     turned up whose ten members were named the other way and were therefore
//     all unreachable. The separated form was measured on two more packs, 54
//     members between them, in "Firefly [S01 E01] - Serenity.eng.srt" shape.
//   - ##x## — Addic7ed's form, as in "Black Sails - 01x08 - VIII.". The episode
//     half must carry at least two digits, which is what keeps an aspect ratio
//     ("4x3", "16x9") from reading as a season and episode, and a match may not
//     begin right after an alphanumeric, which is what keeps a resolution
//     ("1920x1080", whose interior "20x108" is otherwise in range) out.
//   - Multi-episode ranges — E01E02, E01-E02, E01-02, E01.E02 — expanded by
//     Claims against every season the name names.
//
// Views:
//
//   - Find: every well-formed single marker, in order of appearance.
//   - Claims: every episode the name claims, ranges expanded. This is the view a
//     caller selecting an archive member wants.
//   - Target and its Matches: what a lookup is looking for, where the zero value
//     matches anything. Selecting with a Target means a caller has ONE loop
//     rather than one for "find this episode" and another for "take the first".
//   - FirstIndex / Present: where the first marker-SHAPED token starts, and
//     whether one exists at all. Shape presence is deliberately independent of
//     Find: a token whose digits overflow an int is still shaped like a marker,
//     so it must keep suppressing a "this name carries no marker" fallback, but
//     it yields no usable season/episode and Find therefore omits it.
//   - Season: the season claimed by the leading S## token, whether or not an E##
//     follows it — a season pack ("Show.S04.Complete") has no E##.
//
// Deliberately NOT here, because it is per-site policy rather than marker
// syntax: the archive extractor's filepath.Base narrowing, and the AnimeTosho
// provider's standalone absolute-number probes (" - 26", "e26").
//
// Digit runs in the S##E## form are unbounded on purpose: a bound either
// silently truncates a long episode number or silently misses a zero-padded
// season. "S001E01" read under a two-digit bound scanned as season 0 with no
// episode marker at all, which classified a single episode as a season pack, and
// "S01E1234" truncated to 123.
package epmarker

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

// markerRe matches one S##E## token, allowing a short separator between the two
// halves. A space is the form measured in the wild ("Firefly [S01 E01] - …",
// "S01 E01 Winter is Coming SDH.srt"); a dot, underscore and hyphen ride along
// because they are the same choice by a different uploader.
//
// The separator cannot pull a match out of ordinary text, because the pattern
// still needs digits immediately after the S AND digits immediately after the E:
// an audio tag like "DTS 5.1 EAC3" has no digit after its S, and a word like
// "Edition" has no digit after its E.
var markerRe = regexp.MustCompile(`(?i)S(\d+)[ ._-]{0,3}E(\d+)`)

// crossRe matches one ##x## token. The episode half requires two or three
// digits: real episode notation is zero-padded ("01x08") while the tokens this
// must not claim are not ("4x3", "16x9"). The season half is capped at two
// digits so a resolution cannot be consumed whole, and what rejects a
// resolution's interior match is the alphanumeric-boundary check in findCross.
var crossRe = regexp.MustCompile(`(?i)(\d{1,2})x(\d{2,3})`)

// multiEpRe matches multi-episode ranges like E01E02, E01-E02, E01-02.
// Requires either a second E prefix or a separator (- or .) between episode
// numbers to avoid matching single episodes (e.g. E05 as range [0,5]).
var multiEpRe = regexp.MustCompile(`(?i)E(\d+)(?:[-.]E?|E)(\d+)`)

// seasonRe matches the season half of a marker on its own, so a season pack is
// readable through the same authority as an episode.
var seasonRe = regexp.MustCompile(`(?i)S(\d+)`)

// seasonWordRe matches a season stated as a word, "Season 1" or "Season.01".
// Measured on real SubSource release names: "Game Of Thrones Season 1 (2011) …"
// and "Show - Season 10 - Complete" state their season only this way, and while
// that was unread the scorer's wrong-season rejection was inert for them,
// because it is gated on a season being readable at all.
//
// A plural "Seasons 1-9" deliberately does NOT match: the separator class cannot
// consume the "s", and a multi-season bundle claims no single season.
var seasonWordRe = regexp.MustCompile(`(?i)\bseason[ ._-]*(\d+)`)

// seasonForms are the explicit season statements, ordered by how directly each
// names one. The first form PRESENT decides.
var seasonForms = []*regexp.Regexp{seasonRe, seasonWordRe}

// bareEpisodeRe matches a name that OPENS with an episode number: one to three
// digits and then a separator. The separator is what keeps a resolution out
// ("1080p" has a digit where the separator must be), and the leading anchor is
// what keeps a number from being read out of the middle of a title.
var bareEpisodeRe = regexp.MustCompile(`^(\d{1,3})[ ._-]`)

// maxCrossSeason and maxCrossEpisode bound the ##x## form. A season above 99 or
// an episode above 999 is a number that happens to contain an "x", not an
// episode marker.
const (
	maxCrossSeason  = 99
	maxCrossEpisode = 999
)

// maxRangeSpan and maxRangeEpisode bound a multi-episode range, rejecting the
// year numbers that otherwise read as one: "1923" in "S01E01.1923.REPACK" would
// span 1 to 923.
const (
	maxRangeSpan    = 50
	maxRangeEpisode = 999
)

// Marker is the season/episode pair one token claims. Both numbers are
// non-negative: they are read from digit runs, so "S00E00" is a legitimate
// Marker{0, 0} rather than a miss.
type Marker struct {
	Season  int
	Episode int
}

// String renders a marker in the canonical S##E## form, which is what a
// diagnostic naming the episode a caller looked for should say.
func (m Marker) String() string {
	return "S" + pad2(m.Season) + "E" + pad2(m.Episode)
}

func pad2(n int) string {
	if n >= 0 && n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// Target is what a lookup is looking for. The zero Target matches any name,
// which is what a movie download and a provider that reports no numbering both
// arrive with, so a caller selecting from candidates needs one loop rather than
// one per case.
type Target struct {
	want  Marker
	fixed bool
}

// Any returns the Target that matches every name.
func Any() Target { return Target{} }

// For returns the Target that matches only m, or Any when either half of m is
// absent. A zero season or episode is how the wire says "this download has no
// episode to disambiguate", so a caller can hand over a subtitle's fields
// without deciding what that means.
func For(m Marker) Target {
	if m.Season <= 0 || m.Episode <= 0 {
		return Any()
	}
	return Target{want: m, fixed: true}
}

// Matches reports whether name claims the episode t is looking for. An Any
// target matches every name.
func (t Target) Matches(name string) bool {
	if !t.fixed {
		return true
	}
	return slices.Contains(Claims(name), t.want)
}

// MatchesBareEpisode reports whether name states t's episode as a bare leading
// number, with no season anywhere in the name.
//
// Separate from Matches because it is only sound with information the NAME does
// not carry: the season. Two conditions must hold before a caller uses it, and
// neither is checkable from here.
//
// The season must be known from outside the name, and known reliably rather than
// assumed. In subflux it is: the provider search sends seasonNumber to the API and
// the result's season is stamped from that same request, so a target's season came
// from Sonarr and not from any filename.
//
// And no member of the same archive may claim a readable marker, which only the
// caller holding the whole member list can decide. That condition is what stops
// this reading from ever outvoting a notation that does parse.
func (t Target) MatchesBareEpisode(name string) bool {
	if !t.fixed {
		return false
	}
	ep, ok := BareEpisode(name)
	return ok && ep == t.want.Episode
}

// String renders t for a diagnostic: the episode in canonical form, or "any"
// when nothing narrows the lookup.
func (t Target) String() string {
	if !t.fixed {
		return "any episode"
	}
	return t.want.String()
}

// Find returns every well-formed single marker in name, in order of appearance,
// across both notations. Callers that care about precedence pick from the
// returned order themselves. A marker-shaped token whose season or episode does
// not fit an int is skipped here but still reported by Present.
func Find(name string) []Marker {
	found := findStandard(name)
	found = append(found, findCross(name)...)
	slices.SortStableFunc(found, func(a, b located) int { return a.at - b.at })

	out := make([]Marker, 0, len(found))
	for _, f := range found {
		out = append(out, f.Marker)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// located is a marker with the byte offset it was read from, so markers found by
// two different patterns can be merged into one appearance order.
type located struct {
	Marker
	at int
}

// findStandard reads every S##E## token.
func findStandard(name string) []located {
	var out []located
	for _, m := range markerRe.FindAllStringSubmatchIndex(name, -1) {
		season, sErr := strconv.Atoi(name[m[2]:m[3]])
		episode, eErr := strconv.Atoi(name[m[4]:m[5]])
		if sErr != nil || eErr != nil {
			continue
		}
		out = append(out, located{Marker{Season: season, Episode: episode}, m[0]})
	}
	return out
}

// findCross reads every ##x## token that survives its guards.
//
// The boundary check is what a lookbehind would do if Go's regexp had one: a
// match starting immediately after a letter or digit is part of a longer number
// or word, not a marker. That is the check which rejects "1920x1080", whose
// interior "20x108" is otherwise a season and episode in range.
func findCross(name string) []located {
	var out []located
	for _, m := range crossRe.FindAllStringSubmatchIndex(name, -1) {
		if m[0] > 0 && isAlnum(name[m[0]-1]) {
			continue
		}
		season, sErr := strconv.Atoi(name[m[2]:m[3]])
		episode, eErr := strconv.Atoi(name[m[4]:m[5]])
		if sErr != nil || eErr != nil {
			continue
		}
		if season > maxCrossSeason || episode > maxCrossEpisode {
			continue
		}
		out = append(out, located{Marker{Season: season, Episode: episode}, m[0]})
	}
	return out
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// Claims returns every episode name claims, with multi-episode ranges expanded
// against each season the name names. Ordered, deduplicated, nil when the name
// claims nothing.
//
// This is the view for selecting an archive member. A range only means something
// once a season is known, so "S01E01-E02" claims S01E01 and S01E02 while a bare
// "E01-E02" claims nothing: attaching a range to a season nobody stated would be
// guessing, and the cost of guessing wrong is the wrong episode's subtitle
// written under the right episode's name.
func Claims(name string) []Marker {
	singles := Find(name)
	claims := slices.Clone(singles)

	ranges := findRanges(name)
	if len(ranges) > 0 {
		for _, season := range seasonsOf(singles) {
			for _, r := range ranges {
				for ep := r[0]; ep <= r[1]; ep++ {
					claims = append(claims, Marker{Season: season, Episode: ep})
				}
			}
		}
	}

	slices.SortFunc(claims, func(a, b Marker) int {
		if a.Season != b.Season {
			return a.Season - b.Season
		}
		return a.Episode - b.Episode
	})
	claims = slices.Compact(claims)
	if len(claims) == 0 {
		return nil
	}
	return claims
}

// findRanges returns each multi-episode span in name as a [first, last] pair.
func findRanges(name string) [][2]int {
	var out [][2]int
	for _, m := range multiEpRe.FindAllStringSubmatch(name, -1) {
		first, err1 := strconv.Atoi(m[1])
		last, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil {
			continue
		}
		if last > maxRangeEpisode || last-first > maxRangeSpan || last < first {
			continue
		}
		out = append(out, [2]int{first, last})
	}
	return out
}

// seasonsOf returns the distinct seasons a set of markers names, in order.
func seasonsOf(markers []Marker) []int {
	var out []int
	for _, m := range markers {
		if !slices.Contains(out, m.Season) {
			out = append(out, m.Season)
		}
	}
	return out
}

// FirstIndex returns the byte offset at which the first marker-shaped token
// starts, or -1 when name carries none. Used to split a release name into its
// title portion and its marker portion.
func FirstIndex(name string) int {
	first := -1
	if loc := markerRe.FindStringIndex(name); loc != nil {
		first = loc[0]
	}
	// The cross form's guards decide whether a token is marker-SHAPED at all, so
	// the shape question reads the same guarded scan Find does.
	if cross := findCross(name); len(cross) > 0 {
		if first < 0 || cross[0].at < first {
			first = cross[0].at
		}
	}
	return first
}

// Present reports whether name carries a marker-shaped token at all. This is
// the gate for "the name numbers its episodes some other way, fall back":
// a name containing an unparseable marker is still explicitly numbered, so it
// must not reach an absolute-number or season-pack fallback.
func Present(name string) bool { return FirstIndex(name) >= 0 }

// Season returns the season claimed by the first explicit season statement in
// name, whether written compactly ("S01", with or without a trailing E##) or as
// a word ("Season 1"). ok is false when name carries no season token or the
// token's digits do not fit an int.
//
// Falls back last to the cross form's season, because an authority that reported
// no season for "Black Sails - 01x08" while Find reported season 1 from the same
// token would be contradicting itself, and this package exists so its callers
// cannot disagree about what a marker says.
func Season(name string) (season int, ok bool) {
	// The first form PRESENT decides, whether or not its digits are readable: a
	// token too wide for an int is unreadable rather than absent, so falling
	// through would answer with a different notation's season.
	for _, re := range seasonForms {
		if m := re.FindStringSubmatch(name); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				// Atoi clamps an overflowing run to MaxInt64 and returns it
				// alongside the error, so the value has to be dropped here: a
				// caller that ignored ok would otherwise read a season number
				// no season has.
				return 0, false
			}
			return n, true
		}
	}
	if cross := findCross(name); len(cross) > 0 {
		return cross[0].Season, true
	}
	return 0, false
}

// BareEpisode returns the episode a name states as a bare leading number, with
// no season anywhere in it: "6 - A Golden Crown..srt" states episode 6.
//
// This is a reading, not a match. It is only SOUND alongside information the name
// does not carry, so it is reported separately from Find and Claims and never
// folded into them: a bare number says which episode of some season, and nothing
// about which season. Target.MatchesBareEpisode states the conditions a caller
// must meet before using it.
//
// A name that carries a readable marker returns false, so the two readings can
// never both answer for one name.
func BareEpisode(name string) (episode int, ok bool) {
	if len(Claims(name)) > 0 {
		return 0, false
	}
	m := bareEpisodeRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// Compile-time proof that both diagnostic renderings satisfy fmt.Stringer, per
// the fleet rule that a well-known interface method is paired with an assertion
// at the implementation site. Both have production call sites in the archive
// extractor's refusal messages.
var (
	_ fmt.Stringer = Marker{}
	_ fmt.Stringer = Target{}
)
