package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// Tests in this file kill surviving gremlins mutation-testing mutants in
// internal/search/scoring/identity_match.go (lines 114, 118, 140) and
// internal/search/scoring/score.go (line 111) for unit subflux-u10. Every
// identifier defined here is prefixed gk_subflux_u10_ so it never collides
// with sibling units subflux-u8 / subflux-u9, which share package scoring.

// --- releaseNameMatchesTitleWith: run-in suffix returns true (identity_match.go:114) ---
//
// Kills 114:12 (CONDITIONALS_NEGATION on `b[end] != ' '`). When the requested
// title is found as a word-prefix and the very next char is NOT a space
// (a "run-in", e.g. "dragon ball" inside "dragon ballz"), the original returns
// true immediately. Flipping `!=` to `==` skips that early return and falls
// through to the sequel-suffix check; the run-in token "z" is a sequel
// indicator, so the mutant returns false instead of true.
func Test_gk_subflux_u10_releaseNameMatchesTitle_runInSuffix(t *testing.T) {
	// "dragon ball" is a prefix of "dragon ballz" with no separating space:
	// b[end]=='z' (non-space) -> original true; `==` mutant falls through and
	// the sequel token "z" -> false.
	got := ReleaseNameMatchesTitle("dragon ball", "dragon ballz")
	if got != true {
		t.Errorf("ReleaseNameMatchesTitle(%q, %q) = %v, want true", "dragon ball", "dragon ballz", got)
	}
}

// --- releaseNameMatchesTitleWith: empty-remainder branch (identity_match.go:118) ---
//
// Kills 118:10 (CONDITIONALS_NEGATION on `rest == ""`). After a space follows
// the matched title, `rest` is the trimmed remainder. The original returns
// true only when `rest` is empty; otherwise it falls through to the
// sequel-suffix check. Here `rest`=="z" (a sequel indicator), so the original
// returns false. Flipping `==` to `!=` makes the non-empty remainder short
// circuit to true, so the mutant returns true instead of false.
func Test_gk_subflux_u10_releaseNameMatchesTitle_sequelTokenAfterSpace(t *testing.T) {
	// "dragon ball" matched, then a space and the sequel token "z":
	// rest=="z" (non-empty) -> original returns !sequelIndicators["z"] == false;
	// `!=` mutant returns true because the remainder is non-empty.
	got := ReleaseNameMatchesTitle("dragon ball", "dragon ball z")
	if got != false {
		t.Errorf("ReleaseNameMatchesTitle(%q, %q) = %v, want false", "dragon ball", "dragon ball z", got)
	}
}

// --- TitlesMatch: normalized equality (identity_match.go:140) ---
//
// Kills 140:11 (CONDITIONALS_NEGATION on `return a == b`). Both titles are
// non-empty after normalization so the empty-operand guard does not fire and
// the final `a == b` decides the result. The equal row pins true (the `!=`
// mutant returns false); the unequal row pins false (the `!=` mutant returns
// true).
func Test_gk_subflux_u10_titlesMatch_equality(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		candidate string
		want      bool
	}{
		// Same title via different separators -> both normalize to
		// "breaking bad": original a==b true; `!=` mutant false.
		{"equal after normalization matches", "Breaking Bad", "breaking-bad", true},
		// Distinct non-empty titles: original a==b false; `!=` mutant true.
		{"distinct titles do not match", "Breaking Bad", "Better Call Saul", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TitlesMatch(tc.requested, tc.candidate)
			if got != tc.want {
				t.Errorf("TitlesMatch(%q, %q) = %v, want %v", tc.requested, tc.candidate, got, tc.want)
			}
		})
	}
}

// --- MatchBreakdown: zero-weight matched category is excluded (score.go:111) ---
//
// Kills 111:53 (CONDITIONALS_BOUNDARY on `w > 0`). A category that matches but
// carries weight 0 must be excluded from the breakdown: the original `0 > 0`
// is false so the key is omitted (empty map). The boundary mutant `w >= 0`
// makes `0 >= 0` true, so it adds the key with value 0 (len 1). The positive
// row is a control: a matched weight of 28 is included under both operators.
func Test_gk_subflux_u10_matchBreakdown_zeroWeightExcluded(t *testing.T) {
	tests := []struct {
		name         string
		scores       api.Scores
		matches      api.MatchSet
		wantLen      int
		wantKey      string
		wantKeyVal   int
		wantKeyThere bool
	}{
		{
			// Matched source with weight 0: original `0 > 0` false -> excluded
			// (empty); `0 >= 0` mutant includes it with value 0 (len 1).
			name:         "zero weight matched category excluded",
			scores:       api.Scores{Source: 0},
			matches:      api.MatchSet{Source: true},
			wantLen:      0,
			wantKey:      "source",
			wantKeyThere: false,
		},
		{
			// Control: positive weight is included regardless of `>` vs `>=`.
			name:         "positive weight matched category included",
			scores:       api.Scores{Source: 28},
			matches:      api.MatchSet{Source: true},
			wantLen:      1,
			wantKey:      "source",
			wantKeyVal:   28,
			wantKeyThere: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scores := tc.scores
			got := MatchBreakdown(&scores, tc.matches)
			if len(got) != tc.wantLen {
				t.Errorf("len(MatchBreakdown(%+v, %+v)) = %d, want %d; got %v",
					tc.scores, tc.matches, len(got), tc.wantLen, got)
			}
			v, ok := got[tc.wantKey]
			if ok != tc.wantKeyThere {
				t.Errorf("MatchBreakdown(%+v, %+v) key %q present = %v, want %v",
					tc.scores, tc.matches, tc.wantKey, ok, tc.wantKeyThere)
			}
			if tc.wantKeyThere && v != tc.wantKeyVal {
				t.Errorf("MatchBreakdown(%+v, %+v)[%q] = %d, want %d",
					tc.scores, tc.matches, tc.wantKey, v, tc.wantKeyVal)
			}
		})
	}
}
