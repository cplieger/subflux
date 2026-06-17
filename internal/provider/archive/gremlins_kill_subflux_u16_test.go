package archive

// Tests in this file target surviving gremlins mutants for unit subflux-u16
// in package internal/provider/archive. Tests only; no production code is
// changed. Helpers/identifiers use the gk_subflux_u16_ prefix.
//
// All three mutants live in MatchesMultiEpisodeRange (zip.go):
//   - 111:58 INVERT_NEGATIVES / ARITHMETIC_BASE — the -1 (n arg) in
//     multiEpRe.FindAllStringSubmatch(base, -1). n=-1 means "all matches";
//     a mutation to +1/1 (or 0) limits the scan to the first match only.
//   - 118:10 CONDITIONALS_BOUNDARY — the `>` in `ep2 > 999` (year/range
//     reject guard); a `>=` mutation would also reject the boundary ep2==999.

import "testing"

// kills zip.go:111:58 INVERT_NEGATIVES and ARITHMETIC_BASE.
// "Show.E01E02.and.E05E08.srt" contains two multi-episode ranges, [1,2] then
// [5,8]. Episode 6 falls only in the SECOND range, so the function must scan
// EVERY regex match (FindAllStringSubmatch with n=-1). Mutating -1 to any
// non-negative n caps the scan at the first match [1,2], which excludes 6, so
// the function would return false.
func Test_gk_subflux_u16_matchesMultiEpisodeRange_scans_all_matches(t *testing.T) {
	t.Parallel()
	const base = "Show.E01E02.and.E05E08.srt"

	if !MatchesMultiEpisodeRange(base, 6) {
		t.Errorf("MatchesMultiEpisodeRange(%q, 6) = false, want true "+
			"(episode in the second range requires scanning all matches, n=-1)", base)
	}

	// Control: an episode in the first range is found regardless of scan depth.
	if !MatchesMultiEpisodeRange(base, 1) {
		t.Errorf("MatchesMultiEpisodeRange(%q, 1) = false, want true", base)
	}
}

// kills zip.go:118:10 CONDITIONALS_BOUNDARY (`ep2 > 999` vs `ep2 >= 999`).
// "E950E999" yields the range [950,999]: ep2 is exactly the boundary 999 and
// the span (49) is within the 50 cap, so the guard must NOT reject it. With a
// `>=` mutation, 999 >= 999 is true, the range is rejected (continue), and the
// interior episode 975 is missed.
func Test_gk_subflux_u16_matchesMultiEpisodeRange_ep2_999_boundary(t *testing.T) {
	t.Parallel()

	if !MatchesMultiEpisodeRange("E950E999", 975) {
		t.Errorf("MatchesMultiEpisodeRange(%q, 975) = false, want true "+
			"(ep2 == 999 must not be rejected by the `> 999` guard)", "E950E999")
	}

	// Control: ep2 == 1000 exceeds the 999 cap and is rejected, so episode 975
	// is not matched even though it lies within [950,1000].
	if MatchesMultiEpisodeRange("E950E1000", 975) {
		t.Errorf("MatchesMultiEpisodeRange(%q, 975) = true, want false "+
			"(ep2 == 1000 exceeds the 999 cap)", "E950E1000")
	}
}
