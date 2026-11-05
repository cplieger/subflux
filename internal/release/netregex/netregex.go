// Package netregex runs .NET-flavour regular expressions with .NET's match
// semantics, as dlclark/regexp2 defines them, in polynomial time.
//
// Go's regexp/syntax compiles each pattern and a Pike VM runs it with .NET's
// assertions: Unicode \b, \B, \w, \d and \s, and $ also before a final \n.
// Each lookaround is a position predicate over the whole input, memoized per
// call. A lookbehind costs one pass over the input; a lookahead one anchored
// run per position it is asked at. For an n-byte input and a pattern whose
// programs hold m instructions in all, a call is O(n²·m); release names are
// clamped to 512 bytes. Backreferences, atomic groups, conditionals, inline
// options other than (?i:) and (?-i:), and a named group in a loop that can
// match empty are refused at Compile.
package netregex

import (
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Options are the RegexOptions that change a pattern's meaning.
type Options struct {
	IgnoreCase              bool
	IgnorePatternWhitespace bool
}

// Regexp is a compiled pattern; it is safe for concurrent use.
type Regexp struct {
	pool  sync.Pool // *input
	main  *prog
	pre   *regexp.Regexp
	names []string
	preds []predicate
}

// predicate is one lookaround: its body's program and which way it reads.
type predicate struct {
	p              *prog
	text           string
	behind, negate bool
}

// prog is a compiled program and what its InstCapture Args stand for, -1
// for none: pred[a] is the lookaround a placeholder's opening tests; a
// group occurrence's opening records its start in slot scratch[a], and its
// closing commits that start and its end to the group's slots from group[a],
// so a group holds the occurrence that closed last, as in .NET.
type prog struct {
	inst                 []syntax.Inst
	scratch, group, pred []int
	start, ncap          int
}

// parseFlags leave out OneLine, so ^ and $ parse as the line forms the VM
// gives .NET's meaning: ^ the text start, $ the end or before a final \n.
const parseFlags = syntax.ClassNL | syntax.PerlX | syntax.UnicodeGroups

// compiler collects what the reader finds: the named groups' slots (slot 0
// is the whole match) and the lookarounds, in reading order.
type compiler struct {
	slots map[string]int
	names []string
	preds []predicate
	n     int
}

func (c *compiler) capture(name string) string {
	slot, ok := c.slots[name]
	if !ok {
		slot = len(c.names)
		c.slots[name] = slot
		c.names = append(c.names, name)
	}
	c.n++
	return "g" + strconv.Itoa(slot) + "_" + strconv.Itoa(c.n)
}

func (c *compiler) predicate(text string, behind, negate bool) int {
	c.preds = append(c.preds, predicate{text: text, behind: behind, negate: negate})
	return len(c.preds) - 1
}

// Compile reads a .NET pattern; the error is an *UnsupportedError for a
// construct the executor does not run.
func Compile(pattern string, o Options) (*Regexp, error) {
	c := &compiler{slots: map[string]int{}, names: []string{""}}
	text, err := (&reader{c: c, src: pattern, freeSpacing: o.IgnorePatternWhitespace, fold: o.IgnoreCase}).read()
	if err != nil {
		return nil, err
	}
	if o.IgnoreCase {
		text = "(?i)" + text
	}
	tree, err := syntax.Parse(text, parseFlags)
	if err != nil {
		return nil, err
	}
	if groupInEmptyLoop(tree, false) {
		return nil, unsupported("a named group in a loop that can match empty")
	}
	re := &Regexp{names: c.names, preds: c.preds}
	if re.main, err = compileProg(tree, len(c.names)); err != nil {
		return nil, err
	}
	if re.pre, err = regexp.Compile(prefilter(tree).String()); err != nil {
		return nil, err
	}
	for k := range re.preds {
		t, err := syntax.Parse(re.preds[k].text, parseFlags)
		if err != nil {
			return nil, err
		}
		if re.preds[k].p, err = compileProg(t, 0); err != nil {
			return nil, err
		}
	}
	return re, nil
}

// compileProg compiles tree over groups named groups and maps the reader's
// placeholder captures: p<k> tests predicate k, and g<slot>_<n>, occurrence
// n of a name, commits to its group's slots, so every occurrence of a name
// shares one group, as in .NET.
func compileProg(tree *syntax.Regexp, groups int) (*prog, error) {
	p, err := syntax.Compile(tree.Simplify())
	if err != nil {
		return nil, err
	}
	names := tree.CapNames()
	out := &prog{inst: p.Inst, start: p.Start, ncap: 2 * groups}
	out.scratch, out.group, out.pred = make([]int, 2*len(names)), make([]int, 2*len(names)), make([]int, 2*len(names))
	for i, name := range names {
		bra, ket := 2*i, 2*i+1
		out.scratch[bra], out.scratch[ket], out.group[bra], out.group[ket], out.pred[bra], out.pred[ket] = -1, -1, -1, -1, -1, -1
		if k, ok := strings.CutPrefix(name, "p"); ok {
			out.pred[bra], _ = strconv.Atoi(k)
		} else if slot, _, ok := strings.Cut(strings.TrimPrefix(name, "g"), "_"); ok {
			n, _ := strconv.Atoi(slot)
			out.scratch[bra], out.scratch[ket], out.group[ket] = out.ncap, out.ncap, 2*n
			out.ncap++
		}
	}
	return out, nil
}

// groupInEmptyLoop reports a named group inside a loop whose body can match
// empty. .NET runs one more, empty iteration that sets the group; the VM's
// queue drops that revisit of the loop, so the group would keep the previous
// iteration's text.
func groupInEmptyLoop(re *syntax.Regexp, inLoop bool) bool {
	switch re.Op {
	case syntax.OpCapture:
		if inLoop && strings.HasPrefix(re.Name, "g") {
			return true
		}
	case syntax.OpStar, syntax.OpPlus:
		inLoop = inLoop || nullable(re.Sub[0])
	case syntax.OpRepeat:
		inLoop = inLoop || (re.Max < 0 || re.Max > 1) && nullable(re.Sub[0])
	}
	for _, sub := range re.Sub {
		if groupInEmptyLoop(sub, inLoop) {
			return true
		}
	}
	return false
}

// nullable reports whether re can match the empty string, counting every
// assertion as one that may hold.
func nullable(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune) == 0
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL, syntax.OpNoMatch:
		return false
	case syntax.OpCapture, syntax.OpPlus:
		return nullable(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min == 0 || nullable(re.Sub[0])
	case syntax.OpConcat:
		return !slices.ContainsFunc(re.Sub, func(s *syntax.Regexp) bool { return !nullable(s) })
	case syntax.OpAlternate:
		return slices.ContainsFunc(re.Sub, nullable)
	}
	return true
}

// prefilter is tree with its lookarounds and word boundaries made empty,
// since Go's \b is ASCII, and its groups plain; Go's multiline $ also holds
// wherever .NET's does. It matches wherever the pattern does, so a miss
// answers for the VM.
func prefilter(re *syntax.Regexp) *syntax.Regexp {
	switch re.Op {
	case syntax.OpCapture:
		return prefilter(re.Sub[0])
	case syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return &syntax.Regexp{Op: syntax.OpEmptyMatch}
	case syntax.OpBeginLine:
		return &syntax.Regexp{Op: syntax.OpBeginText}
	}
	cp := *re
	cp.Sub = make([]*syntax.Regexp, len(re.Sub))
	for i, sub := range re.Sub {
		cp.Sub[i] = prefilter(sub)
	}
	return &cp
}

// SubexpNames returns each group's name by its index in a match: index 0 is
// the whole match, and a name used twice in the pattern is one group.
func (re *Regexp) SubexpNames() []string { return slices.Clone(re.names) }

// SubexpIndex returns the index of the named group, or -1.
func (re *Regexp) SubexpIndex(name string) int {
	if name == "" {
		return -1
	}
	return slices.Index(re.names, name)
}

// MatchString is .NET's IsMatch.
func (re *Regexp) MatchString(s string) bool {
	if !re.pre.MatchString(s) {
		return false
	}
	in := re.get(s)
	defer re.put(in)
	return in.machine(0).run(in, 0, modeAny, nil)
}

// Find is .NET's Match(s, from): the leftmost match starting at or after
// byte from, the text before from still in view, as byte offsets in the
// SubexpNames order (-1 for a group that took no part); nil when none.
func (re *Regexp) Find(s string, from int) []int {
	if !re.pre.MatchString(s) {
		return nil
	}
	in := re.get(s)
	defer re.put(in)
	return in.find(from)
}

// FindAll is .NET's Matches: each search starts where the last match ended,
// one rune further after an empty one.
func (re *Regexp) FindAll(s string) [][]int {
	if !re.pre.MatchString(s) {
		return nil
	}
	in := re.get(s)
	defer re.put(in)
	return in.findAll()
}

// Replace is .NET's Replace with a literal replacement: no $ substitution.
func (re *Regexp) Replace(s, replacement string) string {
	if !re.pre.MatchString(s) {
		return s
	}
	in := re.get(s)
	defer re.put(in)
	all := in.findAll()
	if len(all) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range all {
		b.WriteString(s[last:m[0]])
		b.WriteString(replacement)
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}
