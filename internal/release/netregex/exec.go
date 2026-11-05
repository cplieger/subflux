package netregex

import (
	"regexp/syntax"
	"unicode"
	"unicode/utf8"
)

// input is one call's state over one string: the predicates' memos and a
// machine per program, kept in the Regexp's pool between calls.
type input struct {
	re     *Regexp
	s      string
	ahead  [][]int8 // lookahead k at each position: 0 unknown, 1 holds, 2 fails
	behind [][]bool // lookbehind k: whether a body match ends at each position
	ms     []*machine
	steps  int // queue insertions, the unit of the package doc's bound
}

func (re *Regexp) get(s string) *input {
	in, _ := re.pool.Get().(*input)
	if in == nil {
		in = &input{re: re, ahead: make([][]int8, len(re.preds)), behind: make([][]bool, len(re.preds)), ms: make([]*machine, len(re.preds)+1)}
	}
	in.s, in.steps = s, 0
	for k := range re.preds {
		in.ahead[k], in.behind[k] = in.ahead[k][:0], in.behind[k][:0]
	}
	return in
}

func (re *Regexp) put(in *input) {
	in.s = ""
	re.pool.Put(in)
}

// machine i runs the pattern (0) or predicate i-1's body.
func (in *input) machine(i int) *machine {
	if m := in.ms[i]; m != nil {
		return m
	}
	p := in.re.main
	if i > 0 {
		p = in.re.preds[i-1].p
	}
	m := &machine{p: p, q0: newQueue(len(p.inst)), q1: newQueue(len(p.inst)), cap: make([]int, p.ncap), matchcap: make([]int, p.ncap)}
	in.ms[i] = m
	return m
}

func (in *input) find(from int) []int {
	m := in.machine(0)
	if !m.run(in, from, modeFind, nil) {
		return nil
	}
	return append([]int(nil), m.matchcap[:2*len(in.re.names)]...)
}

func (in *input) findAll() [][]int {
	var all [][]int
	for pos := 0; pos <= len(in.s); {
		m := in.find(pos)
		if m == nil {
			break
		}
		all = append(all, m)
		pos = m[1]
		if m[1] == m[0] {
			if pos == len(in.s) {
				break
			}
			_, w := utf8.DecodeRuneInString(in.s[pos:])
			pos += w
		}
	}
	return all
}

// assert evaluates an InstEmptyWidth at pos. Multiline is never on, so ^ is
// the text start and $ the end or before a final \n.
func (in *input) assert(pos int, op syntax.EmptyOp) bool {
	s := in.s
	switch {
	case op&(syntax.EmptyBeginLine|syntax.EmptyBeginText) != 0 && pos != 0,
		op&syntax.EmptyEndText != 0 && pos != len(s),
		op&syntax.EmptyEndLine != 0 && pos != len(s) && (pos != len(s)-1 || s[pos] != '\n'):
		return false
	}
	if op&(syntax.EmptyWordBoundary|syntax.EmptyNoWordBoundary) != 0 {
		before, after := false, false
		if pos > 0 {
			r, _ := utf8.DecodeLastRuneInString(s[:pos])
			before = IsWordChar(r)
		}
		if pos < len(s) {
			r, _ := utf8.DecodeRuneInString(s[pos:])
			after = IsWordChar(r)
		}
		if (before != after) != (op&syntax.EmptyWordBoundary != 0) {
			return false
		}
	}
	return true
}

// IsWordChar reports whether r is a .NET word character: what \w matches
// and \b and \B test on each side.
func IsWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) || r == 0x200C || r == 0x200D
}

// holds evaluates lookaround k at pos, each answer computed once per call.
func (in *input) holds(k, pos int) bool {
	p := &in.re.preds[k]
	n := len(in.s) + 1
	if p.behind {
		ends := in.behind[k]
		if len(ends) == 0 {
			ends = grow(in.behind[k], n)
			in.behind[k] = ends
			in.machine(k+1).run(in, 0, modeEnds, ends)
		}
		return ends[pos] != p.negate
	}
	memo := in.ahead[k]
	if len(memo) == 0 {
		memo = grow(in.ahead[k], n)
		in.ahead[k] = memo
	}
	if memo[pos] == 0 {
		memo[pos] = 2
		if in.machine(k+1).run(in, pos, modeAt, nil) {
			memo[pos] = 1
		}
	}
	return (memo[pos] == 1) != p.negate
}

// grow returns b cleared at length n.
func grow[T any](b []T, n int) []T {
	if cap(b) < n {
		return make([]T, n)
	}
	b = b[:n]
	clear(b)
	return b
}

type mode uint8

const (
	modeFind mode = iota // the leftmost match, by .NET's priority, with its groups
	modeAny              // whether any match starts at or after from
	modeAt               // whether a match starts at from
	modeEnds             // every position a match ends at, into ends
)

type machine struct {
	p        *prog
	q0, q1   queue
	free     []*thread
	cap      []int // a new start's captures while add runs
	matchcap []int
}

type queue struct {
	sparse []int
	dense  []entry
}

type entry struct {
	t  *thread
	pc int
}

type thread struct{ cap []int }

func newQueue(n int) queue { return queue{sparse: make([]int, n), dense: make([]entry, 0, n)} }

// alloc takes a thread from the free list, which step refills.
func (m *machine) alloc() *thread {
	if n := len(m.free); n > 0 {
		t := m.free[n-1]
		m.free = m.free[:n-1]
		return t
	}
	return &thread{cap: make([]int, m.p.ncap)}
}

// run scans in.s from byte from. A new start joins each position's queue
// last, below every thread already running: the leftmost start wins, and
// within it the queue order is the backtracking order.
func (m *machine) run(in *input, from int, md mode, ends []bool) bool {
	runq, nextq := &m.q0, &m.q1
	defer m.release(runq)
	defer m.release(nextq)
	found := false
	for pos := from; ; {
		c, w := rune(-1), 0
		if pos < len(in.s) {
			c, w = utf8.DecodeRuneInString(in.s[pos:])
		}
		if !found && (md != modeAt || pos == from) {
			m.seed(in, runq, pos)
		}
		found = m.step(in, runq, nextq, pos, pos+w, c, md, ends) || found
		runq, nextq = nextq, runq
		switch {
		case found && md != modeFind, w == 0:
			return found
		case len(runq.dense) == 0 && (found || md == modeAt):
			return found
		}
		pos += w
	}
}

// seed adds a thread starting at pos, its captures unset but the match's.
func (m *machine) seed(in *input, q *queue, pos int) {
	for i := range m.cap {
		m.cap[i] = -1
	}
	if len(m.cap) > 0 {
		m.cap[0] = pos
	}
	m.add(in, q, m.p.start, pos, m.cap)
}

func (m *machine) release(q *queue) {
	for _, d := range q.dense {
		if d.t != nil {
			m.free = append(m.free, d.t)
		}
	}
	q.dense = q.dense[:0]
}

func (m *machine) add(in *input, q *queue, pc, pos int, caps []int) {
	if j := q.sparse[pc]; j < len(q.dense) && q.dense[j].pc == pc {
		return
	}
	in.steps++
	j := len(q.dense)
	q.dense = q.dense[:j+1]
	q.dense[j] = entry{pc: pc}
	q.sparse[pc] = j
	i := &m.p.inst[pc]
	switch i.Op {
	case syntax.InstFail:
	case syntax.InstAlt, syntax.InstAltMatch:
		m.add(in, q, int(i.Out), pos, caps)
		m.add(in, q, int(i.Arg), pos, caps)
	case syntax.InstEmptyWidth:
		if in.assert(pos, syntax.EmptyOp(i.Arg)) { //nolint:gosec // G115: an InstEmptyWidth's Arg is the EmptyOp syntax.Compile wrote
			m.add(in, q, int(i.Out), pos, caps)
		}
	case syntax.InstNop:
		m.add(in, q, int(i.Out), pos, caps)
	case syntax.InstCapture:
		m.capture(in, q, i, pos, caps)
	default:
		t := m.alloc()
		copy(t.cap, caps)
		q.dense[j].t = t
	}
}

// capture runs a placeholder's lookaround, or records a group occurrence's
// start or commits its span for the threads add reaches through it.
func (m *machine) capture(in *input, q *queue, i *syntax.Inst, pos int, caps []int) {
	if k := m.p.pred[i.Arg]; k >= 0 {
		if in.holds(k, pos) {
			m.add(in, q, int(i.Out), pos, caps)
		}
		return
	}
	a, g := m.p.scratch[i.Arg], m.p.group[i.Arg]
	switch {
	case g >= 0:
		start, end := caps[g], caps[g+1]
		caps[g], caps[g+1] = caps[a], pos
		m.add(in, q, int(i.Out), pos, caps)
		caps[g], caps[g+1] = start, end
	case a >= 0:
		start := caps[a]
		caps[a] = pos
		m.add(in, q, int(i.Out), pos, caps)
		caps[a] = start
	default:
		m.add(in, q, int(i.Out), pos, caps)
	}
}

// step moves runq's threads over rune c at pos into nextq at next and
// reports a match. Outside modeEnds the first match ends the threads below
// it, which the backtracking order would never reach.
func (m *machine) step(in *input, runq, nextq *queue, pos, next int, c rune, md mode, ends []bool) bool {
	defer m.release(runq)
	for j := range runq.dense {
		d := &runq.dense[j]
		t := d.t
		if t == nil {
			continue
		}
		d.t = nil
		i := &m.p.inst[d.pc]
		ok := false
		switch i.Op {
		case syntax.InstMatch:
			m.free = append(m.free, t)
			if md == modeEnds {
				ends[pos] = true
				continue
			}
			if md == modeFind {
				copy(m.matchcap, t.cap)
				m.matchcap[1] = pos
			}
			return true
		case syntax.InstRune:
			ok = c >= 0 && i.MatchRune(c)
		case syntax.InstRune1:
			ok = c == i.Rune[0]
		case syntax.InstRuneAny:
			ok = c >= 0
		case syntax.InstRuneAnyNotNL:
			ok = c >= 0 && c != '\n'
		}
		if ok {
			m.add(in, nextq, int(i.Out), next, t.cap)
		}
		// Freed only now: add may write t.cap while it runs.
		m.free = append(m.free, t)
	}
	return false
}
