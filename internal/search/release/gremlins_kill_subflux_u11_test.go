package release

import "testing"

// Tests in this file kill surviving gremlins mutation-testing mutants in
// pcre.go for unit subflux-u11. Each assertion's expected value depends on the
// exact operator at the targeted line, so applying the mutation changes the
// asserted observable.

// gk_subflux_u11_mustCompile compiles a PCRE pattern, failing on error.
func gk_subflux_u11_mustCompile(t *testing.T, pat string) *Pattern {
	t.Helper()
	p, err := CompilePCRE(pat)
	if err != nil {
		t.Fatalf("CompilePCRE(%q) error = %v, want nil", pat, err)
	}
	return p
}

// gk_subflux_u11_didPanic reports whether calling f panics.
func gk_subflux_u11_didPanic(f func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	f()
	return false
}

// --- CompilePCRE: if len(alts) == 1 (line 37) ---

// Kills 37:15 CONDITIONALS_NEGATION (== vs !=): a single (non-alternation)
// pattern must NOT populate branches; a multi-alt pattern must populate them.
func Test_gk_subflux_u11_CompilePCRE_alternationBranching(t *testing.T) {
	single := gk_subflux_u11_mustCompile(t, "foo")
	if len(single.branches) != 0 {
		t.Errorf("CompilePCRE(%q) len(branches) = %d, want 0", "foo", len(single.branches))
	}

	multi := gk_subflux_u11_mustCompile(t, "a|b")
	if len(multi.branches) != 2 {
		t.Errorf("CompilePCRE(%q) len(branches) = %d, want 2", "a|b", len(multi.branches))
	}
}

// --- MatchString: if len(p.branches) > 0 (line 85) ---

// Kills 85:21 CONDITIONALS_BOUNDARY (> vs >=): a single pattern (no branches)
// must still match via p.re; >= 0 falls into the empty branch loop -> false.
func Test_gk_subflux_u11_MatchString_singlePatternMatches(t *testing.T) {
	p := gk_subflux_u11_mustCompile(t, "foo")
	if got := p.MatchString("foobar"); got != true {
		t.Errorf("MatchString(%q) for %q = %v, want true", "foobar", "foo", got)
	}
}

// Kills 85:21 CONDITIONALS_NEGATION (> vs <=): a string matching only the
// second branch must match; <= 0 would test only branches[0].re.
func Test_gk_subflux_u11_MatchString_secondBranchMatches(t *testing.T) {
	p := gk_subflux_u11_mustCompile(t, "a|b")
	if got := p.MatchString("b"); got != true {
		t.Errorf("MatchString(%q) for %q = %v, want true", "b", "a|b", got)
	}
}

// --- MatchString: if len(p.assertions) == 0 (line 93) + checkAssertions (154) ---

// Kills 93:23 CONDITIONALS_NEGATION (== vs !=) and 154:17 CONDITIONALS_NEGATION
// (!= vs ==): a positive lookahead that fails must yield false, one that
// succeeds must yield true.
func Test_gk_subflux_u11_MatchString_positiveLookahead(t *testing.T) {
	p := gk_subflux_u11_mustCompile(t, "foo(?=bar)")
	if got := p.MatchString("foobar"); got != true {
		t.Errorf("MatchString(%q) for %q = %v, want true", "foobar", "foo(?=bar)", got)
	}
	if got := p.MatchString("foobaz"); got != false {
		t.Errorf("MatchString(%q) for %q = %v, want false", "foobaz", "foo(?=bar)", got)
	}
}

// --- MatchString: FindAllStringIndex(s, -1) (line 96) ---

// Kills 96:49 INVERT_NEGATIVES and ARITHMETIC_BASE (-1 -> 1): only the SECOND
// occurrence of x satisfies the lookahead; limiting to one match misses it.
func Test_gk_subflux_u11_MatchString_secondOccurrenceSatisfiesLookahead(t *testing.T) {
	p := gk_subflux_u11_mustCompile(t, "x(?=y)")
	if got := p.MatchString("xa xy"); got != true {
		t.Errorf("MatchString(%q) for %q = %v, want true", "xa xy", "x(?=y)", got)
	}
}

// --- FindStringSubmatch: if len(p.branches) > 0 (line 107) + extractSubmatch (137) ---

// Kills 107:21 CONDITIONALS_BOUNDARY (> vs >=) and 137:12 CONDITIONALS_BOUNDARY
// (start >= 0 vs start > 0): a single pattern matching at index 0 must return
// the full match text. >= 0 -> empty branch loop returns nil; start > 0 -> "".
func Test_gk_subflux_u11_FindStringSubmatch_singlePatternAtStart(t *testing.T) {
	p := gk_subflux_u11_mustCompile(t, "foo")
	m := p.FindStringSubmatch("foobar")
	if m == nil {
		t.Fatalf("FindStringSubmatch(%q) for %q = nil, want non-nil", "foobar", "foo")
	}
	if m[0] != "foo" {
		t.Errorf("FindStringSubmatch(%q) for %q [0] = %q, want %q", "foobar", "foo", m[0], "foo")
	}
}

// Kills 107:21 CONDITIONALS_NEGATION (> vs <=) and 110:39 CONDITIONALS_NEGATION
// (m != nil vs m == nil): a string matching only the second branch must return
// that branch's submatch.
func Test_gk_subflux_u11_FindStringSubmatch_secondBranch(t *testing.T) {
	p := gk_subflux_u11_mustCompile(t, "a|b")
	m := p.FindStringSubmatch("b")
	if m == nil {
		t.Fatalf("FindStringSubmatch(%q) for %q = nil, want non-nil", "b", "a|b")
	}
	if m[0] != "b" {
		t.Errorf("FindStringSubmatch(%q) for %q [0] = %q, want %q", "b", "a|b", m[0], "b")
	}
}

// --- FindStringSubmatch: FindAllStringSubmatchIndex(s, -1) (line 116) ---

// Kills 116:44 INVERT_NEGATIVES and ARITHMETIC_BASE (-1 -> 1): FindStringSubmatch
// returns the LAST match; limiting to one match returns the first.
func Test_gk_subflux_u11_FindStringSubmatch_returnsLastMatch(t *testing.T) {
	p := gk_subflux_u11_mustCompile(t, `(\d+)`)
	m := p.FindStringSubmatch("12 34")
	if m == nil {
		t.Fatalf("FindStringSubmatch(%q) for %q = nil, want non-nil", "12 34", `(\d+)`)
	}
	if m[0] != "34" {
		t.Errorf("FindStringSubmatch(%q) for %q [0] = %q, want %q", "12 34", `(\d+)`, m[0], "34")
	}
}

// --- FindStringSubmatch: if len(p.assertions) == 0 (line 120) ---

// Kills 120:23 CONDITIONALS_NEGATION (== vs !=): with a lookahead, the result
// must be the last match SATISFYING the assertion, not the last raw match.
// 'b' is followed by a digit (passes); 'z' is the last raw match (fails).
func Test_gk_subflux_u11_FindStringSubmatch_assertionFiltersLastMatch(t *testing.T) {
	p := gk_subflux_u11_mustCompile(t, `([a-z])(?=\d)`)
	m := p.FindStringSubmatch("a1 b2 cz")
	if m == nil {
		t.Fatalf("FindStringSubmatch(%q) for %q = nil, want non-nil", "a1 b2 cz", `([a-z])(?=\d)`)
	}
	if m[0] != "b" {
		t.Errorf("FindStringSubmatch(%q) for %q [0] = %q, want %q", "a1 b2 cz", `([a-z])(?=\d)`, m[0], "b")
	}
}

// --- SplitTopLevelAlternation: depth++ / depth-- (lines 174, 176) ---

// Kills 174:9 and 176:9 INCREMENT_DECREMENT: a '|' inside a group must NOT
// split; only the top-level '|' splits. Corrupting depth tracking mis-splits.
func Test_gk_subflux_u11_SplitTopLevelAlternation_respectsGroupDepth(t *testing.T) {
	got := SplitTopLevelAlternation("(a|b)|c")
	want := []string{"(a|b)", "c"}
	if len(got) != len(want) {
		t.Fatalf("SplitTopLevelAlternation(%q) = %#v (len %d), want %#v (len %d)",
			"(a|b)|c", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SplitTopLevelAlternation(%q)[%d] = %q, want %q", "(a|b)|c", i, got[i], want[i])
		}
	}
}

// --- extractAssertions: loop condition + bounds guard (lines 199, 200) ---

// Kills 199:8 CONDITIONALS_NEGATION (i < len vs i >= len): the scan loop must
// visit every byte and preserve a plain core.
func Test_gk_subflux_u11_extractAssertions_preservesCore(t *testing.T) {
	core, asserts := extractAssertions("foo")
	if core != "foo" {
		t.Errorf("extractAssertions(%q) core = %q, want %q", "foo", core, "foo")
	}
	if len(asserts) != 0 {
		t.Errorf("extractAssertions(%q) len(asserts) = %d, want 0", "foo", len(asserts))
	}
}

// Kills 200:7 ARITHMETIC_BASE (i+2 vs i-2): a pattern ending in '(' must not
// trigger an out-of-range read of pattern[i+1]. The guard i+2 < len protects
// it; i-2 < len would permit the read and panic.
func Test_gk_subflux_u11_extractAssertions_trailingOpenParenNoPanic(t *testing.T) {
	var core string
	if gk_subflux_u11_didPanic(func() { core, _ = extractAssertions("a(") }) {
		t.Fatalf("extractAssertions(%q) panicked; want no panic with core %q", "a(", "a(")
	}
	if core != "a(" {
		t.Errorf("extractAssertions(%q) core = %q, want %q", "a(", core, "a(")
	}
}

// --- parseLookaround: bounds (lines 239, 248) ---

// Kills 239:12 CONDITIONALS_BOUNDARY (len(s) < 4 vs <= 4): a minimal 4-char
// lookaround "(?=)" must be accepted (consumed=4, ok=true); <= 4 rejects it.
func Test_gk_subflux_u11_parseLookaround_minimalFourCharLookaround(t *testing.T) {
	_, consumed, ok := parseLookaround("(?=)")
	if !ok {
		t.Fatalf("parseLookaround(%q) ok = false, want true", "(?=)")
	}
	if consumed != 4 {
		t.Errorf("parseLookaround(%q) consumed = %d, want 4", "(?=)", consumed)
	}
}

// Kills 248:10 CONDITIONALS_BOUNDARY (end < len vs end <= len): an unbalanced
// lookaround (no closing paren) must return (.,0,false) without reading s[end]
// out of range; end <= len would read s[len] and panic.
func Test_gk_subflux_u11_parseLookaround_unbalancedNoPanic(t *testing.T) {
	var consumed int
	var ok bool
	if gk_subflux_u11_didPanic(func() { _, consumed, ok = parseLookaround("(?=ab") }) {
		t.Fatalf("parseLookaround(%q) panicked; want graceful (0,false)", "(?=ab")
	}
	if ok {
		t.Errorf("parseLookaround(%q) ok = true, want false", "(?=ab")
	}
	if consumed != 0 {
		t.Errorf("parseLookaround(%q) consumed = %d, want 0", "(?=ab", consumed)
	}
}

// --- skipCharClass: bounds + member detection (lines 271, 274) ---

// Kills 271:9 and 274:9 CONDITIONALS_BOUNDARY (pos < len vs pos <= len): a lone
// '[' must return 1 without reading s[1]; pos <= len reads out of range.
func Test_gk_subflux_u11_skipCharClass_loneOpenBracketNoPanic(t *testing.T) {
	var got int
	if gk_subflux_u11_didPanic(func() { got = skipCharClass("[", 0) }) {
		t.Fatalf("skipCharClass(%q, 0) panicked; want %d", "[", 1)
	}
	if got != 1 {
		t.Errorf("skipCharClass(%q, 0) = %d, want 1", "[", got)
	}
}

// Kills 271:9 CONDITIONALS_NEGATION (pos < len vs pos >= len): the leading '^'
// of a negated class must be skipped so a following ']' is a literal member.
// "[^]]" must skip to the final ']' (returns 3).
func Test_gk_subflux_u11_skipCharClass_negatedClassWithLiteralBracket(t *testing.T) {
	if got := skipCharClass("[^]]", 0); got != 3 {
		t.Errorf("skipCharClass(%q, 0) = %d, want 3", "[^]]", got)
	}
}

// Kills 274:9 CONDITIONALS_NEGATION (pos < len vs pos >= len): a ']' as the
// first member ("[]]") must be treated as literal; the close is the second ']'
// at index 2 (returns 2).
func Test_gk_subflux_u11_skipCharClass_leadingLiteralBracket(t *testing.T) {
	if got := skipCharClass("[]]", 0); got != 2 {
		t.Errorf("skipCharClass(%q, 0) = %d, want 2", "[]]", got)
	}
}

// Kills 274:28 CONDITIONALS_NEGATION (s[pos] == ']' vs != ']'): only an actual
// leading ']' is skipped as a literal member. "[]a]" must skip the literal ']'
// then scan to the closing ']' at index 3 (returns 3).
func Test_gk_subflux_u11_skipCharClass_leadingBracketThenMembers(t *testing.T) {
	if got := skipCharClass("[]a]", 0); got != 3 {
		t.Errorf("skipCharClass(%q, 0) = %d, want 3", "[]a]", got)
	}
}
