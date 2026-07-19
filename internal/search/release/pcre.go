// Package release provides release name parsing via PCRE-compatible regex
// patterns sourced from TRaSH Guides and Sonarr/Radarr QualityParser.
//
// pcre.go implements the match-time half of a PCRE-to-RE2 compatibility
// layer (the compile half lives in pcre_compile.go). Go's regexp (RE2) does
// not support lookarounds; this layer compiles the RE2-compatible core of a
// PCRE pattern with an empty marker group at each lookaround site and
// verifies the assertions programmatically at their actual in-pattern
// offsets after each core match.
//
// # Supported constructs
//
// Everything Go regexp (RE2) accepts, plus lookahead (?=...) (?!...) and
// lookbehind (?<=...) (?<!...) assertions and .NET-style named groups
// (?<name>...) (exposed by number only). Rejected with a typed
// *CompileError: backreferences (\1, \k<name>, (?P=name)), atomic groups
// (?>...), conditionals (?(...)...), inline comments (?#...), possessive
// quantifiers (a*+), quantified assertions ((?!x)*), assertions nested
// inside other assertions, and assertions inside quantified groups (Go
// regexp reports only the last iteration's marker offset, so per-iteration
// verification is impossible; silent under-checking is forbidden).
//
// # Semantics and documented divergence contracts
//
// The layer targets the observable behavior of .NET's backtracking Regex
// (the patterns' native engine, pinned upstream revisions in the spec's
// study appendix), with these documented divergences:
//
//   - Core syntax runs under RE2 semantics (divergence class (i), accepted
//     contract): \d \w \s and \b are ASCII-only vs .NET's Unicode classes;
//     $ matches only at end of text (.NET also before a final \n); case
//     folding is Unicode simple folding vs .NET culture-sensitive folding.
//   - Capture participation (class (k), accepted contract): the []string
//     API cannot distinguish a participating-empty group from an unmatched
//     one (both are ""), and captures inside lookaround assertions never
//     participate (the assertion runs as a standalone anchored regex).
//   - Backtracking retry (class (j), bounded default): when an assertion
//     adjacent to a quantified subexpression fails at the greedy end, the
//     layer re-runs the anchored assertion at each byte offset down to the
//     chain's minimum width (bounded shrink-and-recheck). The candidate is
//     accepted or rejected on that basis; reported capture spans stay at
//     the RE2 greedy widths (a full .NET engine would also re-derive the
//     shrunken captures and re-match the pattern tail).
//   - A source pattern with top-level alternation is compiled per branch;
//     FindStringSubmatch merges branch candidates with a single
//     left-to-right non-overlapping positional scan (earliest start wins,
//     ties broken by source branch order, cursor advances past each
//     selection) and returns the LAST selection, matching .NET consumers'
//     .Last()/.LastOrDefault() usage on a single Matches() scan.
//
// # Input bound (measured linear-time gate)
//
// Assertion verification scans O(n) text per candidate and the shrink
// retry adds a group-length-bounded loop, so per-call work is not covered
// by RE2's linear-time guarantee alone. Callers MUST cap untrusted input
// at MaxNameLen bytes. The cap is enforced at the provider boundary: the
// download-retry provider wrapper (internal/provider.WrapRetry), which
// both composition roots apply to every provider, clamps each search
// result's ReleaseName via ClampName before it enters the engine.
// ParseReleaseName additionally clamps its own input (defense in depth for
// callers that bypass the provider path). Scaling benchmarks in
// pcre_bench_test.go demonstrate near-linear behavior within the bound.
package release

import (
	"regexp"
	"slices"
	"strings"
)

// MaxNameLen is the maximum release-name length in bytes the layer is
// specified for. Provider-supplied names are truncated to this bound at
// the provider boundary (the search engine's result collector) before any
// pattern in this package sees them. 512 bytes comfortably covers real
// release names (filesystem basenames cap at 255 bytes) while bounding the
// assertion-check and shrink-retry work per candidate.
const MaxNameLen = 512

// ClampName truncates a release name to MaxNameLen bytes. It is the
// documented enforcement helper for the layer's input bound.
func ClampName(s string) string {
	if len(s) > MaxNameLen {
		return s[:MaxNameLen]
	}
	return s
}

// retryAnchor locates one element of a shrink-retry chain at match time:
// the synthetic core group wrapping the quantified element, and the summed
// minimum width of the chain from this element to the assertion site.
type retryAnchor struct {
	group     int // core group index of the synthetic anchor wrap
	minSuffix int // minimum bytes the chain must keep from this anchor on
}

// retryPlan is the bounded shrink-and-recheck plan for one assertion
// adjacent to a run of quantified elements (leftmost anchor first).
type retryPlan struct {
	// legalRe is the chain's own source anchored both ends: a probed
	// width is acceptable only if the chain could produce it (skips
	// widths .NET's structured quantifier backtracking never visits).
	legalRe *regexp.Regexp
	// contAt0/contMid verify the same-level pattern remainder at the
	// probed offset (.NET re-matches the rest of the pattern after a
	// shrink; the prefix match is the bounded analogue). contMid consumes
	// one rune of left context so a leading \b sees its real neighbor.
	// Nil when the remainder is empty or unanalyzable (assertion-bearing).
	contAt0 *regexp.Regexp
	contMid *regexp.Regexp
	anchors []retryAnchor
	// pathGroups are enclosing capture groups whose greedy end coincides
	// with the assertion position; chainGroups are capture groups inside
	// the chain span. Both are clamped when a shrink is accepted.
	pathGroups  []int
	chainGroups []int
}

// assertion is a compiled lookaround with its positional marker.
type assertion struct {
	re     *regexp.Regexp
	retry  *retryPlan // non-nil when quantifier-adjacent
	marker int        // core group index of the empty position marker
	// window bounds the lookbehind subject to the inner pattern's maximum
	// match width (plus slack); -1 scans the whole prefix. See
	// analyzeAssertInner.
	window   int
	positive bool // true = must match, false = must not match
	ahead    bool // true = lookahead, false = lookbehind
	// monotoneDot marks a lookahead whose inner pattern starts with an
	// unbounded dot prefix (`.+?X`, `.*X`): on newline-free input its
	// matchability is monotone non-increasing in the evaluation offset,
	// so a per-call threshold answers every check in O(1). See branchEval.
	monotoneDot bool
}

// branchPattern is one compiled top-level alternation branch: the RE2 core
// (with markers and synthetic anchors), its assertions, and the
// source→core capture-group remap.
type branchPattern struct {
	re         *regexp.Regexp
	assertions []assertion
	// remap[srcGroup] = core group index, or -1 when the source group
	// lives in another branch (or inside an assertion, class (k)).
	remap []int
}

// Pattern wraps one or more RE2-backed branches compiled from a PCRE
// pattern. The zero value is not usable; construct via CompilePCRE.
type Pattern struct {
	original   string
	branches   []*branchPattern
	nSrcGroups int
}

// String returns the original source pattern.
func (p *Pattern) String() string { return p.original }

// MatchString reports whether s contains a match satisfying all
// assertions of at least one branch.
func (p *Pattern) MatchString(s string) bool {
	for _, b := range p.branches {
		if len(b.assertions) == 0 {
			if b.re.MatchString(s) {
				return true
			}
			continue
		}
		ev := newBranchEval(b, s)
		if slices.ContainsFunc(b.re.FindAllStringSubmatchIndex(s, -1), ev.checkAssertions) {
			return true
		}
	}
	return false
}

// branchState tracks one branch's surviving-candidate stream during the
// positional merge scan.
type branchState struct {
	cands [][]int
	next  int
}

// FindStringSubmatch returns the LAST match of the positional merge scan
// (see the package documentation) with submatches in SOURCE pattern
// numbering, or nil when no candidate survives its assertions.
func (p *Pattern) FindStringSubmatch(s string) []string {
	states := make([]branchState, len(p.branches))
	found := false
	for i, b := range p.branches {
		states[i].cands = b.survivingCandidates(s)
		found = found || len(states[i].cands) > 0
	}
	if !found {
		return nil
	}
	lastBranch, lastCand := mergeScan(states)
	return p.branches[lastBranch].sourceSubmatch(s, lastCand, p.nSrcGroups)
}

// mergeScan runs the normative single left-to-right non-overlapping scan
// over all branches' candidates: earliest start wins, ties broken by
// source branch order, the cursor advances past each selection (bumping by
// one on a zero-length match). Returns the LAST selection.
func mergeScan(states []branchState) (lastBranch int, lastCand []int) {
	lastBranch = -1
	cursor := 0
	for {
		selBranch, sel := selectEarliest(states, cursor)
		if selBranch < 0 {
			return lastBranch, lastCand
		}
		lastCand, lastBranch = sel, selBranch
		if sel[1] > cursor {
			cursor = sel[1]
		} else {
			cursor++ // zero-length selection: bump along
		}
		states[selBranch].next++
	}
}

// selectEarliest picks the earliest-starting candidate at or after the
// cursor across all branch streams (tie: lowest branch index), advancing
// each stream past candidates the cursor has already passed.
func selectEarliest(states []branchState, cursor int) (branch int, cand []int) {
	branch = -1
	for i := range states {
		st := &states[i]
		for st.next < len(st.cands) && st.cands[st.next][0] < cursor {
			st.next++
		}
		if st.next == len(st.cands) {
			continue
		}
		if c := st.cands[st.next]; branch == -1 || c[0] < cand[0] {
			branch, cand = i, c
		}
	}
	return branch, cand
}

// survivingCandidates returns the branch's non-overlapping core matches
// that satisfy all assertions (after bounded shrink-and-recheck retry),
// in ascending position order.
func (b *branchPattern) survivingCandidates(s string) [][]int {
	all := b.re.FindAllStringSubmatchIndex(s, -1)
	if len(b.assertions) == 0 {
		return all
	}
	ev := newBranchEval(b, s)
	surviving := all[:0]
	for _, cand := range all {
		if ev.checkAssertions(cand) {
			surviving = append(surviving, cand)
		}
	}
	return surviving
}

// branchEval evaluates one branch's assertions against one subject string,
// memoizing per-call state for monotone-dot lookaheads: on newline-free
// input, `.+?X`-shaped inner patterns match at offset p iff p is at most a
// threshold, which one binary search per (assertion, call) discovers. Each
// subsequent check is then O(1) instead of an O(n) regex scan.
type branchEval struct {
	b *branchPattern
	s string
	// thresholds[i] is the largest offset at which assertion i's inner
	// pattern still matches (-1: matches nowhere; unset: not yet computed).
	thresholds  []int
	computed    []bool
	monotoneOK  bool
	hasMonotone bool
}

// newBranchEval builds the per-call evaluation state.
func newBranchEval(b *branchPattern, s string) *branchEval {
	ev := &branchEval{b: b, s: s}
	ev.hasMonotone = slices.ContainsFunc(b.assertions, func(a assertion) bool { return a.monotoneDot })
	if ev.hasMonotone {
		// The monotone threshold argument needs newline-free input (a `.`
		// prefix cannot absorb '\n'); release names virtually never
		// contain one, and the general path stays correct when they do.
		ev.monotoneOK = strings.IndexByte(s, '\n') < 0
		if ev.monotoneOK {
			ev.thresholds = make([]int, len(b.assertions))
			ev.computed = make([]bool, len(b.assertions))
		}
	}
	return ev
}

// checkAssertions verifies every assertion of a candidate match at its
// marker offset, applying the three normative marker rules:
//
//  1. a marker with index -1 (non-participating alternation branch) is
//     SKIPPED — the assertion belongs to a branch that did not match;
//  2. assertions inside quantified groups do not exist at match time
//     (rejected at compile time);
//  3. marker positions come from the core match's submatch index slice.
func (ev *branchEval) checkAssertions(cand []int) bool {
	for i := range ev.b.assertions {
		a := &ev.b.assertions[i]
		pos := cand[2*a.marker]
		if pos < 0 {
			continue // rule 1: non-participating branch
		}
		if ev.holdsAt(i, a, pos) {
			continue
		}
		if a.retry == nil || !ev.retryShrink(a, i, cand, pos) {
			return false
		}
	}
	return true
}

// holdsAt evaluates assertion i at byte offset pos.
func (ev *branchEval) holdsAt(i int, a *assertion, pos int) bool {
	if a.monotoneDot && ev.monotoneOK {
		return a.positive == (pos <= ev.threshold(i, a))
	}
	return a.holdsAt(ev.s, pos)
}

// threshold lazily computes the largest offset at which assertion i's
// inner pattern matches. Monotonicity (newline-free input, unbounded dot
// prefix) makes binary search valid: inner matches at p implies it matches
// at every p' < p.
func (ev *branchEval) threshold(i int, a *assertion) int {
	if ev.computed[i] {
		return ev.thresholds[i]
	}
	lo, hi, ans := 0, len(ev.s), -1
	for lo <= hi {
		mid := (lo + hi) / 2
		if a.re.MatchString(ev.s[mid:]) {
			ans = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	ev.thresholds[i] = ans
	ev.computed[i] = true
	return ans
}

// holdsAt evaluates the assertion at byte offset pos without per-call
// memoization. Lookaheads run the ^-anchored inner regex on the suffix (a
// single anchored attempt); bounded lookbehinds run the $-anchored inner
// regex on a maximum-width window before pos instead of the entire prefix.
func (a *assertion) holdsAt(s string, pos int) bool {
	var subject string
	switch {
	case a.ahead:
		subject = s[pos:]
	case a.window >= 0 && pos > a.window:
		subject = s[pos-a.window : pos]
	default:
		subject = s[:pos]
	}
	return a.positive == a.re.MatchString(subject)
}

// retryShrink is the bounded shrink-and-recheck (design "backtracking-gap
// resolution"): when an assertion adjacent to quantified elements fails at
// the greedy end, probe one byte earlier at a time down to the chain's
// minimum width. A probed offset is accepted when (1) the chain can
// legally produce the clamped width, (2) the assertion holds there, and
// (3) the same-level pattern remainder still matches at that offset. On
// acceptance the affected capture-group spans are clamped to the offset
// (mirroring the captures .NET's backtracking would report); the overall
// match span m[0] intentionally keeps its greedy extent (documented
// bounded approximation, class (j)).
func (ev *branchEval) retryShrink(a *assertion, i int, cand []int, pos int) bool {
	chainStart, floor := shrinkBounds(a.retry, cand)
	if floor < 0 {
		return false // no chain element participated; nothing to shrink
	}
	monotone := a.monotoneDot && ev.monotoneOK
	if monotone && !a.positive {
		// A failed negative dot-prefix lookahead stays failed at every
		// smaller offset: no width can satisfy it.
		return false
	}
	start := pos - 1
	if monotone {
		// Positive dot-prefix lookahead: holds iff the offset clears the
		// threshold, so larger offsets are skipped wholesale.
		t := ev.threshold(i, a)
		if t < floor {
			return false
		}
		start = min(start, t)
	}
	for off := start; off >= floor; off-- {
		if !monotone && !ev.holdsAt(i, a, off) {
			continue
		}
		if ev.acceptShrinkAt(a.retry, chainStart, off) {
			clampCaptures(a.retry, cand, pos, off)
			return true
		}
	}
	return false
}

// shrinkBounds resolves the chain's runtime start and the lowest probe
// offset from the first participating anchor. floor -1 means no chain
// element participated.
func shrinkBounds(plan *retryPlan, cand []int) (chainStart, floor int) {
	for _, an := range plan.anchors {
		if start := cand[2*an.group]; start >= 0 {
			return start, start + an.minSuffix
		}
	}
	return -1, -1
}

// acceptShrinkAt applies the width-legality and continuation checks for a
// probed shrink offset whose assertion already holds.
func (ev *branchEval) acceptShrinkAt(plan *retryPlan, chainStart, off int) bool {
	if plan.legalRe != nil && !plan.legalRe.MatchString(ev.s[chainStart:off]) {
		return false
	}
	return ev.continuationHolds(plan, off)
}

// continuationHolds verifies the same-level pattern remainder at offset
// off (true when the plan carries no continuation check).
func (ev *branchEval) continuationHolds(plan *retryPlan, off int) bool {
	if plan.contAt0 == nil {
		return true
	}
	// One rune of left context keeps a leading \b truthful; multi-byte or
	// mid-rune left context falls back to the offset-0 form (documented
	// slice-boundary approximation, ASCII release names unaffected).
	if off > 0 && ev.s[off-1] < 0x80 {
		return plan.contMid.MatchString(ev.s[off-1:])
	}
	return plan.contAt0.MatchString(ev.s[off:])
}

// clampCaptures rewrites capture spans after an accepted shrink: enclosing
// groups that ended at the greedy assertion position now end at the
// accepted offset; chain-interior groups past the offset are dropped and
// ones straddling it are truncated.
func clampCaptures(plan *retryPlan, cand []int, pos, off int) {
	for _, g := range plan.pathGroups {
		if 2*g+1 < len(cand) && cand[2*g+1] == pos {
			cand[2*g+1] = off
		}
	}
	for _, g := range plan.chainGroups {
		if 2*g+1 >= len(cand) || cand[2*g] < 0 {
			continue
		}
		switch {
		case cand[2*g] >= off:
			cand[2*g], cand[2*g+1] = -1, -1
		case cand[2*g+1] > off:
			cand[2*g+1] = off
		}
	}
}

// sourceSubmatch converts a core submatch index slice into SOURCE pattern
// numbering (normative rule 3): slot 0 is the full match; slot g is source
// group g's text when it participated in this branch, "" otherwise
// (other-branch groups, non-participating groups, and assertion-inner
// groups are all "" — class (k)).
func (b *branchPattern) sourceSubmatch(s string, cand []int, nSrcGroups int) []string {
	out := make([]string, nSrcGroups+1)
	out[0] = s[cand[0]:cand[1]]
	for src := 1; src <= nSrcGroups; src++ {
		core := b.remap[src]
		if core < 0 || 2*core+1 >= len(cand) {
			continue
		}
		if start, end := cand[2*core], cand[2*core+1]; start >= 0 {
			out[src] = s[start:end]
		}
	}
	return out
}

// skipCharClass advances past a [...] character class starting at
// s[pos]='[', returning the index of the closing ']' (or len(s) when
// unterminated).
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
