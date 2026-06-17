package release

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// Tests in this file kill surviving gremlins mutation-testing mutants in
// release.go (ParseReleaseName, ParseReleaseGroup, CompareSource) and pcre.go
// (skipCharClass loop bound) for unit subflux-u12. Each assertion's expected
// value depends on the exact operator at the targeted line, so applying the
// mutation changes the asserted observable.

// gk_subflux_u12_didPanic reports whether calling f panics.
func gk_subflux_u12_didPanic(f func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	f()
	return false
}

// --- ParseReleaseName: if name == "" (release.go:31) ---

// Kills 31:10 CONDITIONALS_NEGATION (== vs !=): an empty name must short-circuit
// to a zero Info, while a populated name must be parsed. Flipping == to != would
// return Info{} for the populated name (Source would be empty) and parse "".
func Test_gk_subflux_u12_ParseReleaseName_emptyVsPopulated(t *testing.T) {
	const populated = "Some.Movie.2020.1080p.BluRay.x264-RARBG"

	if got := ParseReleaseName(""); got != (Info{}) {
		t.Errorf("ParseReleaseName(%q) = %#v, want zero Info{}", "", got)
	}

	got := ParseReleaseName(populated)
	if got.Source != "bluray" {
		t.Errorf("ParseReleaseName(%q).Source = %q, want %q", populated, got.Source, "bluray")
	}
}

// --- ParseReleaseName: if m := editionRe.FindString(name); m != "" (release.go:41) ---

// Kills 41:40 CONDITIONALS_NEGATION (!= vs ==): a name containing an edition
// keyword must set Edition to the lowercased match; flipping != to == would
// only assign when the match is empty, leaving Edition blank for this input.
func Test_gk_subflux_u12_ParseReleaseName_editionDetected(t *testing.T) {
	const withEdition = "The.Movie.2021.Remastered.1080p.BluRay.x264-GRP"
	if got := ParseReleaseName(withEdition).Edition; got != "remastered" {
		t.Errorf("ParseReleaseName(%q).Edition = %q, want %q", withEdition, got, "remastered")
	}

	const noEdition = "The.Movie.2021.1080p.BluRay.x264-GRP"
	if got := ParseReleaseName(noEdition).Edition; got != "" {
		t.Errorf("ParseReleaseName(%q).Edition = %q, want %q", noEdition, got, "")
	}
}

// --- ParseReleaseGroup: release-group block (release.go:67, 68) ---

// Kills 67:63 CONDITIONALS_NEGATION (m != nil vs m == nil),
// 68:13 CONDITIONALS_NEGATION (len(m) > 1 vs len(m) <= 1), and
// 68:25 CONDITIONALS_NEGATION (m[1] != "" vs m[1] == ""): a name with a trailing
// scene group must return that group. Each mutation makes the matched-group
// branch be skipped, so the function falls through and returns "".
func Test_gk_subflux_u12_ParseReleaseGroup_returnsGroup(t *testing.T) {
	const withGroup = "Some.Movie.2020.1080p.BluRay.x264-RARBG"
	if got := ParseReleaseGroup(withGroup); got != "RARBG" {
		t.Errorf("ParseReleaseGroup(%q) = %q, want %q", withGroup, got, "RARBG")
	}

	const noGroup = "Plain Title 2020 1080p"
	if got := ParseReleaseGroup(noGroup); got != "" {
		t.Errorf("ParseReleaseGroup(%q) = %q, want %q", noGroup, got, "")
	}
}

// Coverage for the anime-group branch (release.go:63). NOTE: the 63:73
// CONDITIONALS_BOUNDARY mutant (> vs >=) is equivalent — FindStringSubmatch on
// CompiledAnimeReleaseGroup (1 capture group) returns len 0 or 2, never 1, so
// the boundary at 1 is unreachable. This test documents the true branch.
func Test_gk_subflux_u12_ParseReleaseGroup_animeBracketGroup(t *testing.T) {
	const anime = "[HorribleSubs] Show - 01 [1080p].mkv"
	if got := ParseReleaseGroup(anime); got != "HorribleSubs" {
		t.Errorf("ParseReleaseGroup(%q) = %q, want %q", anime, got, "HorribleSubs")
	}
}

// --- CompareSource: guard + family comparison (release.go:96, 99) ---

// Kills 96:7 and 96:18 CONDITIONALS_NEGATION (a == "" / b == "" -> a != "" /
// b != "") and 99:23 CONDITIONALS_NEGATION (== vs !=): two non-empty,
// same-family sources must set matches.Source = true. Negating either guard
// turns the OR-guard into an early return for two non-empty operands; negating
// the family comparison makes equal families compare unequal. All three leave
// Source false.
func Test_gk_subflux_u12_CompareSource_sameFamily(t *testing.T) {
	var ms api.MatchSet
	CompareSource(&ms, NormWebDL, NormWebRip)
	if ms.Source != true {
		t.Errorf("CompareSource(%q, %q).Source = %v, want true", NormWebDL, NormWebRip, ms.Source)
	}
}

// Kills 99:23 CONDITIONALS_NEGATION (== vs !=) from the other direction: two
// non-empty sources in DIFFERENT families must leave matches.Source = false.
// Negating == to != would set it true.
func Test_gk_subflux_u12_CompareSource_differentFamily(t *testing.T) {
	var ms api.MatchSet
	CompareSource(&ms, NormWebDL, NormBluray)
	if ms.Source != false {
		t.Errorf("CompareSource(%q, %q).Source = %v, want false", NormWebDL, NormBluray, ms.Source)
	}
}

// Coverage for the empty-operand guard (release.go:96): an empty source must
// leave Source false. (Documents the guard; the 96 negations are killed by the
// same-family test above.)
func Test_gk_subflux_u12_CompareSource_emptyOperand(t *testing.T) {
	var ms api.MatchSet
	CompareSource(&ms, "", NormWebDL)
	if ms.Source != false {
		t.Errorf("CompareSource(%q, %q).Source = %v, want false", "", NormWebDL, ms.Source)
	}
}

// --- skipCharClass: for-loop bound (pcre.go:277) ---

// Kills 277:10 CONDITIONALS_BOUNDARY (pos < len(s) vs pos <= len(s)): an
// unterminated character class whose contents run to end-of-string must return
// len(s) cleanly. With <=, the loop runs one extra iteration at pos==len(s) and
// reads s[len(s)] out of range, panicking. The two guards above (lines 271/274)
// short-circuit cleanly here, so this isolates the line-277 loop bound.
func Test_gk_subflux_u12_skipCharClass_loopBoundaryNoOverread(t *testing.T) {
	const unterminated = "[ab"
	var got int
	if gk_subflux_u12_didPanic(func() { got = skipCharClass(unterminated, 0) }) {
		t.Fatalf("skipCharClass(%q, 0) panicked; want %d", unterminated, len(unterminated))
	}
	if got != 3 {
		t.Errorf("skipCharClass(%q, 0) = %d, want 3", unterminated, got)
	}
}
