package release

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode/utf8"
)

// This file implements the compile step of the PCRE-to-RE2 layer (spec
// subflux-release-parse-fidelity, design "Phase 3 — repair architecture").
//
// A source pattern is compiled in three passes:
//
//  1. parse: the source text becomes a small tree (alternation of
//     sequences of nodes). Lookarounds become assertion nodes; unsupported
//     PCRE constructs (backreferences, atomic groups, conditionals, nested
//     lookarounds) are rejected with typed *CompileError values.
//  2. analyze: bottom-up minimum-width computation, detection of
//     assertions inside quantified groups (typed compile error — Go regexp
//     reports only the last iteration's marker offset, so per-iteration
//     checking is impossible), and resolution of each assertion's
//     quantifier-adjacency chain for the bounded shrink-and-recheck retry.
//  3. emit: per top-level branch, an RE2 core is emitted with an empty
//     marker group `()` at each assertion site, synthetic anchor groups
//     wrapped around retry-chain elements, and a source→core capture-group
//     remap so the public FindStringSubmatch exposes SOURCE numbering.
//
// Capture groups inside lookaround assertions are counted for source
// numbering parity with .NET but never participate (divergence class (k),
// documented contract).

// nodeKind identifies the parse-tree node variants.
type nodeKind uint8

const (
	nAtom   nodeKind = iota // consuming atom: literal rune, escape, class, dot
	nAnchor                 // zero-width passthrough: \b \B \A \z ^ $ (?i) ...
	nGroup                  // (...) (?:...) (?flags:...)
	nAssert                 // lookaround assertion
)

// seq is one alternation branch: a concatenation of nodes.
type seq []*node

// node is a parse-tree node.
type node struct {
	text    string // nAtom/nAnchor: raw source text
	quant   string // quantifier suffix incl. laziness ("", "?", "*", "+", "{m,n}", ...)
	prefix  string // nGroup: "(", "(?:", "(?-i:", ...
	inner   string // nAssert: raw inner regex text
	contSrc string // nAssert: source text of the same-level pattern remainder

	body       []seq   // nGroup: alternation body
	tailChain  []*node // analysis: trailing quantified elements (unquantified groups)
	tailPath   []*node // analysis: capturing groups traversed to reach tailChain
	retryChain []*node // nAssert: leftmost-first shrink chain
	retryPath  []*node // nAssert: capturing groups the chain was adopted through

	minRep      int // parsed minimum repetition count (1 when unquantified)
	srcIndex    int // nGroup capturing: source group number
	srcOff      int // byte offset in source pattern
	srcEnd      int // byte offset one past this element (incl. quantifier)
	minW        int // analysis: minimum consumed width in bytes
	coreGroup   int // emit: core group index for remap (capturing groups)
	anchorGroup int // emit: core group index of the synthetic retry-anchor wrap

	kind      nodeKind
	capturing bool
	positive  bool // nAssert
	ahead     bool // nAssert
	hasAssert bool // subtree contains an assertion
	needsWrap bool // element needs a synthetic anchor group
	contOK    bool // nAssert: contSrc is assertion- and capture-free
}

// isZeroWidth reports whether the node consumes no input.
func (n *node) isZeroWidth() bool {
	return n.kind == nAnchor || n.kind == nAssert
}

// --- pass 1: parse ---

// parser holds the source scan state. The source capture-group counter is
// global across top-level alternation branches so exposed numbering matches
// the SOURCE pattern (and the .NET oracle's whole-pattern numbering).
type parser struct {
	src  string
	pos  int
	nSrc int
}

// parseAlt parses an alternation until ')' (depth>0) or end of source.
func (ps *parser) parseAlt(depth int) ([]seq, error) {
	var alts []seq
	for {
		sq, err := ps.parseSeq(depth)
		if err != nil {
			return nil, err
		}
		alts = append(alts, sq)
		if ps.pos < len(ps.src) && ps.src[ps.pos] == '|' {
			ps.pos++
			continue
		}
		return alts, nil
	}
}

// parseSeq parses one concatenation until '|', ')' (depth>0), or end.
func (ps *parser) parseSeq(depth int) (seq, error) {
	var sq seq
	for ps.pos < len(ps.src) {
		c := ps.src[ps.pos]
		if c == '|' {
			return sq, nil
		}
		if c == ')' {
			if depth > 0 {
				return sq, nil
			}
			return nil, compileErr(")", ps.pos, "unmatched closing parenthesis")
		}
		n, err := ps.parseElement(depth)
		if err != nil {
			return nil, err
		}
		if err := ps.attachQuantifier(n); err != nil {
			return nil, err
		}
		n.srcEnd = ps.pos
		sq = append(sq, n)
	}
	return sq, nil
}

// parseElement parses a single atom, anchor, group, or assertion.
func (ps *parser) parseElement(depth int) (*node, error) {
	start := ps.pos
	switch c := ps.src[ps.pos]; c {
	case '\\':
		return ps.parseEscape()
	case '[':
		// Past the closing ']'; an unterminated class caps at the source
		// end and the RE2 core compile rejects it.
		end := min(skipCharClass(ps.src, ps.pos)+1, len(ps.src))
		ps.pos = end
		return &node{kind: nAtom, text: ps.src[start:end], minRep: 1, minW: 1, srcOff: start}, nil
	case '^', '$':
		ps.pos++
		return &node{kind: nAnchor, text: string(c), minRep: 1, srcOff: start}, nil
	case '(':
		return ps.parseGroup(depth)
	default:
		// Single rune literal (multi-byte runes consumed whole).
		_, size := utf8.DecodeRuneInString(ps.src[ps.pos:])
		ps.pos += size
		return &node{kind: nAtom, text: ps.src[start:ps.pos], minRep: 1, minW: size, srcOff: start}, nil
	}
}

// zeroWidthEscapes are escapes that consume no input.
var zeroWidthEscapes = map[byte]bool{'b': true, 'B': true, 'A': true, 'z': true}

// parseEscape parses a backslash escape.
func (ps *parser) parseEscape() (*node, error) {
	start := ps.pos
	if ps.pos+1 >= len(ps.src) {
		return nil, compileErr(`\`, start, "trailing backslash")
	}
	c := ps.src[ps.pos+1]
	switch {
	case c >= '1' && c <= '9':
		return nil, compileErr(ps.src[start:start+2], start, "backreference is not supported (RE2 has no backtracking)")
	case c == 'k' || c == 'g':
		return nil, compileErr(ps.src[start:start+2], start, "named/relative backreference is not supported (RE2 has no backtracking)")
	}
	ps.pos += 2
	txt := ps.src[start:ps.pos]
	if zeroWidthEscapes[c] {
		return &node{kind: nAnchor, text: txt, minRep: 1, srcOff: start}, nil
	}
	return &node{kind: nAtom, text: txt, minRep: 1, minW: 1, srcOff: start}, nil
}

// lookaroundPrefix classifies a lookaround opening at s, returning
// (positive, ahead, prefixLen).
func lookaroundPrefix(s string) (positive, ahead bool, prefixLen int) {
	switch {
	case strings.HasPrefix(s, "(?="):
		return true, true, 3
	case strings.HasPrefix(s, "(?!"):
		return false, true, 3
	case strings.HasPrefix(s, "(?<="):
		return true, false, 4
	case strings.HasPrefix(s, "(?<!"):
		return false, false, 4
	}
	return false, false, 0
}

// parseGroup parses '(' dispatch: capturing group, non-capturing group,
// inline flags, lookaround assertion, or a rejected PCRE-only construct.
func (ps *parser) parseGroup(depth int) (*node, error) {
	start := ps.pos
	rest := ps.src[ps.pos:]

	if pos, ahead, plen := lookaroundPrefix(rest); plen > 0 {
		return ps.parseLookaroundNode(pos, ahead, plen)
	}

	if !strings.HasPrefix(rest, "(?") {
		// Plain capturing group.
		ps.pos++
		ps.nSrc++
		return ps.parseGroupBody(&node{
			kind: nGroup, capturing: true, srcIndex: ps.nSrc,
			prefix: "(", minRep: 1, srcOff: start,
		}, depth)
	}

	if strings.HasPrefix(rest, "(?:") {
		ps.pos += 3
		return ps.parseGroupBody(&node{kind: nGroup, prefix: "(?:", minRep: 1, srcOff: start}, depth)
	}
	if construct, reason, rejected := rejectedGroupSyntax(rest); rejected {
		return nil, compileErr(construct, start, reason)
	}
	if strings.HasPrefix(rest, "(?<") || strings.HasPrefix(rest, "(?P<") {
		return ps.parseNamedGroup(rest, start, depth)
	}
	return ps.parseFlagsGroup(rest, start, depth)
}

// rejectedGroupSyntax classifies PCRE-only group constructs the layer
// rejects with typed errors.
func rejectedGroupSyntax(rest string) (construct, reason string, rejected bool) {
	switch {
	case strings.HasPrefix(rest, "(?>"):
		return "(?>", "atomic group is not supported (RE2 has no backtracking)", true
	case strings.HasPrefix(rest, "(?("):
		return "(?(", "conditional group is not supported", true
	case strings.HasPrefix(rest, "(?#"):
		return "(?#", "inline comment is not supported", true
	case strings.HasPrefix(rest, "(?P="):
		return "(?P=", "named backreference is not supported (RE2 has no backtracking)", true
	case strings.HasPrefix(rest, "(?'"):
		return "(?'", "quoted group name is not supported; use (?<name>...)", true
	}
	return "", "", false
}

// parseNamedGroup parses (?<name>...) / (?P<name>...) captures. The name
// is dropped: only numbering is exposed.
func (ps *parser) parseNamedGroup(rest string, start, depth int) (*node, error) {
	open := "(?<"
	if strings.HasPrefix(rest, "(?P<") {
		open = "(?P<"
	}
	nameEnd := strings.IndexByte(rest[len(open):], '>')
	if nameEnd < 0 {
		return nil, compileErr(open, start, "unterminated group name")
	}
	ps.pos += len(open) + nameEnd + 1
	ps.nSrc++
	return ps.parseGroupBody(&node{
		kind: nGroup, capturing: true, srcIndex: ps.nSrc,
		prefix: "(", minRep: 1, srcOff: start,
	}, depth)
}

// parseFlagsGroup parses inline flags: (?flags) or (?flags:...).
func (ps *parser) parseFlagsGroup(rest string, start, depth int) (*node, error) {
	i := 2
	for i < len(rest) && (rest[i] == '-' || (rest[i] >= 'a' && rest[i] <= 'z') || (rest[i] >= 'A' && rest[i] <= 'Z')) {
		i++
	}
	if i < len(rest) && rest[i] == ')' && i > 2 {
		ps.pos += i + 1
		return &node{kind: nAnchor, text: rest[:i+1], minRep: 1, srcOff: start}, nil
	}
	if i < len(rest) && rest[i] == ':' && i > 2 {
		ps.pos += i + 1
		return ps.parseGroupBody(&node{kind: nGroup, prefix: rest[:i+1], minRep: 1, srcOff: start}, depth)
	}
	return nil, compileErr(head(rest, 3), start, "unsupported group syntax")
}

// head returns at most n leading bytes of s.
func head(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// parseGroupBody parses a group's alternation body and its closing paren.
func (ps *parser) parseGroupBody(n *node, depth int) (*node, error) {
	body, err := ps.parseAlt(depth + 1)
	if err != nil {
		return nil, err
	}
	if ps.pos >= len(ps.src) || ps.src[ps.pos] != ')' {
		return nil, compileErr(n.prefix, n.srcOff, "unterminated group")
	}
	ps.pos++
	n.body = body
	return n, nil
}

// parseLookaroundNode extracts a lookaround assertion's raw inner text.
func (ps *parser) parseLookaroundNode(positive, ahead bool, prefixLen int) (*node, error) {
	start := ps.pos
	s := ps.src[ps.pos:]
	depth := 1
	end := prefixLen
	for end < len(s) && depth > 0 {
		switch s[end] {
		case '[':
			end = skipCharClass(s, end)
			if end >= len(s) {
				break
			}
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
		return nil, compileErr(s[:prefixLen], start, "unterminated lookaround")
	}
	inner := s[prefixLen : end-1]
	if off := findNestedLookaround(inner); off >= 0 {
		return nil, compileErr(s[:prefixLen], start+prefixLen+off,
			"nested lookaround inside an assertion is not supported")
	}
	ps.pos += end
	return &node{
		kind: nAssert, positive: positive, ahead: ahead,
		inner: inner, minRep: 1, srcOff: start,
	}, nil
}

// findNestedLookaround scans raw assertion-inner text for a nested
// lookaround, respecting escapes and character classes. Returns the byte
// offset of the nested construct or -1.
func findNestedLookaround(s string) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			i = skipCharClass(s, i)
		case '(':
			if _, _, plen := lookaroundPrefix(s[i:]); plen > 0 {
				return i
			}
		}
	}
	return -1
}

// countCapturesIn counts capturing groups in raw regex text (used for
// assertion-inner text: those groups take part in SOURCE numbering for
// .NET parity but never participate in matches — divergence class (k)).
func countCapturesIn(s string) int {
	count := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			i = skipCharClass(s, i)
		case '(':
			if isCaptureOpen(s[i:]) {
				count++
			}
		}
	}
	return count
}

// isCaptureOpen reports whether raw regex text starting at '(' opens a
// capturing group: a plain group, (?<name>...), or (?P<name>...) — but not
// a lookbehind.
func isCaptureOpen(s string) bool {
	if len(s) < 2 || s[1] != '?' {
		return true // plain (
	}
	if len(s) < 3 || (s[2] != '<' && s[2] != 'P') {
		return false
	}
	if s[2] == '<' && len(s) > 3 && (s[3] == '=' || s[3] == '!') {
		return false // lookbehind
	}
	return true
}

// attachQuantifier parses an optional quantifier following an element.
func (ps *parser) attachQuantifier(n *node) error {
	if ps.pos >= len(ps.src) {
		return nil
	}
	start := ps.pos
	var minRep int
	switch ps.src[ps.pos] {
	case '?', '*':
		minRep = 0
		ps.pos++
	case '+':
		minRep = 1
		ps.pos++
	case '{':
		m, consumed, ok := parseRepeat(ps.src[ps.pos:])
		if !ok {
			return nil // literal '{', not a quantifier
		}
		minRep = m
		ps.pos += consumed
	default:
		return nil
	}
	// Lazy suffix; a possessive suffix is a PCRE-only construct.
	if ps.pos < len(ps.src) {
		switch ps.src[ps.pos] {
		case '?':
			ps.pos++
		case '+':
			return compileErr(ps.src[start:ps.pos+1], start, "possessive quantifier is not supported")
		}
	}
	if n.kind == nAssert {
		return compileErr(ps.src[start:ps.pos], start,
			"quantified assertion is not supported (marker participation would over-enforce it)")
	}
	n.quant = ps.src[start:ps.pos]
	n.minRep = minRep
	return nil
}

// parseRepeat parses a {m}, {m,}, or {m,n} repetition. Returns the minimum
// count, bytes consumed, and whether the text is a well-formed repetition.
func parseRepeat(s string) (minRep, consumed int, ok bool) {
	i := 1
	m := 0
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		m = m*10 + int(s[i]-'0')
		i++
		digits++
	}
	if digits == 0 {
		return 0, 0, false
	}
	if i < len(s) && s[i] == ',' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < len(s) && s[i] == '}' {
		return m, i + 1, true
	}
	return 0, 0, false
}

// --- pass 2: analyze ---

// analyzeAlt computes minimum widths bottom-up, resolves assertion retry
// chains, and rejects assertions inside quantified groups.
func analyzeAlt(alts []seq, src string) (minW int, tail, tailPath []*node, hasAssert bool, err error) {
	for i, sq := range alts {
		w, t, tp, ha, err := analyzeSeq(sq, src)
		if err != nil {
			return 0, nil, nil, false, err
		}
		if i == 0 || w < minW {
			minW = w
		}
		hasAssert = hasAssert || ha
		if len(alts) == 1 {
			tail, tailPath = t, tp // no chain adoption through multi-branch groups
		}
	}
	return minW, tail, tailPath, hasAssert, nil
}

// analyzeSeq analyzes one concatenation left to right.
func analyzeSeq(sq seq, src string) (minW int, tail, tailPath []*node, hasAssert bool, err error) {
	for i, n := range sq {
		switch n.kind {
		case nGroup:
			if err := analyzeGroupNode(n, src); err != nil {
				return 0, nil, nil, false, err
			}
			hasAssert = hasAssert || n.hasAssert
		case nAtom:
			n.minW *= n.minRep
		case nAssert:
			hasAssert = true
			analyzeAssertNode(n, sq, i, src)
		case nAnchor:
			// zero width
		}
		minW += n.minW
	}
	tail, tailPath = trailingChain(sq)
	return minW, tail, tailPath, hasAssert, nil
}

// analyzeGroupNode analyzes a group's body, propagating minimum width and
// the trailing chain, and enforcing normative marker rule 2 (an assertion
// inside a quantified group is a typed compile error).
func analyzeGroupNode(n *node, src string) error {
	bodyW, bodyTail, bodyTailPath, bodyHA, err := analyzeAlt(n.body, src)
	if err != nil {
		return err
	}
	n.hasAssert = bodyHA
	n.minW = n.minRep * bodyW
	if n.quant == "" {
		n.tailChain = bodyTail
		n.tailPath = bodyTailPath
	}
	if n.quant != "" && bodyHA {
		return compileErr(n.quant, n.srcOff,
			"assertion inside a quantified group requires consuming-path backtracking "+
				"(Go regexp reports only the last iteration's marker offset)")
	}
	return nil
}

// analyzeAssertNode resolves an assertion's shrink chain and same-level
// continuation. The continuation is the pattern remainder at the
// assertion's own level: .NET re-matches the rest of the pattern from a
// shrunken offset; a prefix match of the remainder at the probed offset is
// the bounded necessary-condition analogue.
func analyzeAssertNode(n *node, sq seq, i int, src string) {
	n.hasAssert = true
	n.retryChain, n.retryPath = adjacentChain(sq[:i])
	for _, ce := range n.retryChain {
		ce.needsWrap = true
	}
	if i+1 < len(sq) {
		n.contSrc = src[n.srcEnd:sq[len(sq)-1].srcEnd]
		n.contOK = findNestedLookaround(n.contSrc) < 0
	}
}

// adjacentChain resolves the contiguous run of quantified elements
// immediately preceding an assertion (skipping zero-width elements),
// leftmost-first, plus the capturing groups the chain was adopted through.
// When the immediately preceding element is an unquantified group, its own
// trailing chain is adopted — this is the flagship release-group shape,
// where the lookbehind follows the group whose body ends in quantified
// subexpressions.
func adjacentChain(prev seq) (chain, path []*node) {
	for _, n := range slices.Backward(prev) {
		if n.isZeroWidth() {
			continue
		}
		if n.quant != "" {
			chain = append([]*node{n}, chain...)
			continue
		}
		if len(chain) == 0 && n.kind == nGroup && len(n.tailChain) > 0 {
			chain = append([]*node(nil), n.tailChain...)
			path = append([]*node{n}, n.tailPath...)
		}
		break
	}
	return chain, path
}

// trailingChain computes a sequence's own trailing quantified-element chain
// (for adoption by a following assertion outside the enclosing group) and
// the adoption path of capturing groups.
func trailingChain(sq seq) (chain, path []*node) {
	for _, n := range slices.Backward(sq) {
		if n.isZeroWidth() {
			continue
		}
		if n.quant != "" {
			chain = append([]*node{n}, chain...)
			continue
		}
		if len(chain) == 0 && n.kind == nGroup && len(n.tailChain) > 0 {
			chain = append([]*node(nil), n.tailChain...)
			path = append([]*node{n}, n.tailPath...)
		}
		break
	}
	return chain, path
}

// --- pass 3: emit ---

// emitter builds one branch's RE2 core text and group bookkeeping.
type emitter struct {
	remap   map[int]int // source group -> core group
	buf     strings.Builder
	asserts []*node
	nCore   int
}

// emitAlt emits an alternation body.
func (em *emitter) emitAlt(alts []seq) {
	for i, sq := range alts {
		if i > 0 {
			em.buf.WriteByte('|')
		}
		em.emitSeq(sq)
	}
}

// emitSeq emits one concatenation.
func (em *emitter) emitSeq(sq seq) {
	for _, n := range sq {
		em.emitNode(n)
	}
}

// emitNode emits a single node, assigning core group indices in emission
// order: synthetic retry-anchor wraps, source capture groups, and assertion
// position markers.
func (em *emitter) emitNode(n *node) {
	if n.needsWrap {
		em.nCore++
		n.anchorGroup = em.nCore
		em.buf.WriteByte('(')
		em.emitPlain(n)
		em.buf.WriteByte(')')
		return
	}
	em.emitPlain(n)
}

// emitPlain emits a node without a synthetic wrap.
func (em *emitter) emitPlain(n *node) {
	switch n.kind {
	case nAtom, nAnchor:
		em.buf.WriteString(n.text)
		em.buf.WriteString(n.quant)
	case nGroup:
		em.emitGroup(n)
	case nAssert:
		em.nCore++
		n.coreGroup = em.nCore
		em.buf.WriteString("()")
		em.asserts = append(em.asserts, n)
	}
}

// emitGroup emits a group node with its original prefix and quantifier.
func (em *emitter) emitGroup(n *node) {
	if n.capturing {
		em.nCore++
		n.coreGroup = em.nCore
		em.remap[n.srcIndex] = em.nCore
	}
	em.buf.WriteString(n.prefix)
	em.emitAlt(n.body)
	em.buf.WriteByte(')')
	em.buf.WriteString(n.quant)
}

// --- compile driver ---

// CompilePCRE compiles a case-insensitive PCRE pattern into an RE2 core
// with positional lookaround assertions. Malformed patterns and PCRE
// constructs the layer cannot express return a typed *CompileError. See the
// package documentation for the supported-construct table and the layer's
// documented divergence contracts.
func CompilePCRE(pat string) (*Pattern, error) {
	ps := &parser{src: pat}
	alts, err := ps.parseAlt(0)
	if err != nil {
		return nil, err
	}

	// Analyze all branches first (rule 2 rejections fire before any core
	// compile), resolving assertion inner-capture numbering along the way.
	for _, sq := range alts {
		if _, _, _, _, err := analyzeSeq(sq, pat); err != nil {
			return nil, err
		}
	}
	assignAssertInnerGroups(alts, &ps.nSrc)

	p := &Pattern{original: pat, nSrcGroups: ps.nSrc}
	for _, sq := range alts {
		b, err := compileBranch(sq, ps.nSrc, pat)
		if err != nil {
			return nil, err
		}
		p.branches = append(p.branches, b)
	}
	return p, nil
}

// assignAssertInnerGroups numbers capture groups that appear textually
// inside lookaround assertions. They occupy source-numbering slots (as in
// .NET) but never participate: the emulation evaluates assertions as
// standalone anchored regexes and discards their captures (class (k)).
// Numbering must interleave in source-text order with regular groups, so
// the tree walk mirrors the parser's left-to-right scan, shifting every
// already-assigned source index that follows an assertion's inner groups.
func assignAssertInnerGroups(alts []seq, nSrc *int) {
	// Walk in source order collecting nodes; assertion inner captures
	// insert numbering slots, shifting subsequent capturing groups.
	var walk func(alts []seq, shift int) int
	walk = func(alts []seq, shift int) int {
		for _, sq := range alts {
			for _, n := range sq {
				switch n.kind {
				case nGroup:
					if n.capturing {
						n.srcIndex += shift
					}
					shift = walk(n.body, shift)
				case nAssert:
					shift += countCapturesIn(n.inner)
				case nAtom, nAnchor:
					// no capture bookkeeping
				}
			}
		}
		return shift
	}
	total := walk(alts, 0)
	*nSrc += total
}

// compileBranch emits and compiles one top-level branch.
func compileBranch(sq seq, nSrcGroups int, src string) (*branchPattern, error) {
	em := &emitter{remap: make(map[int]int)}
	em.emitSeq(sq)
	core := em.buf.String()

	re, err := regexp.Compile("(?i)" + core)
	if err != nil {
		return nil, compileErr(core, -1, fmt.Sprintf("RE2 core rejected: %v", err))
	}

	b := &branchPattern{re: re, remap: make([]int, nSrcGroups+1)}
	for i := range b.remap {
		b.remap[i] = -1
	}
	b.remap[0] = 0
	for src, coreIdx := range em.remap {
		if src <= nSrcGroups {
			b.remap[src] = coreIdx
		}
	}

	for _, an := range em.asserts {
		var anchored string
		if an.ahead {
			anchored = "^(?:" + an.inner + ")"
		} else {
			anchored = "(?:" + an.inner + ")$"
		}
		are, err := regexp.Compile("(?i)" + anchored)
		if err != nil {
			return nil, compileErr(an.inner, an.srcOff, fmt.Sprintf("assertion rejected by RE2: %v", err))
		}
		a := assertion{re: are, positive: an.positive, ahead: an.ahead, marker: an.coreGroup}
		a.window, a.monotoneDot = analyzeAssertInner(an.inner, an.ahead)
		if len(an.retryChain) > 0 {
			a.retry = buildRetryPlan(an, src)
		}
		b.assertions = append(b.assertions, a)
	}
	return b, nil
}

// analyzeAssertInner derives two per-assertion evaluation optimizations
// from the inner pattern's syntax tree. Both are soundness-preserving:
// analysis failure or an unanalyzable shape falls back to the general
// evaluation path.
//
// window (lookbehinds only): when the inner pattern has a bounded maximum
// match width and references no text anchors (^ $ \A \z), the end-anchored
// lookbehind scan only needs a window of that many bytes before the marker
// (plus slack so a \b at a match edge still sees its neighbor byte),
// instead of the entire prefix. -1 means no window (scan the full prefix).
//
// monotoneDot (lookaheads only): an inner pattern of shape `.+?X`/`.*?X`
// (an unbounded dot prefix) that MATCHES at offset p also matches at every
// p' < p on newline-free input, so matchability is a threshold predicate
// the evaluator resolves with one binary search per call (branchEval) —
// the flagship release-group lookahead shape.
func analyzeAssertInner(inner string, ahead bool) (window int, monotoneDot bool) {
	window = -1
	tree, err := syntax.Parse("(?i)(?:"+inner+")", syntax.Perl)
	if err != nil {
		return -1, false
	}
	if !ahead {
		if w := maxWidthSyntax(tree); w >= 0 && !hasTextAnchor(tree) {
			window = w + utf8.UTFMax + 1 // slack: \b at a match edge
		}
		return window, false
	}
	return window, hasUnboundedDotPrefix(tree)
}

// maxWidthSyntax computes the maximum match width in bytes of a parsed
// regex, or -1 when unbounded. Conservative per-rune width (utf8.UTFMax)
// keeps windows sound for non-ASCII classes.
func maxWidthSyntax(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		return len(string(re.Rune))
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return utf8.UTFMax
	case syntax.OpCapture, syntax.OpQuest:
		return maxWidthSyntax(re.Sub[0])
	case syntax.OpStar, syntax.OpPlus:
		if w := maxWidthSyntax(re.Sub[0]); w == 0 {
			return 0
		}
		return -1
	case syntax.OpRepeat:
		return maxWidthRepeat(re)
	case syntax.OpConcat:
		return maxWidthSum(re.Sub)
	case syntax.OpAlternate:
		return maxWidthMax(re.Sub)
	case syntax.OpEmptyMatch, syntax.OpNoMatch,
		syntax.OpBeginText, syntax.OpEndText, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return 0
	default:
		return -1
	}
}

// maxWidthRepeat handles OpRepeat ({m,n} and {m,}).
func maxWidthRepeat(re *syntax.Regexp) int {
	w := maxWidthSyntax(re.Sub[0])
	if w == 0 {
		return 0
	}
	if w < 0 || re.Max < 0 {
		return -1
	}
	return re.Max * w
}

// maxWidthSum totals concatenation widths (-1 when any part is unbounded).
func maxWidthSum(subs []*syntax.Regexp) int {
	total := 0
	for _, sub := range subs {
		w := maxWidthSyntax(sub)
		if w < 0 {
			return -1
		}
		total += w
	}
	return total
}

// maxWidthMax takes the widest alternative (-1 when any is unbounded).
func maxWidthMax(subs []*syntax.Regexp) int {
	maxW := 0
	for _, sub := range subs {
		w := maxWidthSyntax(sub)
		if w < 0 {
			return -1
		}
		maxW = max(maxW, w)
	}
	return maxW
}

// hasTextAnchor reports whether the parsed regex references a line or text
// boundary anywhere (which windowed evaluation would misplace).
func hasTextAnchor(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginText, syntax.OpEndText, syntax.OpBeginLine, syntax.OpEndLine:
		return true
	default:
		return slices.ContainsFunc(re.Sub, hasTextAnchor)
	}
}

// hasUnboundedDotPrefix reports whether the parsed regex is a concatenation
// beginning with `.+` or `.*` (greedy or lazy; `.` with or without dotall).
// The newline caveat is handled at retry time by the caller.
func hasUnboundedDotPrefix(re *syntax.Regexp) bool {
	for re.Op == syntax.OpCapture {
		re = re.Sub[0]
	}
	if re.Op == syntax.OpConcat {
		if len(re.Sub) == 0 {
			return false
		}
		re = re.Sub[0]
		for re.Op == syntax.OpCapture {
			re = re.Sub[0]
		}
	}
	if re.Op != syntax.OpStar && re.Op != syntax.OpPlus {
		return false
	}
	sub := re.Sub[0]
	return sub.Op == syntax.OpAnyChar || sub.Op == syntax.OpAnyCharNotNL
}

// buildRetryPlan converts an assertion's resolved shrink chain into the
// runtime plan: anchors with suffix minimum widths, the width-legality
// regex (the chain's own source, both-ends anchored — a probed width is
// only acceptable if the chain could actually produce it), the same-level
// continuation check regexes (.NET re-matches the pattern remainder from a
// shrunken offset; a prefix match of the remainder is the bounded
// analogue), and the capture groups to clamp on acceptance.
func buildRetryPlan(an *node, src string) *retryPlan {
	chain := an.retryChain
	plan := &retryPlan{anchors: make([]retryAnchor, len(chain))}
	suffix := 0
	for i, ce := range slices.Backward(chain) {
		suffix += ce.minW
		plan.anchors[i] = retryAnchor{group: ce.anchorGroup, minSuffix: suffix}
	}

	// Width legality: `^(?:<chain source>)$` (the source slice keeps any
	// zero-width constraints between chain elements). Compile failure
	// (impossible for rule-2-clean chains) just disables the check.
	chainSrc := src[chain[0].srcOff:chain[len(chain)-1].srcEnd]
	if re, err := regexp.Compile("(?i)^(?:" + chainSrc + ")$"); err == nil {
		plan.legalRe = re
	}

	// Same-level continuation: only when assertion- and top-anchor-free
	// (a lookaround inside the remainder cannot compile raw; a ^/\A would
	// be misplaced on a suffix subject). contMid carries one consumed rune
	// of left context so a leading \b in the remainder sees its real
	// neighbor; contAt0 covers offset 0.
	if an.contOK && an.contSrc != "" && !hasTopLevelStartAnchor(an.contSrc) {
		at0, err0 := regexp.Compile("(?i)^(?:" + an.contSrc + ")")
		mid, err1 := regexp.Compile("(?i)(?s)^.(?:" + an.contSrc + ")")
		if err0 == nil && err1 == nil {
			plan.contAt0, plan.contMid = at0, mid
		}
	}

	// Capture clamping: enclosing groups the chain was adopted through
	// (their greedy end coincides with the assertion position) and capture
	// groups inside the chain span (dropped or truncated by a shrink).
	for _, pn := range an.retryPath {
		if pn.capturing {
			plan.pathGroups = append(plan.pathGroups, pn.coreGroup)
		}
	}
	for _, ce := range chain {
		collectCaptureGroups(ce, &plan.chainGroups)
	}
	return plan
}

// collectCaptureGroups appends the core indices of all capturing groups in
// a node subtree.
func collectCaptureGroups(n *node, out *[]int) {
	if n.kind == nGroup && n.capturing {
		*out = append(*out, n.coreGroup)
	}
	for _, sq := range n.body {
		for _, sub := range sq {
			collectCaptureGroups(sub, out)
		}
	}
}

// hasTopLevelStartAnchor scans raw regex text for ^ or \A outside
// character classes (constructs a suffix-subject continuation check would
// misplace).
func hasTopLevelStartAnchor(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) && s[i+1] == 'A' {
				return true
			}
			i++
		case '[':
			i = skipCharClass(s, i)
		case '^':
			return true
		}
	}
	return false
}
