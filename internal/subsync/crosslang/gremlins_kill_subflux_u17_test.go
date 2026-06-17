package crosslang

import "testing"

// Unit subflux-u17: tests that kill surviving gremlins mutants in
// internal/subsync/crosslang. Tests only; behavior-named; expected values are
// hardcoded and depend on the exact operator at each mutated line.

// --- align.go ---

// align.go:397:18 CONDITIONALS_BOUNDARY: `candidate > dp[i]` in dpAlign's inner
// loop. Two predecessors of the final node give an EQUAL accumulated score; the
// inner loop scans j high->low. With `>` the first-seen (higher-index, RefIdx 2)
// predecessor wins the tie; `>=` would switch the parent to the later one
// (RefIdx 1), changing the reconstructed path's middle node.
func Test_gk_subflux_u17_dpAlignTieBreakKeepsFirst(t *testing.T) {
	t.Parallel()
	pairs := []CuePair{
		{IncIdx: 0, RefIdx: 0, Score: 5.0},
		{IncIdx: 1, RefIdx: 1, Score: 1.0}, // tie predecessor B
		{IncIdx: 1, RefIdx: 2, Score: 1.0}, // tie predecessor C (wins under `>`)
		{IncIdx: 2, RefIdx: 3, Score: 2.0}, // final node
	}
	got := dpAlign(pairs)
	if len(got) != 3 {
		t.Fatalf("dpAlign() len = %d, want 3 (path %+v)", len(got), got)
	}
	if got[1].IncIdx != 1 || got[1].RefIdx != 2 {
		t.Errorf("dpAlign() middle = (Inc %d, Ref %d), want (1, 2)",
			got[1].IncIdx, got[1].RefIdx)
	}
}

// align.go:407:12 CONDITIONALS_BOUNDARY: `dp[i] > dp[bestIdx]` selecting the
// best end node. Two independent equal-score pairs: with `>` the first (RefIdx 0)
// stays best; `>=` would pick the last (RefIdx 1).
func Test_gk_subflux_u17_dpAlignBestIdxKeepsFirst(t *testing.T) {
	t.Parallel()
	pairs := []CuePair{
		{IncIdx: 0, RefIdx: 0, Score: 3.0},
		{IncIdx: 0, RefIdx: 1, Score: 3.0}, // same IncIdx => no chain; equal dp
	}
	got := dpAlign(pairs)
	if len(got) != 1 {
		t.Fatalf("dpAlign() len = %d, want 1 (path %+v)", len(got), got)
	}
	if got[0].RefIdx != 0 {
		t.Errorf("dpAlign() best RefIdx = %d, want 0", got[0].RefIdx)
	}
}

// --- anchors.go ---

// anchors.go:54:21 CONDITIONALS_BOUNDARY: `r <= '9'` in extractNumbers' digit
// filter. '9' is the upper boundary; `<` would map it to -1 and drop the number.
func Test_gk_subflux_u17_extractNumbersKeepsNine(t *testing.T) {
	t.Parallel()
	got := extractNumbers("9")
	if len(got) != 1 || got[0] != "9" {
		t.Errorf("extractNumbers(%q) = %v, want [9]", "9", got)
	}
}

// anchors.go:74:28 CONDITIONALS_NEGATION: `i > 0` flipped (to `i <= 0`) in
// extractWords. A capitalized word right after a sentence-terminating word is a
// sentence start, so it is NOT a proper noun; the flip would make it one.
func Test_gk_subflux_u17_wordAfterSentenceIsNotProperNoun(t *testing.T) {
	t.Parallel()
	a := extractAnchors("Hello. World")
	if len(a.ProperNouns) != 0 {
		t.Errorf("extractAnchors(%q).ProperNouns = %v, want []", "Hello. World", a.ProperNouns)
	}
}

// anchors.go:84:16 CONDITIONALS_BOUNDARY: `len(clean) < 2` in classifyWord. An
// exactly-2-rune proper noun must still be classified; `<=` would skip it.
func Test_gk_subflux_u17_twoCharProperNounClassified(t *testing.T) {
	t.Parallel()
	a := extractAnchors("go Bo")
	if len(a.ProperNouns) != 1 || a.ProperNouns[0] != "Bo" {
		t.Errorf("extractAnchors(%q).ProperNouns = %v, want [Bo]", "go Bo", a.ProperNouns)
	}
}

// anchors.go:120:7 CONDITIONALS_NEGATION: `w == ""` flipped (to `w != ""`) in
// endsWithSentence. A non-empty word ending in '.' must report true.
func Test_gk_subflux_u17_endsWithSentence(t *testing.T) {
	t.Parallel()
	if !endsWithSentence("Hi.") {
		t.Errorf("endsWithSentence(%q) = false, want true", "Hi.")
	}
	if endsWithSentence("Hi") {
		t.Errorf("endsWithSentence(%q) = true, want false", "Hi")
	}
}

// anchors.go:135:8 CONDITIONALS_NEGATION: `r > 0x024F` flipped (to `r <= 0x024F`)
// in isLatinWord. Greek letters have codepoints > 0x024F and are non-Latin, so a
// Greek word must be rejected; the flip would skip the Latin check and accept it.
func Test_gk_subflux_u17_isLatinWordRejectsGreek(t *testing.T) {
	t.Parallel()
	if isLatinWord("\u03b1\u03b2\u03b3\u03b4") { // αβγδ
		t.Errorf("isLatinWord(%q) = true, want false", "\u03b1\u03b2\u03b3\u03b4")
	}
	if !isLatinWord("cafe") {
		t.Errorf("isLatinWord(%q) = false, want true", "cafe")
	}
}

// isCognate covers anchors.go:267:12 (B+N), 270:42 N, 274:25, 274:27, 275:14 (B+N).
//   - "test"/"test": maxLen==4 boundary, dist 0 < thr 1  -> cognate.
//     kills 267:12 BOUNDARY (maxLen<=4 => false), 267:12 NEGATION (maxLen>=4 => false),
//     270:42 NEGATION (ratio>=0.5 => false).
//   - "abc"/"abc": maxLen 3 (<4) -> too short, NOT cognate.
//     kills 267:12 NEGATION (maxLen>=4 would proceed to true).
//   - len-10 dist==thr(3): cognate. kills 275:14 BOUNDARY (dist<thr => false),
//     275:14 NEGATION (dist>thr => false), 274:25 *->/ (thr drops to 1 => false).
//   - len-10 dist 5 > thr 3: NOT cognate. kills 274:27 /->* (thr jumps to 300 => true).
func Test_gk_subflux_u17_isCognate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"equal_len4_cognate", "test", "test", true},
		{"equal_len3_too_short", "abc", "abc", false},
		{"dist_eq_threshold", "abcdefghij", "abcdefg000", true},
		{"dist_above_threshold", "abcdefghij", "abcde00000", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isCognate(tt.a, tt.b); got != tt.want {
				t.Errorf("isCognate(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// countCognates covers anchors.go:279:12 N, 279:27 N, 289:10 N.
//   - ([test],[test]) -> 1: kills 279:12 (len(a)!=0 would early-return 0) and
//     279:27 (len(b)!=0 would early-return 0).
//   - ([ab],[cd]) -> 0 (distinct, too short to be cognates): kills 289:10
//     (`wa == wb` flipped to `wa != wb` would count the distinct pair => 1).
func Test_gk_subflux_u17_countCognates(t *testing.T) {
	t.Parallel()
	if got := countCognates([]string{"test"}, []string{"test"}); got != 1 {
		t.Errorf("countCognates([test],[test]) = %d, want 1", got)
	}
	if got := countCognates([]string{"ab"}, []string{"cd"}); got != 0 {
		t.Errorf("countCognates([ab],[cd]) = %d, want 0", got)
	}
}

// countSharedFold covers anchors.go:323:27 (`freq[..]++`) and 329:13 (`count++`).
// One case-insensitive match must yield 1; flipping either `++` to `--` makes the
// result non-positive (0 via negative freq, or -1 via negative count).
func Test_gk_subflux_u17_countSharedFold(t *testing.T) {
	t.Parallel()
	if got := countSharedFold([]string{"hello"}, []string{"hello"}); got != 1 {
		t.Errorf("countSharedFold([hello],[hello]) = %d, want 1", got)
	}
}
