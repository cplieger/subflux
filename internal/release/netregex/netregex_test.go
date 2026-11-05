package netregex

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/dlclark/regexp2/v2"
)

// oraclePatterns exercise each construct the reader rewrites and each
// assertion the VM evaluates; regexp2, a .NET port, is the oracle.
var oraclePatterns = []struct {
	src  string
	opts Options
}{
	{`\b(amzn|amazon(hd)?)\b`, Options{IgnoreCase: true}},
	{`\Bx\B|\w+|\d+|\s+|\W\D\S`, Options{}},
	{`\Bx\B`, Options{}},
	{`x\b|\bx`, Options{}},
	{`[\w-]+[\d.]*[\s,]?[^\s]`, Options{IgnoreCase: true}},
	{`\[(?:ettv|rarbg)\]$`, Options{IgnoreCase: true}},
	{`^a|b\Z|c\z|\Ad`, Options{}},
	{`(?<g>x)|(?<g>y)(?<h>z)?`, Options{}},
	{`(?<g>a)(?<g>b)`, Options{}},
	{`\b(CNLP)\b(?=[ ._-]web[ ._-]?(dl|rip)\b)`, Options{IgnoreCase: true}},
	{`\b((?<!hbo[ ._-])max)\b(?=[ ._-]web)`, Options{IgnoreCase: true}},
	{`(?<!dts[ .-]?hd[ .-]?)\b(ma|ykw)\b(?=.*\bweb[ ._-]?(dl|rip)\b)`, Options{IgnoreCase: true}},
	{`\[(iT)(?![+])\b|\b(?<![+])(iT)\]`, Options{IgnoreCase: true}},
	{`(?<=[._ \[])(?<g>(Joy|Anime Time)(?=\]|\)))`, Options{IgnoreCase: true}},
	{`^(?:\[(?<sub>(?!\s).+?(?<!\s))\](?:_|-|\s|\.)?)`, Options{IgnoreCase: true}},
	{`^(?:(?:\[|\()\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?<!Naruto-Kun\.)(?:[a-z]{2,6}\.[a-z]{2,6}|[a-z]{2,})\b(?:\s*(?:\]|\))|[ -]{2,})[ -]*`, Options{IgnoreCase: true}},
	{`(Special|Final)(?=(.(Cut|Edition)))(?:.(Cut|Edition))?`, Options{IgnoreCase: true}},
	{"a b # comment\n [ ] c\\ d", Options{IgnorePatternWhitespace: true}},
	{`(?-i:WEB)$|(?i:hd)tv`, Options{IgnoreCase: true}},
	{`a*?b|a+?|x{2,3}?|y{2}`, Options{}},
	{`(?<=\bS\d+\b).*\b(Extras)\b`, Options{IgnoreCase: true}},
	{`x*|(?=y)`, Options{}},
	{`(?<!a(?=b))c|(?<=(?<!x)y)z`, Options{}},
	{`\u00e9\x41[[:a]\e\a\t.`, Options{}},
	{`a(?#note)b{1,}c{`, Options{}},
	{`(?<g>a|b)+c|(?:(?<h>x)y?)*|(?<g>z){0,2}`, Options{}},
	{`(?<g>a(?<g>b)c)|(?<h>x(?<h>y)?)z|(?<g>y(?<h>))`, Options{}},
	{`(?<g>b*){1}x|(?<g>a?){0,1}`, Options{}},
}

var oracleInputs = []string{
	"", "a", "AMZN", "éAMZNé.1080p.WEB-DL", "Show.S01E01.AMZN.WEB-DL", "x amazonhd y", "AMZN\n", "AMZN\n\n",
	"Movie [rarbg]", "Movie [rarbg]\n", "Movie [RARBG]\nx", "a", "d", "b\n", "c\n", "c", "xx", "yz", "ab", "xyz y",
	"Show.CNLP.WEB-DL", "Show.CNLP.web.x", "Show.HBO.MAX.WEB", "Show.MAX.WEB", "Show.hbo-max web", "Movie.MA.WEB-DL",
	"Movie.DTS-HD.MA.1080p.WEB-DL", "Movie.DTSHDMA.WEB", "Movie [iT] x", "Movie +iT]", "[iT+", "[iT]", "Show [Joy]", "Show.Joy)",
	"Show [Anime Time]", "[SubGroup] Show - 01", "[ Sub ] Show", "[a]b] x", "[] x", "www.Naruto-Kun.org - Show - 01",
	"www.ab--naruto-kun.com - Show - 01", "[www.ab--Naruto-Kun.org] Show", "[ www.site.tv ] Show", "Movie.Special.Cut",
	"Movie.Final)Edition", "Movie.Special.Cutting", "ab cd", "a  [ ]c d", "abc", "WEB", "web", "HDTV", "hdtv", "Show.WEB",
	"aaab", "aaa", "xz", "xyzy", "xxx", "yy", "Show S01 Bonus EXTRAS DVDRip", "S01E02 Extras", "acb", "abc yz xyz", "éA[c\x1b\x07\tx",
	"ab{1,}c{", "abbbc{", "éxé", "xé", "éx", "x", "_x_", "x\u200dAMZN", "ǅamzn", "ＡＭＺＮ", "١٠٨٠p", "Cafe\u0301 AMZN", "a\u00a0b", "a\u2003b", "ſ K",
}

func dotnetOptions(o Options) regexp2.RegexOptions {
	opts := regexp2.None
	if o.IgnoreCase {
		opts |= regexp2.IgnoreCase
	}
	if o.IgnorePatternWhitespace {
		opts |= regexp2.IgnorePatternWhitespace
	}
	return opts
}

type oracleCase struct {
	re   *Regexp
	orig *regexp2.Regexp
}

func compileCase(t testing.TB, src string, o Options) oracleCase {
	t.Helper()
	re, err := Compile(src, o)
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	return oracleCase{re: re, orig: regexp2.MustCompile(src, dotnetOptions(o))}
}

func span(i, j int) string { return strconv.Itoa(i) + "-" + strconv.Itoa(j) }

// describe spells a match as its span and each taking part group's span.
func (c oracleCase) describe(m []int) string {
	var b strings.Builder
	b.WriteString(span(m[0], m[1]))
	for i, name := range c.re.names {
		if i > 0 && m[2*i] >= 0 {
			b.WriteString(" " + name + "=" + span(m[2*i], m[2*i+1]))
		}
	}
	return b.String()
}

func (c oracleCase) describeDotnet(m *regexp2.Match) string {
	var b strings.Builder
	i, n := m.ByteRange()
	b.WriteString(span(i, i+n))
	for _, name := range c.re.names[1:] {
		if g := m.GroupByName(name); g != nil && len(g.Captures) > 0 {
			i, n := g.ByteRange()
			b.WriteString(" " + name + "=" + span(i, i+n))
		}
	}
	return b.String()
}

// compare returns how the executor's answers for s differ from regexp2's;
// the error is regexp2's, a match timeout included.
func (c oracleCase) compare(s string) ([]string, error) {
	var diffs []string
	var got, want []string
	for _, m := range c.re.FindAll(s) {
		got = append(got, c.describe(m))
	}
	m, err := c.orig.FindStringMatch(s)
	for m != nil && err == nil {
		want = append(want, c.describeDotnet(m))
		m, err = c.orig.FindNextMatch(m)
	}
	if err != nil {
		return nil, err
	}
	if g, w := strings.Join(got, "; "), strings.Join(want, "; "); g != w {
		diffs = append(diffs, "matches "+g+", want "+w)
	}
	ok, err := c.orig.MatchString(s)
	if err != nil {
		return nil, err
	}
	if c.re.MatchString(s) != ok {
		diffs = append(diffs, "MatchString "+strconv.FormatBool(!ok))
	}
	repl, err := c.orig.Replace(s, "<>", -1, -1)
	if err != nil {
		return nil, err
	}
	if got := c.re.Replace(s, "<>"); got != repl {
		diffs = append(diffs, "Replace "+strconv.Quote(got)+", want "+strconv.Quote(repl))
	}
	return diffs, nil
}

func TestMatchesOracle(t *testing.T) {
	for _, p := range oraclePatterns {
		t.Run(p.src, func(t *testing.T) {
			c := compileCase(t, p.src, p.opts)
			for _, s := range oracleInputs {
				diffs, err := c.compare(s)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range diffs {
					t.Errorf("%q: %s", s, d)
				}
			}
		})
	}
}

func TestFindSeesTheTextBefore(t *testing.T) {
	re, err := Compile(`(?<=a)b|^c|\bd`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		s    string
		from int
		want int
	}{{"ab", 1, 1}, {"cb", 1, -1}, {"cc", 1, -1}, {"xd", 1, -1}, {" d", 1, 1}} {
		m := re.Find(c.s, c.from)
		got := -1
		if m != nil {
			got = m[0]
		}
		if got != c.want {
			t.Errorf("Find(%q, %d) starts at %d, want %d", c.s, c.from, got, c.want)
		}
	}
}

func TestCompileRefuses(t *testing.T) {
	cases := []struct{ name, src, construct string }{
		{"a backreference", `(?<a>x)\k<a>`, "a backreference"},
		{"a numbered backreference", `(x)\1`, "a backreference"},
		{"class subtraction", `[a-z-[aeiou]]`, "class subtraction"},
		{"an atomic group", `(?>a)`, "(?>a"},
		{"explicit capture", `(?n:a)`, "(?n:"},
		{"an inline option without a body", `(?i)a`, "(?i)"},
		{"a quantified assertion", `\b*a`, "a quantified assertion"},
		{"a quantified lookaround", `(?=a)+a`, "a quantified assertion"},
		{"a named group in a lookaround", `(?=(?<g>a))`, "a named group inside a lookaround"},
		{`\S in a class`, `[\S]`, `\S inside a class`},
		{`\W in a class`, `[\W]`, `\W inside a class`},
		{"an unknown escape", `\y`, `\y`},
		{`\G`, `\Ga`, `\G`},
		{"a group in a star loop that can match empty", `(?<g>a|)*`, "a named group in a loop that can match empty"},
		{"a group in a plus loop that can match empty", `(?<g>a?)+b`, "a named group in a loop that can match empty"},
		{"a group under a lazy loop that can match empty", `(?:x|(?<g>a)*)*?`, "a named group in a loop that can match empty"},
		{"a group in a counted loop that can match empty", `(?<g>a*){2,}`, "a named group in a loop that can match empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compile(c.src, Options{})
			if u, ok := errors.AsType[*UnsupportedError](err); !ok || u.Construct != c.construct {
				t.Errorf("got %v, want unsupported %q", err, c.construct)
			}
		})
	}
}

// TestSpaceClassIsWhiteSpace: \s is char.IsWhiteSpace, which unicode.IsSpace
// matches.
func TestSpaceClassIsWhiteSpace(t *testing.T) {
	re := regexp.MustCompile(`^[` + spaceClass + `]$`)
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if re.MatchString(string(r)) != unicode.IsSpace(r) {
			t.Errorf("%U: in \\s %t, IsSpace %t", r, !unicode.IsSpace(r), unicode.IsSpace(r))
		}
	}
}

// TestWordClassIsWordChar: \w's class and the \b test read one set.
func TestWordClassIsWordChar(t *testing.T) {
	re := regexp.MustCompile(`^[` + wordClass + `]$`)
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if re.MatchString(string(r)) != IsWordChar(r) {
			t.Errorf("%U: in \\w %t, IsWordChar %t", r, !IsWordChar(r), IsWordChar(r))
		}
	}
}

// worstCases are the shapes whose cost grows fastest: a lookahead scanning
// to the end at every position, a lookbehind beside it, and a lookahead
// asked inside another's scan, which only the per-call memo keeps at n².
var worstCases = []struct {
	src  string
	unit string
}{
	{`(?<!dts[ .-]?hd[ .-]?)\b(ma|ykw)\b(?=.*\bweb[ ._-]?(dl|rip)\b)`, "ma "},
	{`(?=.*x)(?=.*y)a`, "a"},
	{`(?<=a.*)b(?=.*c)`, "ab"},
	{`(?=(?:.(?=.*a$))*c)a`, "a"},
}

func worstInput(unit string, n int) string { return strings.Repeat(unit, n/len(unit)+1)[:n] }

func cost(re *Regexp, s string) int {
	in := re.get(s)
	defer re.put(in)
	in.findAll()
	return in.steps
}

// TestWorstCaseAtTheClamp pins the package doc's bound: at 512 bytes each
// shape stays within n² queue insertions per program instruction, and
// doubling n grows the insertion count at most 4.4 times.
func TestWorstCaseAtTheClamp(t *testing.T) {
	const n = 512
	for _, c := range worstCases {
		t.Run(c.src, func(t *testing.T) {
			re, err := Compile(c.src, Options{IgnoreCase: true})
			if err != nil {
				t.Fatal(err)
			}
			insts := len(re.main.inst)
			for _, p := range re.preds {
				insts += len(p.p.inst)
			}
			full, half := cost(re, worstInput(c.unit, n)), cost(re, worstInput(c.unit, n/2))
			if full > n*n*insts {
				t.Errorf("%d insertions at %d bytes, over n²·m = %d", full, n, n*n*insts)
			}
			if float64(full) > 4.4*float64(half) {
				t.Errorf("%d insertions at %d bytes, %d at %d: grows faster than n²", full, n, half, n/2)
			}
		})
	}
}

func BenchmarkWorstCaseAtTheClamp(b *testing.B) {
	for _, c := range worstCases {
		re, err := Compile(c.src, Options{IgnoreCase: true})
		if err != nil {
			b.Fatal(err)
		}
		s := worstInput(c.unit, 512)
		b.Run(c.unit, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				re.FindAll(s)
			}
		})
	}
}
