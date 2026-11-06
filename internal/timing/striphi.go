package timing

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// textRules is which post_processing text options apply, each within one
// line. hi: [...], (...) and note-to-note spans, the closer taking the
// earliest opener of its kind; a speaker label at the line start (an
// uppercase ASCII letter, one or more of A-Z 0-9 . ' - or blanks, then ':')
// with the blanks after it; a line of only notes and blanks. tags: a
// formatTag from the latest '<' to its '>'. blanks: blanks at either end of a
// line, and a line left empty or "-". A blank is a unicode.IsSpace rune.
type textRules struct{ hi, tags, blanks bool }

// mark is a position in the line's kept output and in the source.
type mark struct{ out, src int }

// opener is a rune a later closer may pair with, and the trailing-blank
// state at it, which a truncation back to it restores.
type opener struct {
	at      mark
	trim    mark
	hasTrim bool
}

// kept is a source range whose runes survive, and where its first rune sits
// in the line's output.
type kept struct{ outFrom, from, to int }

// lineScan applies textRules to one line in one left-to-right pass. A kept
// rune is appended to out once, and a rule completing at the end of out
// truncates out back to where its match began, so every rune is appended
// and removed at most once and the output holds no match of any rule: the
// scan is linear, and its result is a fixed point of the rules.
type lineScan struct {
	out                          []byte // the kept runes, UTF-8
	runs                         []kept
	brackets, parens, notes, lts []opener
	trim                         mark // the trailing blank run's start
	deadAt                       int  // where out stopped reading as a label; -1: it still does
	work                         int  // bytes fed, removed and re-read: linear in the line
	rules                        textRules
	hasTrim, afterLabel          bool
}

func (s *lineScan) reset(rules textRules) {
	*s = lineScan{
		out: s.out[:0], runs: s.runs[:0], rules: rules, deadAt: -1,
		brackets: s.brackets[:0], parens: s.parens[:0], notes: s.notes[:0], lts: s.lts[:0],
	}
}

// feed takes the line's next rune, n source bytes at src.
func (s *lineScan) feed(r rune, src, n int) {
	s.work += n
	blank := unicode.IsSpace(r)
	if blank && len(s.out) == 0 && (s.rules.blanks || s.afterLabel) {
		return
	}
	if !blank {
		s.afterLabel = false
	}
	if s.close(r) {
		return
	}
	at := mark{len(s.out), src}
	s.push(r, opener{at: at, trim: s.trim, hasTrim: s.hasTrim})
	if s.deadAt < 0 && !labelRune(r, len(s.out) == 0) {
		s.deadAt = len(s.out)
	}
	switch {
	case !blank:
		s.hasTrim = false
	case !s.hasTrim:
		s.trim, s.hasTrim = at, true
	}
	if k := len(s.runs) - 1; k >= 0 && s.runs[k].to == src {
		s.runs[k].to += n
	} else {
		s.runs = append(s.runs, kept{outFrom: len(s.out), from: src, to: src + n})
	}
	s.out = utf8.AppendRune(s.out, r)
}

// push records r as an opener a later closer may pair with.
func (s *lineScan) push(r rune, op opener) {
	if st := s.openers(r); st != nil {
		*st = append(*st, op)
	}
}

// openers is the stack r opens under the rules, or nil.
func (s *lineScan) openers(r rune) *[]opener {
	switch {
	case s.rules.hi && r == '[':
		return &s.brackets
	case s.rules.hi && r == '(':
		return &s.parens
	case s.rules.hi && isNote(r):
		return &s.notes
	case s.rules.tags && r == '<':
		return &s.lts
	}
	return nil
}

// closes is the stack whose earliest opener r closes, or nil.
func (s *lineScan) closes(r rune) []opener {
	switch {
	case !s.rules.hi:
		return nil
	case r == ']':
		return s.brackets
	case r == ')':
		return s.parens
	case isNote(r):
		return s.notes
	}
	return nil
}

// close reports whether r completes a match, having removed it.
func (s *lineScan) close(r rune) bool {
	switch st := s.closes(r); {
	case len(st) > 0:
		s.truncate(st[0])
	case s.rules.tags && r == '>' && s.tagOpen():
		s.truncate(s.lts[len(s.lts)-1])
	case s.rules.hi && r == ':' && s.deadAt < 0 && len(s.out) >= 2:
		s.truncate(opener{})
		s.afterLabel = true
	default:
		return false
	}
	return true
}

// tagOpen reports whether out from the latest '<' is a formatting tag once
// a '>' closes it. A '>' kept since that '<' failed it, and leaves the name
// formatTag reads failing.
func (s *lineScan) tagOpen() bool {
	if len(s.lts) == 0 {
		return false
	}
	lt := s.lts[len(s.lts)-1].at.out
	inner := s.out[lt+1 : min(len(s.out), lt+1+len("/font "))]
	s.work += len(inner)
	return formatTag(string(inner))
}

func (s *lineScan) truncate(to opener) {
	p := to.at.out
	s.work += len(s.out) - p
	s.out = s.out[:p]
	for len(s.runs) > 0 && s.runs[len(s.runs)-1].outFrom >= p {
		s.runs = s.runs[:len(s.runs)-1]
	}
	if k := len(s.runs) - 1; k >= 0 {
		s.runs[k].to = min(s.runs[k].to, to.at.src)
	}
	s.brackets, s.parens = popFrom(s.brackets, p), popFrom(s.parens, p)
	s.notes, s.lts = popFrom(s.notes, p), popFrom(s.lts, p)
	if s.deadAt >= p {
		s.deadAt = -1
	}
	s.trim, s.hasTrim = to.trim, to.hasTrim
}

func popFrom(st []opener, p int) []opener {
	for len(st) > 0 && st[len(st)-1].at.out >= p {
		st = st[:len(st)-1]
	}
	return st
}

// finish ends the line: keep is false for a line the rules remove, and text
// reports whether a kept line holds a rune that is not blank.
func (s *lineScan) finish() (keepLine, text bool) {
	if s.rules.blanks && s.hasTrim {
		s.truncate(opener{at: s.trim})
	}
	line := string(s.out)
	s.work += len(line)
	if s.rules.blanks && (line == "" || line == "-") || s.rules.hi && musicOnly(line) {
		return false, false
	}
	return true, strings.TrimFunc(line, unicode.IsSpace) != ""
}

func isNote(r rune) bool { return r == '♪' || r == '♫' }

func labelRune(r rune, first bool) bool {
	if 'A' <= r && r <= 'Z' {
		return true
	}
	return !first && ('0' <= r && r <= '9' || r == '.' || r == '\'' || r == '-' || unicode.IsSpace(r))
}

func musicOnly(line string) bool {
	return strings.ContainsFunc(line, isNote) &&
		strings.TrimFunc(line, func(r rune) bool { return isNote(r) || unicode.IsSpace(r) }) == ""
}

// formatTag reports whether inner, the text of a <...> with no '<' in it, is
// a tag a player reads as formatting: i, b, u, s or font in any
// case, opening or closing, with attributes after a space or tab. Only its
// first six bytes decide.
func formatTag(inner string) bool {
	name := strings.TrimPrefix(inner, "/")
	name = name[:min(len(name), len("font")+1)]
	if k := strings.IndexAny(name, " \t"); k >= 0 {
		name = name[:k]
	}
	switch strings.ToLower(name) {
	case "i", "b", "u", "s", "font":
		return true
	}
	return false
}

// StripHI removes hearing-impaired annotations from one event's decoded
// text, its lines separated by "\n": [...], (...) and ♪...♪ spans, speaker
// labels at a line start (JOHN:, DR. SMITH:), and lines of only notes. It is
// the one HI rule: Clean's strip_hi and subsync's dialogue filter both call
// it. Text is read as UTF-8, an invalid sequence as U+FFFD.
func StripHI(text string) string {
	return applyRules(strings.ToValidUTF8(text, "\uFFFD"), textRules{hi: true})
}

// applyRules is text, valid UTF-8 with lines separated by "\n", after rules.
func applyRules(text string, rules textRules) string {
	var s lineScan
	var b strings.Builder
	kept := 0
	for line := range strings.SplitSeq(text, "\n") {
		s.reset(rules)
		for i, r := range line {
			s.feed(r, i, utf8.RuneLen(r))
		}
		if ok, _ := s.finish(); !ok {
			continue
		}
		if kept > 0 {
			b.WriteByte('\n')
		}
		kept++
		for _, k := range s.runs {
			b.WriteString(line[k.from:k.to])
		}
	}
	return b.String()
}
