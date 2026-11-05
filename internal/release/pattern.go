package release

import "github.com/cplieger/subflux/internal/release/netregex"

// symbolEntry is one imported C# symbol, generated into patterns_gen.go: a
// pattern as published with its options, or a list's or enum's data.
type symbolEntry struct {
	groups      map[string]Source
	override    *override
	id          string
	pattern     string
	replacement string
	values      []string
	options     netregex.Options
}

// formatEntry is one arr's file of a TRaSH streaming custom format.
type formatEntry struct {
	id, label, arr string
	specs          []specEntry
}

// specEntry is one specification: a title or group pattern, which the arrs
// compile under IgnoreCase, or the Sources a Source specification accepts.
type specEntry struct {
	kind             string // title, specGroup or specSource
	pattern          string
	sources          []Source
	negate, required bool
}

// The specification kinds cmd/release-patterns generates.
const (
	specGroup  = "group"
	specSource = "source"
)

func (e *symbolEntry) compile() (*netregex.Regexp, error) {
	return netregex.Compile(e.pattern, e.options)
}

func (e *specEntry) compile() (*netregex.Regexp, error) {
	return netregex.Compile(e.pattern, netregex.Options{IgnoreCase: true})
}

// group returns a named group's text in m, a match of re.
func group(re *netregex.Regexp, s string, m []int, name string) (string, bool) {
	i := re.SubexpIndex(name)
	if i < 0 || m[2*i] < 0 {
		return "", false
	}
	return s[m[2*i]:m[2*i+1]], true
}

// replacer is a RegexReplace: its pattern and its literal replacement.
type replacer struct {
	re   *netregex.Regexp
	with string
}

func (r replacer) replace(s string) string { return r.re.Replace(s, r.with) }
