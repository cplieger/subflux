// Package epmarker is the single authority for reading S##E## episode markers
// out of release names and archive member filenames. Every site that asks
// "which season and episode does this name claim?" reads the same scanner, so
// the scoring layer, the archive extractor and the AnimeTosho provider can no
// longer disagree about what a marker is.
//
// Views (all pure, all case-insensitive):
//
//   - Find: every well-formed marker in the name, in order of appearance.
//   - FirstIndex / Present: where the first marker-SHAPED token starts, and
//     whether one exists at all. Shape presence is deliberately independent of
//     Find: a token whose digits overflow an int is still shaped like a marker,
//     so it must keep suppressing a "this name carries no marker" fallback,
//     but it yields no usable season/episode and Find therefore omits it.
//   - Season: the season claimed by the leading S## token, whether or not an
//     E## follows it — a season pack ("Show.S04.Complete") has no E##.
//
// Deliberately NOT here, because it is per-site policy rather than marker
// syntax: the archive extractor's multi-episode range scan (E01-E02) and its
// filepath.Base narrowing, and the AnimeTosho provider's standalone
// absolute-number probes (" - 26", "e26"). Those live at their one call site.
//
// Seed evidence (2026-07 triplication removal): the table below is the union of
// the three scanners it replaced — scoring's bounded pair
// (S\d{1,2}E\d{1,3} plus S(\d{1,2}) for the season-only form) and the
// byte-identical unbounded S(\d+)E(\d+) used by archive and animetosho. The
// unbounded reading won: the bounded one mis-read zero-padded and long markers
// ("S001E01" scanned as season 0 with NO episode marker, so a single episode
// was classified as a season pack), and it truncated long episode numbers
// ("S01E1234" -> 123). See TestFind_diverged_from_bounded_scoring_reading for
// the exact inputs whose reading changed and why.
package epmarker

import (
	"regexp"
	"strconv"
)

// markerRe matches one S##E## token. Digit runs are unbounded on purpose: a
// bound either silently truncates a long episode number or silently misses a
// zero-padded season, and both are worse than reading the whole run and
// letting strconv reject what cannot fit an int.
var markerRe = regexp.MustCompile(`(?i)S(\d+)E(\d+)`)

// seasonRe matches the season half of a marker on its own, so a season pack is
// readable through the same authority as an episode.
var seasonRe = regexp.MustCompile(`(?i)S(\d+)`)

// Marker is the season/episode pair one S##E## token claims. Both numbers are
// non-negative: they are read from digit runs, so "S00E00" is a legitimate
// Marker{0, 0} rather than a miss.
type Marker struct {
	Season  int
	Episode int
}

// Find returns every well-formed marker in name, in order of appearance.
// Callers that care about precedence pick from the returned order themselves
// (the archive extractor accepts any match, AnimeTosho accepts any exact
// season+episode match). A marker-shaped token whose season or episode does
// not fit an int is skipped here but still reported by Present.
func Find(name string) []Marker {
	all := markerRe.FindAllStringSubmatch(name, -1)
	if all == nil {
		return nil
	}
	out := make([]Marker, 0, len(all))
	for _, m := range all {
		season, sErr := strconv.Atoi(m[1])
		episode, eErr := strconv.Atoi(m[2])
		if sErr != nil || eErr != nil {
			continue
		}
		out = append(out, Marker{Season: season, Episode: episode})
	}
	return out
}

// FirstIndex returns the byte offset at which the first marker-shaped token
// starts, or -1 when name carries none. Used to split a release name into its
// title portion and its marker portion.
func FirstIndex(name string) int {
	loc := markerRe.FindStringIndex(name)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// Present reports whether name carries a marker-shaped token at all. This is
// the gate for "the name numbers its episodes some other way, fall back":
// a name containing an unparseable marker is still explicitly numbered, so it
// must not reach an absolute-number or season-pack fallback.
func Present(name string) bool { return FirstIndex(name) >= 0 }

// Season returns the season claimed by the first S## token in name, with or
// without a trailing E##. ok is false when name carries no season token or the
// token's digits do not fit an int.
func Season(name string) (season int, ok bool) {
	m := seasonRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}
