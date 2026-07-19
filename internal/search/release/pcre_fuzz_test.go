package release

import (
	"strings"
	"testing"
)

// Match-path fuzz for the marker-based PCRE layer (spec
// subflux-release-parse-fidelity R4.3): the properties cover assertion
// evaluation at marker offsets (compared against naive hand-rolled
// per-position checks) and the bounded shrink-and-recheck contract
// (a candidate survives iff a satisfying width exists). Seeds come from
// the study's adversarial corpus, including the BluRay class-(h) witness.

// foldASCII lower-cases ASCII letters byte-wise, preserving byte offsets
// even on invalid UTF-8, so naive per-position checks agree with the
// layer's (?i) compilation on the ASCII patterns used here (none of the
// letters involved has a non-ASCII Unicode simple-fold equivalent).
func foldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// FuzzMatchPathMarkers cross-checks the layer against naive per-position
// evaluations of four elementary lookaround patterns. Any divergence means
// an assertion was evaluated at the wrong offset (marker plumbing bug).
func FuzzMatchPathMarkers(f *testing.F) {
	seeds := []string{
		"", "x", "xy", "xyx", "ab", "b", "ba", "aab",
		"XY", "aB", "The.Matrix.1999.1080p.BluRay",
		"Movie.2020.BD", "xa xy", "a1 b2 cz",
		"\xff\xfe", "x\xffy", strings.Repeat("xy", 40),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	lookahead := mustCompileFuzz(f, `x(?=y)`)
	negLookahead := mustCompileFuzz(f, `x(?!y)`)
	lookbehind := mustCompileFuzz(f, `(?<=a)b`)
	negLookbehind := mustCompileFuzz(f, `(?<!a)b`)

	f.Fuzz(func(t *testing.T, input string) {
		s := foldASCII(input)

		wantLA := strings.Contains(s, "xy")
		if got := lookahead.MatchString(input); got != wantLA {
			t.Errorf("x(?=y) on %q = %v, want %v", input, got, wantLA)
		}

		wantNLA := false
		for i := range len(s) {
			if s[i] == 'x' && (i+1 >= len(s) || s[i+1] != 'y') {
				wantNLA = true
				break
			}
		}
		if got := negLookahead.MatchString(input); got != wantNLA {
			t.Errorf("x(?!y) on %q = %v, want %v", input, got, wantNLA)
		}

		wantLB := strings.Contains(s, "ab")
		if got := lookbehind.MatchString(input); got != wantLB {
			t.Errorf("(?<=a)b on %q = %v, want %v", input, got, wantLB)
		}

		wantNLB := false
		for i := range len(s) {
			if s[i] == 'b' && (i == 0 || s[i-1] != 'a') {
				wantNLB = true
				break
			}
		}
		if got := negLookbehind.MatchString(input); got != wantNLB {
			t.Errorf("(?<!a)b on %q = %v, want %v", input, got, wantNLB)
		}
	})
}

// FuzzMatchPathShrinkRetry pins the bounded shrink-and-recheck contract on
// the canonical quantifier-adjacent shape (\d+)(?!x): a maximal digit run
// survives iff SOME width w >= 1 puts the assertion at an offset not
// followed by 'x'. It also asserts MatchString/FindStringSubmatch
// consistency and the class-(h) witness pattern's stability on arbitrary
// inputs.
func FuzzMatchPathShrinkRetry(f *testing.F) {
	seeds := []string{
		"", "99x", "9x", "12ab", "1x2x3x", "42", "007xx7",
		"x1x22x333x", "9", "99", "999x999",
		"The.Matrix.1999.1080p.BluRay", "Movie.2020.BD",
		"Show.S01E01.1080p-GROUP.720p", "Movie.x264-GROUP-ES",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	retryPat := mustCompileFuzz(f, `(\d+)(?!x)`)
	witness := mustCompileFuzz(f, `\b(?:BluRay|Blu-Ray|HD-?DVD|BDMux|BD(?!$))\b`)
	flagship := mustCompileFuzz(f, sonarrReleaseGroupRegex)

	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }

	f.Fuzz(func(t *testing.T, input string) {
		s := foldASCII(input)

		// Naive shrink oracle: for each maximal digit run [i,j), some
		// assertion offset o in [i+1, j] must not be followed by 'x'.
		want := false
		for i := 0; i < len(s); {
			if !isDigit(s[i]) {
				i++
				continue
			}
			j := i
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			for o := i + 1; o <= j; o++ {
				if o >= len(s) || s[o] != 'x' {
					want = true
					break
				}
			}
			i = j
		}
		if got := retryPat.MatchString(input); got != want {
			t.Errorf("(\\d+)(?!x) on %q = %v, want %v (shrink-retry contract)", input, got, want)
		}

		// MatchString and FindStringSubmatch must agree on existence for
		// every pattern (merge scan cannot invent or lose matches).
		for _, p := range []*Pattern{retryPat, witness, flagship} {
			match := p.MatchString(input)
			found := p.FindStringSubmatch(input) != nil
			if match != found {
				t.Errorf("pattern %q on %q: MatchString=%v but FindStringSubmatch!=nil is %v",
					p.String(), input, match, found)
			}
		}
	})
}

// mustCompileFuzz compiles a pattern or aborts the fuzz setup.
func mustCompileFuzz(f *testing.F, pat string) *Pattern {
	f.Helper()
	p, err := CompilePCRE(pat)
	if err != nil {
		f.Fatalf("CompilePCRE(%q): %v", pat, err)
	}
	return p
}
