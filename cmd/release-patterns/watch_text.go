package main

import (
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/runesafe/v3"
)

// Issue text quotes upstream strings only through these helpers: sanitized
// to one line, bounded, and inside a code span or block that no backtick in
// the text can close, so it neither mentions anyone nor renders markdown.

const (
	maxSpanRunes = 300
	maxDiffLines = 200
	// maxDiffCells bounds the line diff's table; a larger change lists the
	// changed region whole.
	maxDiffCells = 4_000_000
)

func codeSpan(s string) string {
	s = runesafe.SanitizeSingleLineBounded(s, maxSpanRunes)
	if s == "" {
		return "(empty)"
	}
	fence := strings.Repeat("`", longestRun(s, '`')+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return fence + s + fence
}

func runesafeName(s string) string { return runesafe.SanitizeSingleLineBounded(s, maxSpanRunes) }

// codeBlock fences at most max lines, saying how many it left out.
func codeBlock(lang string, lines []string, limit int) string {
	more := 0
	if len(lines) > limit {
		more, lines = len(lines)-limit, lines[:limit]
	}
	clean := make([]string, len(lines))
	run := 0
	for i, l := range lines {
		clean[i] = runesafe.SanitizeSingleLine(l)
		run = max(run, longestRun(clean[i], '`'))
	}
	fence := strings.Repeat("`", max(3, run+1))
	out := fence + lang + "\n" + strings.Join(clean, "\n") + "\n" + fence
	if more > 0 {
		out += "\n\n" + strconv.Itoa(more) + " more changed lines; the compare link lists them."
	}
	return out
}

func longestRun(s string, c byte) int {
	best, cur := 0, 0
	for i := range len(s) {
		if s[i] != c {
			cur = 0
			continue
		}
		cur++
		best = max(best, cur)
	}
	return best
}

// lineDiff lists the lines removed from a ("- ") and added in b ("+ ") in
// order, by a longest common subsequence of the region between their common
// prefix and suffix.
func lineDiff(a, b []string) []string {
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	if (len(a)+1)*(len(b)+1) > maxDiffCells {
		return append(prefixed("- ", a), prefixed("+ ", b)...)
	}
	return walkDiff(a, b, lcsTable(a, b))
}

// walkDiff follows lcs from the start, a removal before an addition where
// both keep the subsequence.
func walkDiff(a, b []string, lcs [][]int32) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, "- "+a[i])
			i++
		default:
			out = append(out, "+ "+b[j])
			j++
		}
	}
	return out
}

// lcsTable's [i][j] is the longest common subsequence length of a[i:] and
// b[j:].
func lcsTable(a, b []string) [][]int32 {
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i, x := range slices.Backward(a) {
		for j, y := range slices.Backward(b) {
			if x == y {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	return lcs
}

func prefixed(p string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = p + l
	}
	return out
}
