package search

import (
	"regexp"
	"testing"

	"github.com/cplieger/subflux/internal/search/release"
	"pgregory.net/rapid"
)

// compilePCRE delegates to release.CompilePCRE for test compatibility.
var compilePCRE = release.CompilePCRE

func TestCompilePCRE_lookahead_positive(t *testing.T) {
	t.Parallel()
	// HDR10+ pattern: should match HDR10+ but not HDR10 alone.
	p, err := compilePCRE(`\b(HDR10(?=[+]|P(lus)?))`)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		input string
		want  bool
	}{
		{"Movie.HDR10+.x265", true},
		{"Movie.HDR10Plus.x265", true},
		{"Movie.HDR10P.x265", true},
		{"Movie.HDR10.x265", false},
		{"Movie.HDR10.BluRay", false},
	}
	for _, tt := range tests {
		if got := p.MatchString(tt.input); got != tt.want {
			t.Errorf("HDR10+ pattern on %q: got %v, want %v",
				tt.input, got, tt.want)
		}
	}
}

func TestCompilePCRE_lookahead_negative(t *testing.T) {
	t.Parallel()
	// HDR10 pattern: should match HDR10 but not HDR10+.
	p, err := compilePCRE(`\b(HDR10(?![+]|P(lus)?))`)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		input string
		want  bool
	}{
		{"Movie.HDR10.x265", true},
		{"Movie.HDR10.BluRay", true},
		{"Movie.HDR10+.x265", false},
		{"Movie.HDR10Plus.x265", false},
		{"Movie.HDR10P.x265", false},
	}
	for _, tt := range tests {
		if got := p.MatchString(tt.input); got != tt.want {
			t.Errorf("HDR10 pattern on %q: got %v, want %v",
				tt.input, got, tt.want)
		}
	}
}

func TestCompilePCRE_lookbehind_negative(t *testing.T) {
	t.Parallel()
	// DD/AC3 pattern: ac3 should not match inside e-ac3.
	p, err := compilePCRE(`\bDD[^a-z+]|(?<!e-)\b(ac-?3)\b`)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		input string
		want  bool
	}{
		{"Movie.AC3.x264", true},
		{"Movie.DD5.1.x264", true},
		{"Movie.E-AC3.x264", false},
		{"Movie.e-ac3.x264", false},
		{"Movie.EAC3.x264", false}, // No hyphen, \b before ac3 won't match inside EAC3.
	}
	for _, tt := range tests {
		if got := p.MatchString(tt.input); got != tt.want {
			t.Errorf("DD/AC3 pattern on %q: got %v, want %v",
				tt.input, got, tt.want)
		}
	}
}

func TestCompilePCRE_no_assertions(t *testing.T) {
	t.Parallel()
	// Plain RE2 pattern should work unchanged.
	p, err := compilePCRE(`\bFLAC(\b|\d)`)
	if err != nil {
		t.Fatal(err)
	}
	if !p.MatchString("Movie.FLAC.x264") {
		t.Error("FLAC should match")
	}
	if p.MatchString("Movie.x264") {
		t.Error("should not match without FLAC")
	}
}

func TestCompilePCRE_dtsx_not_x264(t *testing.T) {
	t.Parallel()
	// DTS:X should not match dts in dts-x264.
	p, err := compilePCRE(`\b(dts[-_.: ]?x7?)\b(?![-_. ]?(26[456]))`)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		input string
		want  bool
	}{
		{"Movie.DTS-X.BluRay", true},
		{"Movie.DTS.X.BluRay", true},
		{"Movie.DTS-X7.BluRay", true},
		{"Movie.DTS-x264", false},
		{"Movie.DTS.x265", false},
	}
	for _, tt := range tests {
		if got := p.MatchString(tt.input); got != tt.want {
			t.Errorf("DTS:X pattern on %q: got %v, want %v",
				tt.input, got, tt.want)
		}
	}
}

func TestCompilePCRE_invalid_core_regex_returns_error(t *testing.T) {
	t.Parallel()
	// An unbalanced group in the core (after assertion extraction) should fail.
	_, err := compilePCRE(`(unclosed`)
	if err == nil {
		t.Error("compilePCRE(unclosed group) expected error, got nil")
	}
}

func TestCompilePCRE_invalid_assertion_regex_returns_error(t *testing.T) {
	t.Parallel()
	// A lookahead containing an invalid regex should fail during assertion compilation.
	// Use \p (incomplete Unicode property) which is valid enough for extraction
	// but invalid as an RE2 regex.
	_, err := compilePCRE(`foo(?=\p)`)
	if err == nil {
		t.Error("compilePCRE(invalid assertion regex) expected error, got nil")
	}
}

func TestCompilePCRE_unterminated_lookaround_ignored(t *testing.T) {
	t.Parallel()
	// An unterminated lookaround (?!... without a closing ) is rejected by
	// the marker-based compiler with a typed error (study: rejected-shape
	// table; previously it fell through to the core and failed there).
	_, err := compilePCRE(`(?!unclosed`)
	if err == nil {
		t.Error("compilePCRE(unterminated lookaround) expected error, got nil")
	}
}

func TestCompilePCRE_lookbehind_positive(t *testing.T) {
	t.Parallel()
	// Positive lookbehind: match "bar" only when preceded by "foo".
	p, err := compilePCRE(`(?<=foo)bar`)
	if err != nil {
		t.Fatal(err)
	}
	if !p.MatchString("foobar") {
		t.Error("positive lookbehind: foobar should match")
	}
	if p.MatchString("bazbar") {
		t.Error("positive lookbehind: bazbar should not match")
	}
}

func TestCompilePCRE_alternation_branch_error(t *testing.T) {
	t.Parallel()
	// When one branch of a top-level alternation is invalid, compilePCRE
	// should return an error (exercises the error path in the alternation loop).
	_, err := compilePCRE(`valid|(?P<bad)`)
	if err == nil {
		t.Error("compilePCRE(alternation with invalid branch) expected error, got nil")
	}
}

func TestCompilePCRE_lookaround_with_negated_char_class(t *testing.T) {
	t.Parallel()
	// Exercises the skipCharClass [^...] (negated) path inside a lookaround.
	p, err := compilePCRE(`foo(?![^a-z])`)
	if err != nil {
		t.Fatalf("compilePCRE(negated char class in lookahead): %v", err)
	}
	// "foobar" → lookahead checks char after "foo" match; 'b' is in [a-z],
	// so [^a-z] does NOT match → negative lookahead succeeds → overall match.
	if !p.MatchString("foobar") {
		t.Error("foobar should match (negative lookahead: next char IS a-z)")
	}
	// "foo123" → '1' is NOT in [a-z], so [^a-z] matches → negative
	// lookahead fails → no match.
	if p.MatchString("foo123") {
		t.Error("foo123 should not match (negative lookahead: next char is NOT a-z)")
	}
}

func TestCompilePCRE_negated_char_class_in_alternation(t *testing.T) {
	t.Parallel()
	// Exercises the skipCharClass [^...] path in splitTopLevelAlternation.
	// The negated char class must be at the top level (not inside a group)
	// so splitTopLevelAlternation's skipCharClass call processes it.
	p, err := compilePCRE(`[^a-z]+|foo`)
	if err != nil {
		t.Fatalf("compilePCRE(negated char class in alternation): %v", err)
	}
	if !p.MatchString("123") {
		t.Error("[^a-z]+ should match digits")
	}
	if !p.MatchString("foo") {
		t.Error("foo branch should match")
	}
}

func TestCompilePCRE_char_class_literal_bracket(t *testing.T) {
	t.Parallel()
	// Exercises the skipCharClass path where ] is the first character
	// in a character class (treated as literal, not closing bracket).
	// Pattern: []]|foo — char class containing literal ], then alternation.
	p, err := compilePCRE(`[]]|foo`)
	if err != nil {
		t.Fatalf("compilePCRE(literal bracket in char class): %v", err)
	}
	if !p.MatchString("]") {
		t.Error("[]] should match literal ]")
	}
	if !p.MatchString("foo") {
		t.Error("foo branch should match")
	}
	if p.MatchString("bar") {
		t.Error("bar should not match")
	}
}

func TestCompilePCRE_truncated_lookaround_falls_to_core(t *testing.T) {
	t.Parallel()
	// A pattern ending with "(?=" (3 chars, < 4 minimum for parseLookaround)
	// is not parsed as a lookaround; it falls through to the core regex.
	// "(?=" is invalid RE2, so compilation should fail.
	_, err := compilePCRE(`a(?=`)
	if err == nil {
		t.Error("compilePCRE(truncated lookaround) expected error, got nil")
	}
}

func TestCompilePCRE_anime_bd_dual_assertions(t *testing.T) {
	t.Parallel()
	// Sonarr AnimeBlurayRegex: bd(?:720|1080|2160)|(?<=[-_. (\[])bd(?=[-_. )\]])
	// The second alternative has both lookbehind and lookahead.
	p, err := compilePCRE(`bd(?:720|1080|2160)|(?<=[-_. (\[])bd(?=[-_. )\]])`)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		input string
		want  bool
	}{
		{"[SubGroup] Title bd1080", true},
		{"[SubGroup] Title [bd]", true},
		{"[SubGroup] Title (bd)", true},
		{"[SubGroup] Title .bd.", true},
		{"[SubGroup] Title bd720p", true},
		{"[SubGroup] Title abduct", false}, // "bd" inside word
		{"Title.BDRip.x264", false},        // "BD" followed by "Rip", not separator
	}
	for _, tt := range tests {
		if got := p.MatchString(tt.input); got != tt.want {
			t.Errorf("AnimeBD on %q: got %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- FindStringSubmatch ---

func TestFindStringSubmatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		input   string
		wantVal string
		wantIdx int
		wantNil bool
	}{
		{
			name: "no_match_returns_nil", pattern: `\bFOO\b`,
			input: "bar baz", wantNil: true,
		},
		{
			name: "simple_match", pattern: `\b(FOO)\b`,
			input: "hello FOO world", wantIdx: 1, wantVal: "FOO",
		},
		{
			name: "last_match_semantics", pattern: `\b(\d+)\b`,
			input: "abc 123 def 456 ghi", wantIdx: 1, wantVal: "456",
		},
		{
			name: "with_assertion_filters_matches", pattern: `(\d+)(?!x)`,
			input: "99x 42y", wantIdx: 1, wantVal: "42",
		},
		{
			name: "alternation_returns_last_branch_match", pattern: `(alpha)|(beta)`,
			input: "alpha and beta", wantIdx: 0, wantVal: "beta",
		},
		{
			name: "alternation_first_branch_only", pattern: `(alpha)|(beta)`,
			input: "only alpha here", wantIdx: 0, wantVal: "alpha",
		},
		{
			name: "alternation_no_match", pattern: `(alpha)|(beta)`,
			input: "gamma delta", wantNil: true,
		},
		{
			name: "all_matches_fail_assertions", pattern: `(\d+)(?!.)`,
			input: "12ab", wantNil: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := compilePCRE(tc.pattern)
			if err != nil {
				t.Fatal(err)
			}
			got := p.FindStringSubmatch(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("FindStringSubmatch(%q) = %v, want nil", tc.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("FindStringSubmatch(%q) = nil, want non-nil", tc.input)
			}
			if tc.wantIdx >= len(got) {
				t.Fatalf("FindStringSubmatch(%q) has %d elements, want at least %d", tc.input, len(got), tc.wantIdx+1)
			}
			if got[tc.wantIdx] != tc.wantVal {
				t.Errorf("FindStringSubmatch(%q)[%d] = %q, want %q", tc.input, tc.wantIdx, got[tc.wantIdx], tc.wantVal)
			}
		})
	}
}

// --- PBT: RE2-compatible pattern equivalence ---

func TestCompilePCRE_re2_equivalence(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Generate simple RE2-safe patterns (word chars, dots, digits).
		pat := rapid.StringMatching(`[a-z][a-z0-9.]{0,10}`).Draw(t, "pattern")
		input := rapid.StringMatching(`[a-zA-Z0-9. ]{0,30}`).Draw(t, "input")

		p, err := compilePCRE(pat)
		if err != nil {
			return // Skip patterns that don't compile.
		}

		re := regexp.MustCompile("(?i)" + pat)
		got := p.MatchString(input)
		want := re.MatchString(input)
		if got != want {
			t.Errorf("compilePCRE(%q).MatchString(%q) = %v, regexp = %v", pat, input, got, want)
		}
	})
}

// --- Edge cases ---

func TestCompilePCRE_empty_pattern(t *testing.T) {
	t.Parallel()
	p, err := compilePCRE("")
	if err != nil {
		t.Fatalf("compilePCRE(\"\") unexpected error: %v", err)
	}
	if !p.MatchString("anything") {
		t.Error("compilePCRE(\"\").MatchString(\"anything\") = false, want true")
	}
	if !p.MatchString("") {
		t.Error("compilePCRE(\"\").MatchString(\"\") = false, want true")
	}
}

func TestCompilePCRE_assertion_only_pattern(t *testing.T) {
	t.Parallel()
	p, err := compilePCRE("(?=foo)")
	if err != nil {
		t.Fatalf("compilePCRE(\"(?=foo)\") unexpected error: %v", err)
	}
	if !p.MatchString("foobar") {
		t.Error("assertion-only pattern should match when assertion is satisfied")
	}
	if p.MatchString("barbar") {
		t.Error("assertion-only pattern should not match when assertion fails")
	}
}

func TestCompilePCRE_escaped_parens_in_lookaround(t *testing.T) {
	t.Parallel()
	// Lookahead contains escaped parens: should match literal (foo) after the match.
	p, err := compilePCRE(`bar(?=\(foo\))`)
	if err != nil {
		t.Fatalf("compilePCRE(escaped parens in lookahead): %v", err)
	}
	if !p.MatchString("bar(foo)") {
		t.Error("escaped parens in lookahead: bar(foo) should match")
	}
	if p.MatchString("barfoo") {
		t.Error("escaped parens in lookahead: barfoo should not match")
	}
}
