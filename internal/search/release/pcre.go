// Package release provides release name parsing via PCRE-compatible regex
// patterns sourced from TRaSH Guides and Sonarr/Radarr QualityParser.
//
// pcre.go provides a thin PCRE-to-RE2 compatibility layer.
// Go's regexp (RE2) does not support lookaheads or lookbehinds.
// This file implements a Pattern type that compiles the RE2-compatible
// core of a PCRE regex and validates lookaround assertions programmatically
// after each match.

package release

import (
	"fmt"
	"regexp"
	"strings"
)

// assertion represents a zero-width lookaround extracted from a PCRE pattern.
type assertion struct {
	re       *regexp.Regexp
	positive bool // true = must match, false = must not match
	ahead    bool // true = lookahead (after match), false = lookbehind (before match)
}

// Pattern wraps an RE2 regex with optional lookaround assertions.
// For alternation patterns, branches holds independently compiled alternatives.
type Pattern struct {
	re         *regexp.Regexp
	original   string // Original PCRE pattern for debugging.
	assertions []assertion
	branches   []*Pattern // Non-nil for top-level alternation patterns.
}

// CompilePCRE compiles a case-insensitive PCRE pattern into an RE2 core
// + assertions.
func CompilePCRE(pat string) (*Pattern, error) {
	alts := SplitTopLevelAlternation(pat)
	if len(alts) == 1 {
		return compileSinglePCRE(pat)
	}
	branches := make([]*Pattern, 0, len(alts))
	for _, alt := range alts {
		p, err := compileSinglePCRE(alt)
		if err != nil {
			return nil, err
		}
		branches = append(branches, p)
	}
	return &Pattern{
		re:       branches[0].re,
		original: pat,
		branches: branches,
	}, nil
}

// compileSinglePCRE compiles a single (non-alternation) PCRE pattern.
func compileSinglePCRE(pat string) (*Pattern, error) {
	const flags = "(?i)"
	core, asserts := extractAssertions(pat)

	re, err := regexp.Compile(flags + core)
	if err != nil {
		return nil, fmt.Errorf("pcre compile core %q: %w", core, err)
	}

	compiled := make([]assertion, len(asserts))
	for i, a := range asserts {
		var anchored string
		if a.ahead {
			anchored = "^(?:" + a.pattern + ")"
		} else {
			anchored = "(?:" + a.pattern + ")$"
		}
		are, err := regexp.Compile(flags + anchored)
		if err != nil {
			return nil, fmt.Errorf("pcre compile assertion %q: %w", a.pattern, err)
		}
		compiled[i] = assertion{re: are, positive: a.positive, ahead: a.ahead}
	}

	return &Pattern{re: re, assertions: compiled, original: pat}, nil
}

// MatchString reports whether s contains a match satisfying all assertions.
func (p *Pattern) MatchString(s string) bool {
	if len(p.branches) > 0 {
		for _, b := range p.branches {
			if b.MatchString(s) {
				return true
			}
		}
		return false
	}
	if len(p.assertions) == 0 {
		return p.re.MatchString(s)
	}
	for _, loc := range p.re.FindAllStringIndex(s, -1) {
		if p.checkAssertions(s, loc[0], loc[1]) {
			return true
		}
	}
	return false
}

// FindStringSubmatch returns the last match and its submatches that
// satisfies all assertions, or nil.
func (p *Pattern) FindStringSubmatch(s string) []string {
	if len(p.branches) > 0 {
		var last []string
		for _, b := range p.branches {
			if m := b.FindStringSubmatch(s); m != nil {
				last = m
			}
		}
		return last
	}
	all := p.re.FindAllStringSubmatchIndex(s, -1)
	if len(all) == 0 {
		return nil
	}
	if len(p.assertions) == 0 {
		return extractSubmatch(s, all[len(all)-1])
	}
	var last []string
	for _, idx := range all {
		if p.checkAssertions(s, idx[0], idx[1]) {
			last = extractSubmatch(s, idx)
		}
	}
	return last
}

// extractSubmatch converts a flat submatch index slice into strings.
func extractSubmatch(s string, idx []int) []string {
	result := make([]string, len(idx)/2)
	for i := range result {
		start, end := idx[i*2], idx[i*2+1]
		if start >= 0 {
			result[i] = s[start:end]
		}
	}
	return result
}

// checkAssertions verifies all lookaround assertions against the text
// surrounding a match at [start, end).
func (p *Pattern) checkAssertions(s string, start, end int) bool {
	for _, a := range p.assertions {
		var subject string
		if a.ahead {
			subject = s[end:]
		} else {
			subject = s[:start]
		}
		if a.positive != a.re.MatchString(subject) {
			return false
		}
	}
	return true
}

// SplitTopLevelAlternation splits a regex on top-level | characters,
// respecting groups (...), character classes [...], and escapes.
func SplitTopLevelAlternation(pattern string) []string {
	var alts []string
	depth := 0
	start := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			i = skipCharClass(pattern, i)
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 {
				alts = append(alts, pattern[start:i])
				start = i + 1
			}
		}
	}
	return append(alts, pattern[start:])
}

// rawAssertion is an uncompiled assertion extracted during parsing.
type rawAssertion struct {
	pattern  string
	positive bool
	ahead    bool
}

// extractAssertions splits a PCRE pattern into an RE2 core and assertions.
func extractAssertions(pattern string) (string, []rawAssertion) {
	var coreBuf strings.Builder
	var asserts []rawAssertion
	i := 0
	for i < len(pattern) {
		if i+2 < len(pattern) && pattern[i] == '(' && pattern[i+1] == '?' {
			rest := pattern[i:]
			if a, consumed, ok := parseLookaround(rest); ok {
				asserts = append(asserts, a)
				i += consumed
				continue
			}
		}
		coreBuf.WriteByte(pattern[i])
		i++
	}
	return coreBuf.String(), asserts
}

// lookaround describes the type of a PCRE lookaround assertion prefix.
type lookaround struct {
	positive  bool
	ahead     bool
	prefixLen int
}

// classifyLookaround identifies the lookaround type from a (?... prefix.
func classifyLookaround(s string) (lookaround, bool) {
	switch {
	case strings.HasPrefix(s, "(?="):
		return lookaround{positive: true, ahead: true, prefixLen: 3}, true
	case strings.HasPrefix(s, "(?!"):
		return lookaround{positive: false, ahead: true, prefixLen: 3}, true
	case strings.HasPrefix(s, "(?<="):
		return lookaround{positive: true, ahead: false, prefixLen: 4}, true
	case strings.HasPrefix(s, "(?<!"):
		return lookaround{positive: false, ahead: false, prefixLen: 4}, true
	default:
		return lookaround{}, false
	}
}

// parseLookaround attempts to parse a lookaround assertion starting at s.
func parseLookaround(s string) (rawAssertion, int, bool) {
	if len(s) < 4 || s[0] != '(' || s[1] != '?' {
		return rawAssertion{}, 0, false
	}
	la, ok := classifyLookaround(s)
	if !ok {
		return rawAssertion{}, 0, false
	}
	depth := 1
	end := la.prefixLen
	for end < len(s) && depth > 0 {
		switch s[end] {
		case '[':
			end = skipCharClass(s, end)
		case '(':
			depth++
		case ')':
			depth--
		case '\\':
			end++
		}
		end++
	}
	if depth != 0 {
		return rawAssertion{}, 0, false
	}
	inner := s[la.prefixLen : end-1]
	return rawAssertion{pattern: inner, positive: la.positive, ahead: la.ahead}, end, true
}

// skipCharClass advances past a [...] character class starting at s[pos]='['.
func skipCharClass(s string, pos int) int {
	pos++
	if pos < len(s) && s[pos] == '^' {
		pos++
	}
	if pos < len(s) && s[pos] == ']' {
		pos++
	}
	for pos < len(s) && s[pos] != ']' {
		if s[pos] == '\\' {
			pos++
		}
		pos++
	}
	return pos
}
