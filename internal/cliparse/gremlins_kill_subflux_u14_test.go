package cliparse

import "testing"

// Tests added to kill surviving gremlins mutants in cliparse.go (unit
// subflux-u14). Internal test package so the (already exported) SuggestName
// is reached in the same package as the production code. All identifiers
// defined here are prefixed gk_subflux_u14_ to avoid colliding with the
// sibling unit subflux-u13 that shares this package.
//
// All seven targeted mutants live in SuggestName:
//
//	cliparse.go:269:14 ARITHMETIC_BASE   — `bestDist := -1` sentinel
//	cliparse.go:269:14 INVERT_NEGATIVES  — `bestDist := -1` sentinel
//	cliparse.go:273:8  CONDITIONALS_BOUNDARY — `if d > 2`
//	cliparse.go:273:8  CONDITIONALS_NEGATION — `if d > 2`
//	cliparse.go:281:28 CONDITIONALS_NEGATION — `bestDist != -1` (the `!=`)
//	cliparse.go:281:31 ARITHMETIC_BASE   — `bestDist != -1` (the `-1`)
//	cliparse.go:281:31 INVERT_NEGATIVES  — `bestDist != -1` (the `-1`)
//
// SuggestName(input, candidates) returns (closestName, true) when some
// candidate is within Levenshtein distance 2, else ("", false).
//
// Distances used below are pure-insertion prefixes of "search"
// (s,e,a,r,c,h), so they are unambiguous and pinned by u13's
// editDistance table test:
//
//	"search" -> "search" : 0   (exact)
//	"searc"  -> "search" : 1   (insert h)
//	"sear"   -> "search" : 2   (insert c,h)   <- the 273 `> 2` boundary
//	"sea"    -> "search" : 3   (insert r,c,h) <- just past the boundary
//	"xxxxxxxx" vs any    : >2  (no common chars) -> no match
//
// Per-row mutant coverage:
//
//   - "no match": original returns ("", false). The 269 sentinel mutants
//     (init becomes +1/1 so it never equals -1), the 281:28 `==` mutant
//     (-1 == -1 is true), and the 281:31 `-1`->`1` mutants (-1 != 1 is true)
//     each flip the returned ok to true. Expecting false kills all five.
//   - "exact match" (dist 0): ok must be true; the 281:28 `==` mutant makes
//     0 == -1 false -> kills it in the match direction.
//   - "distance one" (dist 1): ok must be true; the 281:31 `-1`->`1` mutants
//     make 1 != 1 false -> kills them in the match direction.
//   - "distance two boundary": original accepts (2 > 2 is false). The 273
//     BOUNDARY `>=` (2 >= 2) and NEGATION `<=` (2 <= 2) mutants both drop it,
//     returning ("", false) instead of ("search", true).
//   - "distance three": original drops it (3 > 2 is true). The 273 NEGATION
//     `<=` mutant (3 <= 2 is false) accepts it, returning ("search", true)
//     instead of ("", false).
func Test_gk_subflux_u14_SuggestName_returnsClosestWithinDistanceTwo(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		candidates []string
		wantName   string
		wantOK     bool
	}{
		{
			name:       "no candidate within distance two",
			input:      "xxxxxxxx",
			candidates: []string{"search", "scan"},
			wantName:   "",
			wantOK:     false,
		},
		{
			name:       "exact match",
			input:      "search",
			candidates: []string{"search", "scan"},
			wantName:   "search",
			wantOK:     true,
		},
		{
			name:       "distance one match",
			input:      "searc",
			candidates: []string{"search"},
			wantName:   "search",
			wantOK:     true,
		},
		{
			name:       "distance two boundary accepted",
			input:      "sear",
			candidates: []string{"search"},
			wantName:   "search",
			wantOK:     true,
		},
		{
			name:       "distance three rejected",
			input:      "sea",
			candidates: []string{"search"},
			wantName:   "",
			wantOK:     false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotName, gotOK := SuggestName(c.input, c.candidates)
			if gotName != c.wantName || gotOK != c.wantOK {
				t.Errorf("SuggestName(%q, %v) = (%q, %t), want (%q, %t)",
					c.input, c.candidates, gotName, gotOK, c.wantName, c.wantOK)
			}
		})
	}
}
