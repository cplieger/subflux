package mediawrite

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cplieger/atomicfile/v4"
	"github.com/cplieger/pathinside/v2"
)

const (
	probeLine   = "subflux write probe\n"
	probePrefix = ".subflux-write-probe-"

	// maxConcurrentRechecks bounds the probes of a known-bad set, which can
	// hold hundreds of folders; Roots and Folders sets are probed at once.
	maxConcurrentRechecks = 8
)

var errProbeTimeout = errors.New("probe timed out")

// PreflightRequest selects what a Preflight probes before any provider work.
type PreflightRequest struct {
	// Folders are the folders scoped work will write into. Only entries
	// inside a bound root are probed, with their known-bad ancestors and
	// descendants; an entry the containment check refuses is skipped.
	Folders []string
	// Roots probes every bound root; the verdict depends only on them.
	Roots bool
	// RecheckBad also re-probes every known-bad folder, without letting a
	// bad subfolder fail the verdict.
	RecheckBad bool
	// Raise re-creates a dismissed alert of a folder that still fails.
	Raise bool
}

type probeKind int

const (
	kindUnvalidated probeKind = iota
	kindRoot
	kindConfirm
	kindVettedBad
)

type outcomeKind int

const (
	probeOK outcomeKind = iota
	probeRefused
	probeVanished
	probeFault
	probeNotProven
)

type probeOutcome struct {
	err  error
	op   string
	kind outcomeKind
}

func (o probeOutcome) fails() bool { return o.kind == probeFault || o.kind == probeNotProven }

type probeTarget struct {
	confirm *faultReport // set when a failed WriteFile asks for the probe
	folder  string
	size    int64
	quiet   bool
}

// probeCall is one probe of a folder. A folder has at most one, in
// Writer.probes until its filesystem work has ended and its outcome is
// applied. A caller arriving while the work runs shares or waits for it; one
// arriving after waits for the outcome and then probes anew.
type probeCall struct {
	validate func(ctx context.Context, path string) error
	confirm  *faultReport
	done     chan struct{} // closed once outcome is settled
	folder   string
	outcome  probeOutcome
	size     int64
	kind     probeKind
	quiet    bool
	marked   bool        // the settlement marked the folder; read after done
	settled  atomic.Bool // won by the probe's own finish or by its deadline
	finished atomic.Bool // the filesystem work returned
	timedOut atomic.Bool // its deadline settled it
	vetted   atomic.Bool
}

// applied reports whether c's work has ended and its outcome is settled, so
// c no longer answers for the folder.
func (c *probeCall) applied() bool {
	if !c.finished.Load() {
		return false
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// report is the fault a call's outcome records: a write confirmation's
// names the failed write, with the probe's error beside it.
func (c *probeCall) report(o probeOutcome) faultReport {
	if c.confirm != nil {
		return faultReport{Op: c.confirm.Op, Err: c.confirm.Err, ProbeErr: o.err}
	}
	return faultReport{Op: o.op, Err: o.err}
}

// probe runs or joins the folder's probe and waits for its outcome. Each call
// settles as a timeout fault ProbeTimeout after its own start. A running call
// smaller than t.size, or one whose work already ended, is waited out before
// t's own starts with its full ProbeTimeout; one that hangs or times out
// while t waits answers for t at once, since the folder hangs. The error is
// non-nil only when ctx ended first.
func (w *Writer) probe(ctx context.Context, t probeTarget) (probeOutcome, error) {
	for {
		w.mu.Lock()
		c := w.probes[t.folder]
		stale := false
		if c == nil || c.applied() {
			c = w.startLocked(ctx, t)
		} else {
			stale = c.finished.Load()
		}
		hung := c.settled.Load() && !c.finished.Load()
		w.mu.Unlock()
		if err := c.wait(ctx); err != nil {
			return probeOutcome{}, err
		}
		switch {
		case stale:
			// Its work predates this caller, so probe anew now it is applied.
		case hung || c.timedOut.Load() || c.size >= t.size:
			return w.held(c, t, hung), nil
		}
	}
}

// held is the outcome c gives the caller asking for t, so a caller is never
// handed a fault the writer did not report. A write confirmation answered by
// another call records its own write's error; a hung call's fault is marked
// again, since the hang is current; and a fault its settlement could not mark,
// for a folder no bound root contains, reads as a refusal.
func (w *Writer) held(c *probeCall, t probeTarget, hung bool) probeOutcome {
	o := c.outcome
	switch {
	case o.kind != probeFault:
	case t.confirm != nil && c.confirm != t.confirm:
		w.fail(c.folder, faultReport{Op: t.confirm.Op, Err: t.confirm.Err, ProbeErr: o.err}, true, false)
	case hung:
		if !w.fail(c.folder, c.report(o), c.vetted.Load(), true) {
			return probeOutcome{kind: probeRefused}
		}
	case !c.marked:
		return probeOutcome{kind: probeRefused}
	}
	return o
}

func (w *Writer) startLocked(ctx context.Context, t probeTarget) *probeCall {
	c := &probeCall{
		folder:   t.folder,
		size:     t.size,
		confirm:  t.confirm,
		quiet:    t.quiet,
		validate: w.validate,
		done:     make(chan struct{}),
	}
	b, isBad := w.bad[t.folder]
	switch {
	case slices.Contains(w.roots, t.folder):
		c.kind = kindRoot
	case t.confirm != nil:
		c.kind = kindConfirm
	case isBad && b.vetted:
		c.kind = kindVettedBad
	default:
		c.kind = kindUnvalidated
	}
	c.vetted.Store(c.kind != kindUnvalidated)
	w.probes[t.folder] = c
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.cfg.ProbeTimeout)
	context.AfterFunc(pctx, func() { w.expire(c) })
	go w.run(pctx, cancel, c)
	return c
}

// run does c's filesystem work under pctx, c's own deadline, and settles c
// unless that deadline already did. c leaves Writer.probes only once
// settled, so a newer probe of the folder can never be overwritten by c's
// older outcome, and a caller that finds c settled before its work finished
// knows that work hangs.
func (w *Writer) run(pctx context.Context, cancel context.CancelFunc, c *probeCall) {
	defer cancel()
	o := w.runProbe(pctx, c)
	c.finished.Store(true)
	if c.settled.CompareAndSwap(false, true) {
		c.outcome = o
		w.settle(c)
		close(c.done)
	} else {
		<-c.done
	}
	w.mu.Lock()
	if w.probes[c.folder] == c {
		delete(w.probes, c.folder)
	}
	w.mu.Unlock()
}

// expire settles c as a timeout fault once its deadline passes while its work
// still runs, since the folder is what hangs. The cancel run defers after
// settling finds c settled and changes nothing.
func (w *Writer) expire(c *probeCall) {
	if !c.settled.CompareAndSwap(false, true) {
		return
	}
	c.timedOut.Store(true)
	c.outcome = probeOutcome{kind: probeFault, op: opProbe, err: w.timeoutErr()}
	w.settle(c)
	close(c.done)
}

// wait returns once c is settled, or with ctx's error when ctx ends first,
// which settles nothing.
func (c *probeCall) wait(ctx context.Context) error {
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) timeoutErr() error {
	return fmt.Errorf("%w after %s", errProbeTimeout, w.cfg.ProbeTimeout)
}

func (w *Writer) runProbe(pctx context.Context, c *probeCall) probeOutcome {
	switch c.kind {
	case kindUnvalidated:
		if c.validate == nil {
			return probeOutcome{kind: probeRefused}
		}
		if err := c.validate(pctx, c.folder); err != nil {
			if isContextErr(err) {
				return w.classify(pctx, c, opProbe, err)
			}
			return probeOutcome{kind: probeRefused, err: err}
		}
		c.vetted.Store(true)
	case kindVettedBad:
		if _, err := w.cfg.Stat(c.folder); errors.Is(err, fs.ErrNotExist) {
			return probeOutcome{kind: probeVanished, err: err}
		}
	case kindRoot, kindConfirm:
		// The root's own write names a missing root; a confirmation's write
		// already reached the folder.
	}

	w.sweepStale(c.folder)
	name := filepath.Join(c.folder, probePrefix+randomHex())
	if err := w.cfg.Write(pctx, name, probePayload(c.size)); err != nil {
		return w.classify(pctx, c, opProbe, err)
	}
	if err := w.cfg.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return w.classify(pctx, c, opProbeCleanup, err)
	}
	return probeOutcome{kind: probeOK}
}

func (w *Writer) classify(pctx context.Context, c *probeCall, op string, err error) probeOutcome {
	switch {
	case errors.Is(err, context.DeadlineExceeded) && pctx.Err() != nil:
		return probeOutcome{kind: probeFault, op: op, err: w.timeoutErr()}
	case errors.Is(err, fs.ErrNotExist) && c.kind != kindRoot:
		return probeOutcome{kind: probeVanished, op: op, err: err}
	case errors.Is(err, fs.ErrNotExist) || folderFault(err, probeSource):
		return probeOutcome{kind: probeFault, op: op, err: err}
	}
	slog.Warn("media folder write test inconclusive", "folder", c.folder, "error", err)
	return probeOutcome{kind: probeNotProven, op: op, err: err}
}

// settle applies a call's outcome to the current state exactly once,
// before its waiters return.
func (w *Writer) settle(c *probeCall) {
	o := c.outcome
	switch o.kind {
	case probeOK:
		w.clear(c.folder, true)
	case probeFault:
		c.marked = w.fail(c.folder, c.report(o), c.vetted.Load(), c.quiet)
	case probeVanished:
		w.forget(c.folder, false)
	case probeRefused:
		w.forget(c.folder, true)
	case probeNotProven:
		// A confirmation tests a write WriteFile already counted, and a quiet
		// recheck counts nothing.
		if c.confirm == nil && !c.quiet {
			w.cfg.Metrics.IncSubtitleWriteError()
		}
	}
}

func (w *Writer) sweepStale(folder string) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		slog.Debug("media folder probe sweep skipped", "folder", folder, "error", err)
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), probePrefix) {
			continue
		}
		if err := w.cfg.Remove(filepath.Join(folder, e.Name())); err != nil {
			slog.Debug("stale media probe file not removed", "folder", folder, "name", e.Name(), "error", err)
		}
	}
}

func probePayload(n int64) []byte {
	reps := int(n)/len(probeLine) + 1
	return bytes.Repeat([]byte(probeLine), reps)[:n]
}

func randomHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error
	return hex.EncodeToString(b[:])
}

// planned is one folder of a preflight probe set. derivedFrom lists the
// requested folders a related known-bad folder counts through.
type planned struct {
	folder      string
	derivedFrom []string
	requested   bool
	counted     bool // the folder's failure decides the verdict
	cached      bool // a passing probe within ProbeTTL answers for it
}

// Preflight probes the folders req names and returns nil, or the
// *UnwritableError of the first folder (by path) that decides the verdict
// and failed. With no media_roots bound it probes nothing and returns nil.
// A ctx that ends first returns ctx's error; a probe already started still
// runs to its own deadline and settles for the folder.
func (w *Writer) Preflight(ctx context.Context, req PreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	plan, recheck, ok := w.plan(req)
	if !ok {
		return nil
	}
	outcomes, err := w.probeAll(ctx, plan, len(plan), false)
	if err != nil {
		return err
	}
	recheckOutcomes, err := w.probeAll(ctx, recheck, maxConcurrentRechecks, false)
	if err != nil {
		return err
	}
	if req.Raise {
		all := slices.Concat(outcomes, recheckOutcomes)
		for i, p := range slices.Concat(plan, recheck) {
			if all[i].kind == probeFault {
				w.reraise(p.folder)
			}
		}
	}
	return w.verdict(plan, outcomes)
}

// plan derives the probe set under the mutex, in memory only.
func (w *Writer) plan(req PreflightRequest) (plan, recheck []planned, ok bool) {
	w.mu.Lock()
	if len(w.roots) == 0 {
		var fx effects
		if !w.warned {
			w.warned = true
			fx = effects{func() { slog.Warn("media_roots not configured; subtitle writes are not pre-checked") }}
		}
		w.commitLocked(fx)
		return nil, nil, false
	}
	defer w.mu.Unlock()
	set := probeSet{seen: map[string]int{}}
	if req.Roots {
		for _, r := range w.roots {
			set.add(planned{folder: r, counted: true})
		}
	}
	now := w.cfg.Now()
	for _, raw := range req.Folders {
		w.planFolderLocked(&set, raw, now)
	}
	if req.RecheckBad {
		for bf := range w.bad {
			if _, dup := set.seen[bf]; !dup {
				recheck = append(recheck, planned{folder: bf})
			}
		}
	}
	return set.items, recheck, true
}

func (w *Writer) planFolderLocked(set *probeSet, raw string, now time.Time) {
	if raw == "" {
		return
	}
	f := filepath.Clean(raw)
	if w.rootOfLocked(f) == "" {
		return
	}
	at, hit := w.probedOK[f]
	set.add(planned{folder: f, requested: true, counted: true, cached: hit && now.Sub(at) < w.cfg.ProbeTTL})
	for bf := range w.bad {
		if bf != f && (pathinside.Root(bf).Contains(f) || pathinside.Root(f).Contains(bf)) {
			set.add(planned{folder: bf, derivedFrom: []string{f}})
		}
	}
}

// probeSet is a preflight's folders in planning order; a folder planned
// twice is merged into its first entry.
type probeSet struct {
	seen  map[string]int
	items []planned
}

func (s *probeSet) add(p planned) {
	i, dup := s.seen[p.folder]
	if !dup {
		s.seen[p.folder] = len(s.items)
		s.items = append(s.items, p)
		return
	}
	q := &s.items[i]
	q.counted = q.counted || p.counted
	q.requested = q.requested || p.requested
	q.cached = q.cached && p.cached
	q.derivedFrom = append(q.derivedFrom, p.derivedFrom...)
}

// probeAll probes every planned folder, at most limit at once, and returns
// their outcomes by index; a cached folder counts as passing with no call.
func (w *Writer) probeAll(ctx context.Context, plan []planned, limit int, quiet bool) ([]probeOutcome, error) {
	outcomes := make([]probeOutcome, len(plan))
	errs := make([]error, len(plan))
	sem := make(chan struct{}, max(limit, 1))
	var wg sync.WaitGroup
	for i, p := range plan {
		if p.cached {
			continue
		}
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			outcomes[i], errs[i] = w.probe(ctx, probeTarget{folder: p.folder, size: int64(len(probeLine)), quiet: quiet})
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, cmp.Or(ctx.Err(), err)
	}
	return outcomes, nil
}

func (*Writer) verdict(plan []planned, outcomes []probeOutcome) error {
	accepted := map[string]bool{}
	for i, p := range plan {
		if p.requested && outcomes[i].kind != probeRefused && outcomes[i].kind != probeVanished {
			accepted[p.folder] = true
		}
	}
	var failing []int
	for i, p := range plan {
		counted := p.counted && (!p.requested || accepted[p.folder])
		for _, from := range p.derivedFrom {
			counted = counted || accepted[from]
		}
		if counted && outcomes[i].fails() {
			failing = append(failing, i)
		}
	}
	if len(failing) == 0 {
		return nil
	}
	first := slices.MinFunc(failing, func(a, b int) int { return strings.Compare(plan[a].folder, plan[b].folder) })
	o := outcomes[first]
	return &UnwritableError{Folder: plan[first].folder, Op: o.op, Err: o.err}
}

// Run re-probes the known-bad folders every RecheckInterval, at most eight
// at once, so a folder recovers without a scan; a still-failing folder
// changes nothing. It blocks until ctx is done and returns ctx's error once
// the current round has returned. A probe step hung in the kernel outlives
// it: its goroutine ends when the call returns.
func (w *Writer) Run(ctx context.Context) error {
	timer := time.NewTimer(w.cfg.RecheckInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		w.mu.Lock()
		set := make([]planned, 0, len(w.bad))
		for bf := range w.bad {
			set = append(set, planned{folder: bf})
		}
		w.mu.Unlock()
		if len(set) > 0 {
			_, _ = w.probeAll(ctx, set, maxConcurrentRechecks, true)
		}
		timer.Reset(w.cfg.RecheckInterval)
	}
}

type faultSource int

const (
	writeSource faultSource = iota
	probeSource
)

// folderFault reports whether err says the destination folder refuses
// writes. A name the filesystem refuses is the target's fault on a write and
// the folder's on a probe, whose own name is known good.
func folderFault(err error, src faultSource) bool {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, fs.ErrNotExist),
		errors.Is(err, atomicfile.ErrFileTooLarge), errors.Is(err, atomicfile.ErrSymlinkTarget),
		errors.Is(err, atomicfile.ErrNotRegular), errors.Is(err, atomicfile.ErrRaced),
		errors.Is(err, atomicfile.ErrUnsafePath), errors.Is(err, atomicfile.ErrEmptyPath):
		return false
	case errors.Is(err, syscall.ENAMETOOLONG), errors.Is(err, syscall.EINVAL),
		errors.Is(err, syscall.EILSEQ):
		return src == probeSource
	}
	return true
}
