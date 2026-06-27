package release

import (
	"slices"
	"testing"
)

func mustCompilePCRE(t *testing.T, pat string) *Pattern {
	t.Helper()
	p, err := CompilePCRE(pat)
	if err != nil {
		t.Fatalf("CompilePCRE(%q) error = %v, want nil", pat, err)
	}
	return p
}

// didPanic reports whether calling f panics.
func didPanic(f func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	f()
	return false
}

func TestCompilePCRE_alternation_branches(t *testing.T) {
	t.Parallel()
	single := mustCompilePCRE(t, "foo")
	if len(single.branches) != 0 {
		t.Errorf("CompilePCRE(%q) branches = %d, want 0 (no alternation)", "foo", len(single.branches))
	}
	multi := mustCompilePCRE(t, "a|b")
	if len(multi.branches) != 2 {
		t.Errorf("CompilePCRE(%q) branches = %d, want 2", "a|b", len(multi.branches))
	}
}

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

func TestSplitTopLevelAlternation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{"plain top-level pipes", "a|b|c", []string{"a", "b", "c"}},
		{"pipe inside group is not a split", "(a|b)|c", []string{"(a|b)", "c"}},
		{"pipe inside character class is not a split", "[a|b]|c", []string{"[a|b]", "c"}},
		{"no alternation", "abc", []string{"abc"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SplitTopLevelAlternation(tc.pattern)
			if !slices.Equal(got, tc.want) {
				t.Errorf("SplitTopLevelAlternation(%q) = %#v, want %#v", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestExtractAssertions_plain_core(t *testing.T) {
	t.Parallel()
	core, asserts := extractAssertions("foo")
	if core != "foo" {
		t.Errorf("extractAssertions(%q) core = %q, want %q", "foo", core, "foo")
	}
	if len(asserts) != 0 {
		t.Errorf("extractAssertions(%q) asserts = %d, want 0", "foo", len(asserts))
	}
}

func TestExtractAssertions_trailing_open_paren_no_panic(t *testing.T) {
	t.Parallel()
	var core string
	if didPanic(func() { core, _ = extractAssertions("a(") }) {
		t.Fatalf("extractAssertions(%q) panicked, want no panic", "a(")
	}
	if core != "a(" {
		t.Errorf("extractAssertions(%q) core = %q, want %q", "a(", core, "a(")
	}
}

func TestParseLookaround_minimal(t *testing.T) {
	t.Parallel()
	_, consumed, ok := parseLookaround("(?=)")
	if !ok {
		t.Fatalf("parseLookaround(%q) ok = false, want true", "(?=)")
	}
	if consumed != 4 {
		t.Errorf("parseLookaround(%q) consumed = %d, want 4", "(?=)", consumed)
	}
}

func TestParseLookaround_unbalanced_no_panic(t *testing.T) {
	t.Parallel()
	var consumed int
	var ok bool
	if didPanic(func() { _, consumed, ok = parseLookaround("(?=ab") }) {
		t.Fatalf("parseLookaround(%q) panicked, want graceful (0, false)", "(?=ab")
	}
	if ok {
		t.Errorf("parseLookaround(%q) ok = true, want false", "(?=ab")
	}
	if consumed != 0 {
		t.Errorf("parseLookaround(%q) consumed = %d, want 0", "(?=ab", consumed)
	}
}

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
			var got int
			if didPanic(func() { got = skipCharClass(tc.input, 0) }) {
				t.Fatalf("skipCharClass(%q, 0) panicked, want %d", tc.input, tc.want)
			}
			if got != tc.want {
				t.Errorf("skipCharClass(%q, 0) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}
