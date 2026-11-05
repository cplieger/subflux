package release

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/release/netregex"
)

// override is a hand port of one upstream pattern the executor cannot run,
// a backreference. upstream is the sha256 of the upstream text it was
// written from: cmd/release-patterns refuses to generate once that text
// changes.
type override struct {
	port     *groupPort
	upstream string
}

var (
	overrideReleaseGroupSonarr = override{upstream: "e214f1607c89e59f5e6904a31e131265f2f9821160f7018f0810e8eaeb6d6698", port: &releaseGroupSonarr}
	overrideReleaseGroupRadarr = override{upstream: "645041b6944198d9d211c48c7a464ac8ba05c7b8b25db885c1c02e7ea783bda3", port: &releaseGroupRadarr}
)

// groupPort is ReleaseGroupRegex by hand: -name(-part2)? taking the longest
// name first, refused where a resolution token follows (.+?TOKEN) or the
// text before the end is a known non-group (lookbehind, also with part2
// repeated, \k<part2>), then [-._ ][name]$. Word boundaries and digits are
// .NET's Unicode ones, as the name runes reach past ASCII.
type groupPort struct {
	name    func(rune) bool
	ahead   *regexp.Regexp
	behind  *regexp.Regexp
	bracket *regexp.Regexp
}

var groupNames = []string{"", "releasegroup", "part2"}

var (
	releaseGroupSonarr = groupPort{
		name:    foldIn(unicode.RangeTable{R16: []unicode.Range16{{Lo: '0', Hi: '9', Stride: 1}, {Lo: 'A', Hi: 'Z', Stride: 1}, {Lo: 'a', Hi: 'z', Stride: 1}, {Lo: 0xC0, Hi: 0xD6, Stride: 1}, {Lo: 0xD8, Hi: 0xF6, Stride: 1}, {Lo: 0xF8, Hi: 0xFF, Stride: 1}}}),
		ahead:   regexp.MustCompile(`(?i)^.+?(?:HDTV|SDTV|480p|576p|720p|1080p|2160p)`),
		behind:  regexp.MustCompile(`(?i)(?:HDTV|SDTV|WEB-DL|Blu-Ray|480p|576p|720p|1080p|2160p|DTS-HD|DTS-X|DTS-MA|DTS-ES|-ES|-EN|-CAT|-GER|-FRA|-FRE|-ITA|\p{Nd}{1,2}-bit|[ ._]\p{Nd}{4}-\p{Nd}{2}|-\p{Nd}{2})$`),
		bracket: regexp.MustCompile(`(?i)^[-._ ]\[(?P<releasegroup>[A-Za-zÀ-ÖØ-öø-ÿ0-9]+)\]$`),
	}
	releaseGroupRadarr = groupPort{
		name:    foldIn(unicode.RangeTable{R16: []unicode.Range16{{Lo: '0', Hi: '9', Stride: 1}, {Lo: 'a', Hi: 'z', Stride: 1}}}),
		ahead:   regexp.MustCompile(`(?i)^.+?(?:480p|576p|720p|1080p|2160p)`),
		behind:  regexp.MustCompile(`(?i)(?:WEB-(?:DL|Rip)|Blu-Ray|480p|576p|720p|1080p|2160p|DTS-HD|DTS-X|DTS-MA|DTS-ES|-ES|-EN|-CAT|-ENG|-JAP|-GER|-FRA|-FRE|-ITA|-HDRip|\p{Nd}{1,2}-bit|[ ._]\p{Nd}{4}-\p{Nd}{2}|-\p{Nd}{2}|tmdb(?:id)?-\p{Nd}+|tt\p{Nd}{7,8})$`),
		bracket: regexp.MustCompile(`(?i)^[-._ ]\[(?P<releasegroup>[a-z0-9]+)\]$`),
	}
)

// foldIn matches a rune in t under IgnoreCase, as .NET compares a class.
func foldIn(t unicode.RangeTable) func(rune) bool {
	return func(r rune) bool {
		for f := r; ; {
			if unicode.Is(&t, f) {
				return true
			}
			if f = unicode.SimpleFold(f); f == r {
				return false
			}
		}
	}
}

func (g *groupPort) attempt(s string, i int) []int {
	if i < len(s) && s[i] == '-' {
		if m := g.dashed(s, i); m != nil {
			return m
		}
	}
	m := g.bracket.FindStringSubmatchIndex(strings.TrimSuffix(s[i:], "\n"))
	if m == nil {
		return nil
	}
	for j := range m {
		m[j] += i
	}
	return append(m, -1, -1)
}

// run returns the end offset after each name rune from start: ends[k] is
// the end of a k+1 rune name.
func (g *groupPort) run(s string, start int) []int {
	var ends []int
	for j := start; j < len(s); {
		r, w := utf8.DecodeRuneInString(s[j:])
		if !g.name(r) {
			break
		}
		j += w
		ends = append(ends, j)
	}
	return ends
}

// dashed tries alternative one in backtracking order: the longest name, with
// the longest part2 first and then none, before a shorter name.
func (g *groupPort) dashed(s string, i int) []int {
	names := g.run(s, i+1)
	for _, n1 := range slices.Backward(names) {
		if n1 < len(s) && s[n1] == '-' {
			for _, n2 := range slices.Backward(g.run(s, n1+1)) {
				if end, ok := g.ends(s, n2, s[n1:n2]); ok {
					return []int{i, end, i + 1, n2, n1, n2}
				}
			}
		}
		if end, ok := g.ends(s, n1, ""); ok {
			return []int{i, end, i + 1, n1, -1, -1}
		}
	}
	return nil
}

// ends checks the lookahead, the lookbehind and (?:\b|[-._ ]|$) at e and
// returns where the match ends. The rune before e is a name rune, a word
// rune, so \b holds before a non-word rune; '_' is the one word rune
// [-._ ] then takes.
func (g *groupPort) ends(s string, e int, part2 string) (int, bool) {
	if g.ahead.MatchString(s[e:]) {
		return 0, false
	}
	before := s[:e]
	if g.behind.MatchString(before) {
		return 0, false
	}
	if part2 != "" && len(before) >= len(part2) && strings.EqualFold(before[len(before)-len(part2):], part2) &&
		g.behind.MatchString(before[:len(before)-len(part2)]) {
		return 0, false
	}
	if e == len(s) {
		return e, true
	}
	r, _ := utf8.DecodeRuneInString(s[e:])
	switch {
	case !netregex.IsWordChar(r):
		return e, true
	case r == '_':
		return e + 1, true
	}
	return 0, false
}

// find is Match(s, from) over attempt; the port never matches empty.
func (g *groupPort) find(s string, from int) []int {
	for i := from; i < len(s); {
		if m := g.attempt(s, i); m != nil {
			return m
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return nil
}

// findAll is Matches: each search starts where the last match ended.
func (g *groupPort) findAll(s string) [][]int {
	var all [][]int
	for pos := 0; pos < len(s); {
		m := g.find(s, pos)
		if m == nil {
			break
		}
		all = append(all, m)
		pos = m[1]
	}
	return all
}

// portGroup returns a named group's text in m, a match of a groupPort.
func portGroup(s string, m []int, name string) (string, bool) {
	i := slices.Index(groupNames, name)
	if i < 0 || m[2*i] < 0 {
		return "", false
	}
	return s[m[2*i]:m[2*i+1]], true
}
