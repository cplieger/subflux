package timing

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// todayClean is internal/subsync/postprocess.go's cleanCueText at 640a639
// with every option on, each regex pass adding the length of the text it
// reads to *read.
func todayClean(text string, read *int) string {
	for {
		out := todayStripTags(text, read)
		out = todayWhitespace(out, read)
		out = todayStripHI(out, read)
		out = todayWhitespace(out, read)
		if out == text {
			return out
		}
		text = out
	}
}

var (
	todayBrackets  = regexp.MustCompile(`\[.*?\]`)
	todayParens    = regexp.MustCompile(`\(.*?\)`)
	todayMusic     = regexp.MustCompile(`[♪♫].*?[♪♫]`)
	todayMusicOnly = regexp.MustCompile(`^[♪♫\s]+$`)
	todaySpeaker   = regexp.MustCompile(`(?m)^[A-Z][A-Z0-9 .'-]+(?:\s*\([A-Z.]+\))?\s*:\s*`)
	todayHTMLTag   = regexp.MustCompile(`</?(?:i|b|u|font)[^>]*>`)
)

func todayReplace(re *regexp.Regexp, text string, read *int) string {
	*read += len(text)
	return re.ReplaceAllString(text, "")
}

func todayStripHI(text string, read *int) string {
	text = todayReplace(todayBrackets, text, read)
	text = todayReplace(todayParens, text, read)
	for {
		out := todayReplace(todayMusic, text, read)
		if out == text {
			break
		}
		text = out
	}
	for {
		out := todayReplace(todaySpeaker, text, read)
		if out == text {
			break
		}
		text = out
	}
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		*read += len(line)
		if !todayMusicOnly.MatchString(line) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func todayStripTags(text string, read *int) string {
	for {
		out := todayReplace(todayHTMLTag, text, read)
		if out == text {
			return out
		}
		text = out
	}
}

func todayWhitespace(text string, read *int) string {
	*read += len(text)
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line != "" && line != "-" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// referenceRules applies the rules lineScan documents the slow way: per
// line, remove the match whose closing rune comes first, and repeat until
// none is left. It is the oracle for applyRules.
func referenceRules(text string, rules textRules) string {
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		if l, ok := referenceLine([]rune(line), rules); ok {
			kept = append(kept, string(l))
		}
	}
	return strings.Join(kept, "\n")
}

func referenceLine(line []rune, rules textRules) ([]rune, bool) {
	for {
		if rules.blanks {
			for len(line) > 0 && unicode.IsSpace(line[0]) {
				line = line[1:]
			}
		}
		from, to, ok := firstMatch(line, rules)
		if !ok {
			break
		}
		line = slices.Concat(line[:from], line[to:])
	}
	if rules.blanks {
		line = []rune(strings.TrimRightFunc(string(line), unicode.IsSpace))
	}
	s := string(line)
	if rules.blanks && (s == "" || s == "-") || rules.hi && musicOnly(s) {
		return nil, false
	}
	return line, true
}

// firstMatch is the match of rules in line whose closing rune comes first.
func firstMatch(line []rune, rules textRules) (from, to int, ok bool) {
	bracket, paren, note, lt := -1, -1, -1, -1
	for j, r := range line {
		from = -1
		switch {
		case rules.hi && r == ']':
			from = bracket
		case rules.hi && r == ')':
			from = paren
		case rules.hi && isNote(r):
			from = note
		case rules.tags && r == '>' && lt >= 0 && lastIndex(line[lt:j], '>') < 0 && formatTag(string(line[lt+1:j])):
			from = lt
		case rules.hi && r == ':' && isLabel(line[:j]):
			to = j + 1
			for to < len(line) && unicode.IsSpace(line[to]) {
				to++
			}
			return 0, to, true
		}
		if from >= 0 {
			return from, j + 1, true
		}
		switch {
		case r == '[' && bracket < 0:
			bracket = j
		case r == '(' && paren < 0:
			paren = j
		case isNote(r) && note < 0:
			note = j
		case r == '<':
			lt = j
		}
	}
	return 0, 0, false
}

func lastIndex(s []rune, r rune) int {
	for k, c := range slices.Backward(s) {
		if c == r {
			return k
		}
	}
	return -1
}

func isLabel(p []rune) bool {
	if len(p) < 2 {
		return false
	}
	for k, r := range p {
		if !labelRune(r, k == 0) {
			return false
		}
	}
	return true
}

func TestCleanStackedLabelsLinear(t *testing.T) {
	t.Parallel()
	all := textRules{hi: true, tags: true, blanks: true}
	t.Run("scan at 100,000 labels", func(t *testing.T) {
		line := strings.Repeat("AB: ", 100_000) + "x"
		var s lineScan
		s.reset(all)
		for i, r := range line {
			s.feed(r, i, 1)
		}
		s.finish()
		if got := string(s.out); got != "x" {
			t.Errorf("cleaned = %q, want x", got)
		}
		if s.work > 4*len(line) {
			t.Errorf("examined %d bytes, over 4 × %d", s.work, len(line))
		}
	})
	t.Run("today's loop at 2,000 labels breaks the bound", func(t *testing.T) {
		line := strings.Repeat("AB: ", 2_000) + "x"
		read := 0
		if got := todayClean(line, &read); got != "x" {
			t.Errorf("todayClean = %q, want x", got)
		}
		if read <= 4*len(line) {
			t.Errorf("today's loop examined %d bytes, within 4 × %d: the bound would not catch it", read, len(line))
		}
	})
}

// TestCleanAgreesWithToday: the cases subtitle text carries, cleaned with
// every text option, as today's post_processing cleans them.
func TestCleanAgreesWithToday(t *testing.T) {
	t.Parallel()
	all := textRules{hi: true, tags: true, blanks: true}
	for _, text := range []string{
		"Hello world",
		"[gunshot]\nGet down!",
		"(laughing) You wish.",
		"♪ la la la ♪\nWhat a song.",
		"♪♪♪",
		"JOHN: Hi.\nMARY: Hello.",
		"DR. SMITH: Sit down.",
		"NARRATOR (V.O.): Long ago.",
		"JOHN: MARY: stacked labels",
		"<i>Italic</i> and <b>bold</b>",
		`<font color="#ffff00">yellow</font>`,
		"- [door closes]\n- Who's there?",
		"  spaces around  ",
		"-",
		"A: one letter is no label",
		"0\nA0:\vA0:\n0",
		"0\n0\n♪♪\v♪",
		"A0:A0:",
		"<[x]i>keep",
	} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			read := 0
			want := todayClean(text, &read)
			if got := applyRules(text, all); got != want {
				t.Errorf("applyRules(%q) = %q, today %q", text, got, want)
			}
		})
	}
}

// TestCleanDeparturesFromToday pins where the one-scan rules answer
// differently from today's regex passes, which ran brackets, then parens,
// then notes, then labels, let a label's blanks cross a line break and
// matched any tag name starting i, b or u.
func TestCleanDeparturesFromToday(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text, want string
		rules            textRules
	}{
		{"the first closer wins across kinds", "(a[b)c]", "c]", textRules{hi: true}},
		{"a note span closes before a paren", "♪a(b♪c)", "c)", textRules{hi: true}},
		{"a label stays in its line", "AB:\nCD: x", "\nx", textRules{hi: true}},
		{"a tag needs an exact name", "<idiot> and <bold>", "<idiot> and <bold>", textRules{tags: true}},
		{"s is a formatting tag", "<s>gone</s>", "gone", textRules{tags: true}},
		{"tag names in any case", "<I>shout</I>", "shout", textRules{tags: true}},
		{"b0 is no tag name", "000000000</<b0>b>", "000000000</<b0>b>", textRules{tags: true}},
		{"a tag stays in its line", "<i\n>x", "<i\n>x", textRules{tags: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := applyRules(tc.text, tc.rules); got != tc.want {
				t.Errorf("applyRules(%q) = %q, want %q", tc.text, got, tc.want)
			}
			if got := referenceRules(tc.text, tc.rules); got != tc.want {
				t.Errorf("referenceRules(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
