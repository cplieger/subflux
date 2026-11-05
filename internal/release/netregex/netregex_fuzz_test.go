package netregex

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2/v2"
)

// FuzzFind_oracle: on any valid UTF-8, every oracle pattern answers what
// regexp2 answers.
func FuzzFind_oracle(f *testing.F) {
	for _, s := range oracleInputs {
		f.Add(s)
	}
	cases := make([]oracleCase, len(oraclePatterns))
	for i, p := range oraclePatterns {
		cases[i] = compileCase(f, p.src, p.opts)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) || len(s) > 512 {
			t.Skip()
		}
		for i, c := range cases {
			diffs, err := c.compare(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range diffs {
				t.Errorf("%s on %q: %s", oraclePatterns[i].src, s, d)
			}
		}
	})
}

// grammar is the reader's accepted syntax as tokens: atoms, groups of each
// kind, alternation, greedy and lazy quantifiers, anchors and assertions.
var grammar = []string{
	"a", "b", ".", " ", "é", "[ab]", "[^a]", `\w`, `\d`, `\s`, `\W`,
	"(", "(?:", "(?<g>", "(?<h>", "(?=", "(?!", "(?<=", "(?<!", "(?i:", "(?-i:", ")", ")", ")",
	"|", "*", "+", "?", "*?", "+?", "??", "{2}", "{0,2}", "{1,}", "{2,}?",
	"^", "$", `\b`, `\B`, `\A`, `\z`, `\Z`,
}

// subjects are the characters generated inputs are spelled with.
var subjects = []rune{'a', 'b', 'A', ' ', '\n', 'é', '_', '1'}

// decodePattern reads s as grammar tokens, longest first; a byte starting
// no token stands for the token its value indexes.
func decodePattern(s string) string {
	var b strings.Builder
	for n := 0; s != "" && n < 16; n++ {
		tok := ""
		for _, g := range grammar {
			if len(g) > len(tok) && strings.HasPrefix(s, g) {
				tok = g
			}
		}
		if tok == "" {
			b.WriteString(grammar[int(s[0])%len(grammar)])
			s = s[1:]
			continue
		}
		b.WriteString(tok)
		s = s[len(tok):]
	}
	return b.String()
}

// decodeSubject maps each rune of s outside subjects onto one of them.
func decodeSubject(s string) string {
	var b strings.Builder
	for n, r := range []rune(s) {
		if n == 16 {
			break
		}
		if !strings.ContainsRune(string(subjects), r) {
			r = subjects[int(r&0x7fffffff)%len(subjects)]
		}
		b.WriteRune(r)
	}
	return b.String()
}

// FuzzCompile_oracle: every pattern of the reader's grammar that Compile
// accepts answers what regexp2 answers, each match's span and every group's
// span included.
func FuzzCompile_oracle(f *testing.F) {
	for _, c := range []struct {
		pattern, subject string
		opts             uint8
	}{
		{`(?<g>a|)*`, "ab", 0},
		{`(?<g>a?)+b`, "ab", 0},
		{`(?<g>a*)*`, "aab", 0},
		{`(?<g>a|b)+?(?=b)|(?<h>.)`, "abab", 0},
		{`\b(?<g>a+?)(?!b)\B`, "aab a_", 1},
		{`(?<=a|^)(?<h>b)$|(?<!\w)é`, "ab\nb é", 1},
		{`^(?:(?<g>[ab]){2}|\s)*?\Z`, "ab \n", 2},
		{`(?i:A)(?-i:a)(?<g>\d|\W){0,2}`, "aa1 ", 0},
	} {
		f.Add(c.pattern, c.subject, c.opts)
	}
	f.Fuzz(func(t *testing.T, pattern, subject string, opts uint8) {
		src, s := decodePattern(pattern), decodeSubject(subject)
		o := Options{IgnoreCase: opts&1 != 0, IgnorePatternWhitespace: opts&2 != 0}
		re, err := Compile(src, o)
		if err != nil {
			t.Skip()
		}
		orig, err := regexp2.Compile(src, dotnetOptions(o))
		if err != nil {
			t.Skip()
		}
		orig.MatchTimeout = 200 * time.Millisecond
		diffs, err := oracleCase{re: re, orig: orig}.compare(s)
		if err != nil {
			t.Skip() // a regexp2 timeout or stack limit: the oracle has no answer
		}
		for _, d := range diffs {
			t.Errorf("%q (%+v) on %q: %s", src, o, s, d)
		}
	})
}
