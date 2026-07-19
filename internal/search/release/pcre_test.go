package release

import (
	"errors"
	"slices"
	"testing"
)

// Pinning-suite provenance: this file was updated for the in-place PCRE
// layer repair (spec subflux-release-parse-fidelity; study appendix
// .kiro/specs/subflux-release-parse-fidelity/study.md). Tests exercising
// the retired linear extraction internals (extractAssertions,
// parseLookaround, SplitTopLevelAlternation) were replaced by equivalents
// on the marker-based compiler; behavior pins classified "contract" in the
// study are unchanged.

func mustCompilePCRE(t *testing.T, pat string) *Pattern {
	t.Helper()
	p, err := CompilePCRE(pat)
	if err != nil {
		t.Fatalf("CompilePCRE(%q) error = %v, want nil", pat, err)
	}
	return p
}

// --- structure ---

func TestCompilePCRE_alternation_branches(t *testing.T) {
	t.Parallel()
	// Study ref: top-level alternation still compiles per branch (positional
	// merge rule R3.3); a single-branch pattern now also lives in branches[0].
	single := mustCompilePCRE(t, "foo")
	if len(single.branches) != 1 {
		t.Errorf("CompilePCRE(%q) branches = %d, want 1", "foo", len(single.branches))
	}
	multi := mustCompilePCRE(t, "a|b")
	if len(multi.branches) != 2 {
		t.Errorf("CompilePCRE(%q) branches = %d, want 2", "a|b", len(multi.branches))
	}
}

func TestPattern_String_returns_source(t *testing.T) {
	t.Parallel()
	const pat = `a(?=b)|c`
	if got := mustCompilePCRE(t, pat).String(); got != pat {
		t.Errorf("String() = %q, want %q", got, pat)
	}
}

// --- MatchString (contract pins, unchanged) ---

func TestPattern_MatchString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		input   string
		want    bool
	}{
		{"single pattern matches", "foo", "foobar", true},
		{"second alternation branch matches", "a|b", "b", true},
		{"positive lookahead satisfied", "foo(?=bar)", "foobar", true},
		{"positive lookahead unsatisfied", "foo(?=bar)", "foobaz", false},
		{"later occurrence satisfies lookahead", "x(?=y)", "xa xy", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := mustCompilePCRE(t, tc.pattern)
			if got := p.MatchString(tc.input); got != tc.want {
				t.Errorf("MatchString(%q) for %q = %v, want %v", tc.input, tc.pattern, got, tc.want)
			}
		})
	}
}

// --- FindStringSubmatch: last-match contract (R3.5) + source numbering (rule 3) ---

func TestPattern_FindStringSubmatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		input   string
		want    string // expected full match m[0]
	}{
		{"single pattern at start", "foo", "foobar", "foo"},
		{"second alternation branch", "a|b", "b", "b"},
		{"returns last match", `(\d+)`, "12 34", "34"},
		{"assertion filters to last satisfying match", `([a-z])(?=\d)`, "a1 b2 cz", "b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := mustCompilePCRE(t, tc.pattern)
			m := p.FindStringSubmatch(tc.input)
			if m == nil {
				t.Fatalf("FindStringSubmatch(%q) for %q = nil, want non-nil", tc.input, tc.pattern)
			}
			if m[0] != tc.want {
				t.Errorf("FindStringSubmatch(%q) for %q [0] = %q, want %q", tc.input, tc.pattern, m[0], tc.want)
			}
		})
	}
}

// TestFindStringSubmatch_source_numbering pins normative marker rule 3:
// exposed submatch numbering is the SOURCE pattern's, across top-level
// branches (each branch's groups occupy their global source slots) and
// with assertion-inner capture groups holding (never-participating) slots.
func TestFindStringSubmatch_source_numbering(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		input   string
		want    []string
	}{
		{
			name:    "branch groups occupy global slots (branch 1)",
			pattern: `(alpha)|(beta)`, input: "only alpha here",
			want: []string{"alpha", "alpha", ""},
		},
		{
			name:    "branch groups occupy global slots (branch 2)",
			pattern: `(alpha)|(beta)`, input: "only beta here",
			want: []string{"beta", "", "beta"},
		},
		{
			name:    "assertion-inner group holds a numbering slot",
			pattern: `\b(HDR10(?=[+]|P(lus)?))`, input: "Movie.HDR10Plus.x265",
			want: []string{"HDR10", "HDR10", ""},
		},
		{
			name:    "release-group bracket branch fills m[3]",
			pattern: sonarrReleaseGroupRegex, input: "Movie.2024.1080p.[GRP]",
			want: []string{".[GRP]", "", "", "GRP"},
		},
		{
			name:    "release-group dash branch fills m[1]",
			pattern: sonarrReleaseGroupRegex, input: "Some.Movie.2020.1080p.BluRay.x264-RARBG",
			want: []string{"-RARBG", "RARBG", "", ""},
		},
		{
			name:    "release-group part2 fills m[2]",
			pattern: sonarrReleaseGroupRegex, input: "Movie.2020.1080p.x264-Two-Part",
			want: []string{"-Two-Part", "Two-Part", "-Part", ""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := mustCompilePCRE(t, tc.pattern)
			got := p.FindStringSubmatch(tc.input)
			if !slices.Equal(got, tc.want) {
				t.Errorf("FindStringSubmatch(%q) for %q = %#v, want %#v", tc.input, tc.pattern, got, tc.want)
			}
		})
	}
}

// --- Normative marker rule 1: -1 marker (non-participating branch) skipped ---

// TestMarkerRule1_nonparticipating_branch_assertion_skipped is the class (h)
// regression witness (study class (h)): the (?!$) guarding only the BD
// nested branch must not veto sibling branches. Before the repair the layer
// extracted the assertion to the outer match boundary, so
// "The.Matrix.1999.1080p.BluRay" (ending at the match end) failed.
func TestMarkerRule1_nonparticipating_branch_assertion_skipped(t *testing.T) {
	t.Parallel()
	p := mustCompilePCRE(t, `\b(?:BluRay|Blu-Ray|HD-?DVD|BDMux|BD(?!$))\b`)
	tests := []struct {
		input string
		want  bool
	}{
		{"The.Matrix.1999.1080p.BluRay", true}, // live witness: BluRay at end of text
		{"Movie.2020.1080p.Blu-Ray", true},
		{"Movie.2020.BDMux", true},
		{"Movie.2020.BD.1080p", true}, // BD branch, assertion holds mid-text
		{"Movie.2020.BD", false},      // BD branch, (?!$) fails at end
		{"Movie.2020.HD-DVD", true},   // sibling branch at end of text
		{"Movie.BDRip.x264", false},   // BD followed by word char: \b fails
		{"Plain.Name.1080p", false},   // no branch matches
		{"BD", false},                 // whole input is BD at end
		{"BD ", true},                 // trailing space: not end-of-text
		{"bluray", true},              // case-insensitive
	}
	for _, tc := range tests {
		if got := p.MatchString(tc.input); got != tc.want {
			t.Errorf("MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestMarkerRule1_find_submatch_skips_foreign_assertions verifies rule 1 on
// the capture path as well: a candidate from one nested branch is not
// checked against another branch's assertion.
func TestMarkerRule1_find_submatch_skips_foreign_assertions(t *testing.T) {
	t.Parallel()
	p := mustCompilePCRE(t, `(?:cat(?=fish)|dog)`)
	m := p.FindStringSubmatch("a dog at end")
	if m == nil || m[0] != "dog" {
		t.Fatalf("FindStringSubmatch = %v, want dog match (catfish lookahead must be skipped)", m)
	}
}

// --- Normative marker rule 2: assertion inside quantified group = typed error ---

func TestMarkerRule2_assertion_in_quantified_group_rejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
	}{
		{"lookahead in starred group", `(?:a(?=b))*`},
		{"lookahead in plus capturing group", `(x(?!y))+c`},
		{"lookbehind in optional group", `(?:(?<=a)b)?`},
		{"nested two levels under repeat", `(?:x(?:y(?=z))){2}`},
		{"lazy quantifier over group with assertion", `(?:a(?<!b))+?`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := CompilePCRE(tc.pattern)
			var ce *CompileError
			if !errors.As(err, &ce) {
				t.Fatalf("CompilePCRE(%q) error = %v, want *CompileError", tc.pattern, err)
			}
			if ce.Offset < 0 {
				t.Errorf("CompileError.Offset = %d, want >= 0", ce.Offset)
			}
		})
	}
}

// --- Typed compile errors: table-driven test per rejected shape (R3.6) ---

func TestCompilePCRE_rejected_shapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		pattern       string
		wantConstruct string
	}{
		{"numeric backreference", `(a)\1`, `\1`},
		{"named backreference k", `(?<g>a)\k<g>`, `\k`},
		{"relative backreference g", `(a)\g1`, `\g`},
		{"python named backreference", `(?P=name)`, "(?P="},
		{"atomic group", `(?>abc)`, "(?>"},
		{"conditional group", `(?(1)a|b)`, "(?("},
		{"inline comment", `(?#comment)a`, "(?#"},
		{"quoted group name", `(?'name'a)`, "(?'"},
		{"possessive quantifier", `a*+`, "*+"},
		{"quantified lookahead", `(?=a)+`, "+"},
		{"quantified lookbehind", `(?<!a)?`, "?"},
		{"nested lookaround", `(?<=a(?=b))c`, "(?<="},
		{"unterminated lookaround", `(?!unclosed`, "(?!"},
		{"unterminated group", `(unclosed`, "("},
		{"unmatched closing paren", `ab)`, ")"},
		{"trailing backslash", "ab\\", `\`},
		{"unterminated group name", `(?<name`, "(?<"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := CompilePCRE(tc.pattern)
			var ce *CompileError
			if !errors.As(err, &ce) {
				t.Fatalf("CompilePCRE(%q) error = %v (%T), want *CompileError", tc.pattern, err, err)
			}
			if ce.Construct != tc.wantConstruct {
				t.Errorf("CompilePCRE(%q) construct = %q, want %q (reason: %s)",
					tc.pattern, ce.Construct, tc.wantConstruct, ce.Reason)
			}
			if ce.Reason == "" {
				t.Errorf("CompilePCRE(%q) reason empty, want explanation", tc.pattern)
			}
		})
	}
}

// TestCompilePCRE_re2_core_rejection_is_typed pins that errors surfaced by
// the underlying RE2 compiler (not the layer's own parser) also arrive as
// typed *CompileError values.
func TestCompilePCRE_re2_core_rejection_is_typed(t *testing.T) {
	t.Parallel()
	for _, pat := range []string{`a{2,1}`, `foo(?=\p)`, `[z-a]`} {
		_, err := CompilePCRE(pat)
		var ce *CompileError
		if !errors.As(err, &ce) {
			t.Errorf("CompilePCRE(%q) error = %v (%T), want *CompileError", pat, err, err)
		}
	}
}

// --- Bounded shrink-and-recheck retry (R3.2) ---

func TestRetryShrink_assertion_after_quantified_element(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		input   string
		want    bool
	}{
		// (\d+)(?!x): greedy end fails ("99" followed by x) but width 1
		// passes ("9" followed by "9"). .NET matches "9"; the layer accepts
		// the candidate (greedy captures kept, documented class (j)).
		{"lookahead passes at shorter width", `(\d+)(?!x)`, "99x", true},
		// No width can satisfy: every shrink still sees a digit-then-x tail.
		{"no satisfying width exists", `(\d+)(?!.)`, "12ab", false},
		// Optional group adjacency: (?<!-ES) fails at greedy end (part2
		// "-ES") but passes after shrinking past part2.
		{"lookbehind passes after dropping optional tail", `-([a-z]+(-[a-z]+)?)(?<!-ES)`, "x264-GROUP-ES", true},
		// Floor respected: [ab]+ has minimum width 1, so the assertion is
		// never probed at width 0 (where it would hold).
		{"floor respects plus minimum", `x([ab]+)(?<!.[ab])`, "xab", false},
		// Assertion not quantifier-adjacent: no retry happens.
		{"no retry without adjacent quantifier", `(ab)(?!c)`, "abc", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := mustCompilePCRE(t, tc.pattern)
			if got := p.MatchString(tc.input); got != tc.want {
				t.Errorf("MatchString(%q) for %q = %v, want %v", tc.input, tc.pattern, got, tc.want)
			}
		})
	}
}

// TestRetryShrink_flagship_release_group covers the shape that motivated
// the retry (design "backtracking-gap resolution"): the release-group
// pattern's lookbehind failing at the greedy end must not drop the
// candidate when a shorter quantifier expansion satisfies it, and the
// accepted shrink clamps the reported captures to the offset .NET's
// backtracking would produce (oracle-verified: m[1] = "GROUP").
func TestRetryShrink_flagship_release_group(t *testing.T) {
	t.Parallel()
	p := mustCompilePCRE(t, sonarrReleaseGroupRegex)
	// Greedy: m[1] = "GROUP-ES" -> lookbehind (?<!...-ES...) fails at the
	// greedy end. The retry probes shorter widths: "GROUP-E" satisfies the
	// lookbehind but the same-level continuation (?:\b|[-._ ]|$) fails at
	// 'S'; "GROUP-" is not a legal chain width; "GROUP" satisfies
	// assertion + legality + continuation -> accepted, part2 dropped.
	m := p.FindStringSubmatch("Movie.2024.1080p.WEB-DL.x264-GROUP-ES")
	if m == nil {
		t.Fatal("FindStringSubmatch = nil, want surviving candidate via shrink-retry")
	}
	if m[1] != "GROUP" {
		t.Errorf("m[1] = %q, want %q (clamped to the accepted shrink offset)", m[1], "GROUP")
	}
	if m[2] != "" {
		t.Errorf("m[2] = %q, want empty (part2 dropped by the accepted shrink)", m[2])
	}
}

// --- Positional merge scan (R3.3) ---

func TestPositionalMerge_scan_order(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		input   string
		want    string
	}{
		// One scan: selections a(0), a(2); last is positionally last.
		{"last selection of single scan", `(a)|(b)`, "a a", "a"},
		// Branch-2 candidate at 0 is selected (earliest start), advancing
		// the cursor past branch-1's later overlapping candidate.
		{"earliest start wins across branches", `bc|ab`, "abc", "ab"},
		// Tie at the same start: source branch order decides.
		{"tie broken by branch order", `ab|ax|a`, "ax", "ax"},
		// Old behavior returned the last BRANCH that matched anywhere
		// ("beta" from branch 2 even though "alpha" is positionally last).
		// Study class (b): .NET's scan yields alpha(0), beta(6), alpha(11);
		// last is alpha.
		{"positionally last not branch-last", `(alpha)|(beta)`, "alpha beta alpha", "alpha"},
		// Non-overlap: after selecting [0,2), branch 2's [1,3) is skipped.
		{"selected span suppresses overlaps", `ab|bc`, "abc", "ab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := mustCompilePCRE(t, tc.pattern)
			m := p.FindStringSubmatch(tc.input)
			if m == nil {
				t.Fatalf("FindStringSubmatch(%q) for %q = nil, want %q", tc.input, tc.pattern, tc.want)
			}
			if m[0] != tc.want {
				t.Errorf("FindStringSubmatch(%q) for %q [0] = %q, want %q", tc.input, tc.pattern, m[0], tc.want)
			}
		})
	}
}

func TestPositionalMerge_zero_width_bump_along(t *testing.T) {
	t.Parallel()
	// An assertion-only branch yields zero-width candidates at every
	// position; the scan must advance and terminate, and the last selection
	// still reflects scan order.
	p := mustCompilePCRE(t, `(?=z)|ab`)
	m := p.FindStringSubmatch("abz")
	if m == nil {
		t.Fatal("FindStringSubmatch = nil, want non-nil")
	}
	if m[0] != "" {
		t.Errorf("m[0] = %q, want empty (zero-width selection at z is positionally last)", m[0])
	}
}

// --- assertion-position fixes (class (a)) ---

func TestAssertions_evaluated_at_marker_positions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		input   string
		want    bool
	}{
		// Mid-pattern lookahead: consuming syntax follows the assertion.
		{"mid-pattern lookahead holds", `a(?=bc)bcd`, "abcd", true},
		{"mid-pattern lookahead fails", `a(?=bx)bcd`, "abcd", false},
		// Mid-pattern lookbehind after consuming prefix.
		{"mid-pattern lookbehind holds", `ab(?<=ab)cd`, "abcd", true},
		{"mid-pattern lookbehind fails", `ab(?<=xb)cd`, "abcd", false},
		// Leading lookbehind at match start (old boundary position, still correct).
		{"leading lookbehind", `(?<=foo)bar`, "foobar", true},
		{"leading lookbehind fails", `(?<=foo)bar`, "bazbar", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := mustCompilePCRE(t, tc.pattern)
			if got := p.MatchString(tc.input); got != tc.want {
				t.Errorf("MatchString(%q) for %q = %v, want %v", tc.input, tc.pattern, got, tc.want)
			}
		})
	}
}

// --- parser internals ---

func TestSkipCharClass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"lone open bracket", "[", 1},
		{"negated class with literal bracket", "[^]]", 3},
		{"leading literal bracket", "[]]", 2},
		{"leading bracket then members", "[]a]", 3},
		{"unterminated class runs to end", "[ab", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := skipCharClass(tc.input, 0); got != tc.want {
				t.Errorf("skipCharClass(%q, 0) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseRepeat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input        string
		wantMin      int
		wantConsumed int
		wantOK       bool
	}{
		{"{2}", 2, 3, true},
		{"{2,}", 2, 4, true},
		{"{2,5}", 2, 5, true},
		{"{0,3}", 0, 5, true},
		{"{}", 0, 0, false},
		{"{a}", 0, 0, false},
		{"{2", 0, 0, false},
	}
	for _, tc := range tests {
		gotMin, gotConsumed, gotOK := parseRepeat(tc.input)
		if gotMin != tc.wantMin || gotConsumed != tc.wantConsumed || gotOK != tc.wantOK {
			t.Errorf("parseRepeat(%q) = (%d, %d, %v), want (%d, %d, %v)",
				tc.input, gotMin, gotConsumed, gotOK, tc.wantMin, tc.wantConsumed, tc.wantOK)
		}
	}
}

func TestCountCapturesIn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{"[+]|P(lus)?", 1},
		{"no groups", 0},
		{`(a)(b)`, 2},
		{`(?:x)`, 0},
		{`\(escaped\)`, 0},
		{`[(]class[)]`, 0},
		{`(?<name>x)`, 1},
	}
	for _, tc := range tests {
		if got := countCapturesIn(tc.input); got != tc.want {
			t.Errorf("countCapturesIn(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestClampName(t *testing.T) {
	t.Parallel()
	short := "Movie.2024.1080p.BluRay.x264-GRP"
	if got := ClampName(short); got != short {
		t.Errorf("ClampName(short) = %q, want unchanged", got)
	}
	long := make([]byte, MaxNameLen+100)
	for i := range long {
		long[i] = 'a'
	}
	if got := ClampName(string(long)); len(got) != MaxNameLen {
		t.Errorf("ClampName(long) len = %d, want %d", len(got), MaxNameLen)
	}
}

// --- edge cases (contract pins, unchanged behavior) ---

func TestCompilePCRE_empty_pattern(t *testing.T) {
	t.Parallel()
	p := mustCompilePCRE(t, "")
	if !p.MatchString("anything") {
		t.Error("MatchString(\"anything\") = false, want true")
	}
	if !p.MatchString("") {
		t.Error("MatchString(\"\") = false, want true")
	}
}

func TestCompilePCRE_assertion_only_pattern(t *testing.T) {
	t.Parallel()
	p := mustCompilePCRE(t, "(?=foo)")
	if !p.MatchString("foobar") {
		t.Error("assertion-only pattern should match when assertion is satisfied")
	}
	if p.MatchString("barbar") {
		t.Error("assertion-only pattern should not match when assertion fails")
	}
}

func TestCompilePCRE_escaped_parens_in_lookaround(t *testing.T) {
	t.Parallel()
	p := mustCompilePCRE(t, `bar(?=\(foo\))`)
	if !p.MatchString("bar(foo)") {
		t.Error("bar(foo) should match")
	}
	if p.MatchString("barfoo") {
		t.Error("barfoo should not match")
	}
}

// TestCompilePCRE_inline_flags pins that RE2 inline flag groups pass
// through the compiler unchanged ((?-i:WEB) appears in the WEB-DL source
// pattern).
func TestCompilePCRE_inline_flags(t *testing.T) {
	t.Parallel()
	p := mustCompilePCRE(t, `[. ](?-i:WEB)$`)
	if !p.MatchString("Movie.2023.WEB") {
		t.Error("uppercase WEB at end should match")
	}
	if p.MatchString("Movie.2023.web") {
		t.Error("lowercase web must not match inside (?-i:...)")
	}
}

// TestCompilePCRE_all_shipped_formats is the inventory compile gate: every
// pattern the layer ships (the phase-1 inventory set) compiles, and none
// triggers marker rule 2 (the design records that no current pattern has
// an assertion inside a quantified group).
func TestCompilePCRE_all_shipped_formats(t *testing.T) {
	t.Parallel()
	tables := [][]Format{SonarrSources, TrashVideoCodecs, TrashHDRFormats, TrashStreamingServices}
	count := 0
	for _, tbl := range tables {
		for _, f := range tbl {
			if _, err := CompilePCRE(f.Regex); err != nil {
				t.Errorf("CompilePCRE(%s = %q) error: %v", f.Name, f.Regex, err)
			}
			count++
		}
	}
	if _, err := CompilePCRE(sonarrReleaseGroupRegex); err != nil {
		t.Errorf("CompilePCRE(sonarrReleaseGroupRegex) error: %v", err)
	}
	count++
	if count != 92 {
		t.Errorf("shipped pattern count = %d, want 92 (update the study inventory when the set changes)", count)
	}
}
