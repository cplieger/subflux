package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cplieger/subflux/internal/status"
)

var (
	errRelativeRoot = errors.New("not an absolute path")
	errEmptyRoot    = errors.New("the directory is empty; the share may not be mounted")
	errNotDir       = errors.New("not a directory")
	errOverdue      = fmt.Errorf("no answer within %s; the mount may be hung", rootProbeTimeout)
)

// probeRun is one probe of a root. settled closes at its result or when it
// turns overdue, whichever is first; ok is set before it closes.
type probeRun struct {
	settled chan struct{}
	write   bool
	done    bool // settled has closed
	ok      bool
}

// probeResult is what one probe found: a fresh handle, the borrowed one
// still current, or the error that makes the root unavailable.
type probeResult struct {
	err  error
	root *os.Root
	info fs.FileInfo
}

// startProbeLocked starts rs's probe, writing when write is set; the caller
// checked that none is outstanding. The probe outlives ctx's cancellation,
// so a shutdown never reads as an unavailable root.
func (w *Writer) startProbeLocked(ctx context.Context, rs *rootState, write bool) *probeRun {
	r := &probeRun{settled: make(chan struct{}), write: write}
	rs.probe = r
	cur := rs.cur
	if cur != nil {
		cur.borrows++
	}
	overdue := time.AfterFunc(rootProbeTimeout, func() { w.overdue(rs, r) })
	detached := context.WithoutCancel(ctx)
	go func() {
		pctx, cancel := context.WithTimeout(detached, rootProbeTimeout)
		defer cancel()
		res := w.probeRoot(pctx, rs.path, cur, write)
		overdue.Stop()
		w.finishProbe(rs, r, cur, res)
	}()
	return r
}

// probeRoot reads the root through cur while the path still names the
// directory cur holds, else through a fresh handle: the root must be a
// directory listing an entry, and with write it must take a write.
func (w *Writer) probeRoot(ctx context.Context, path string, cur *rootHandle, write bool) probeResult {
	if !filepath.IsAbs(path) {
		return probeResult{err: errRelativeRoot}
	}
	info, err := w.fs.Stat(path)
	if err != nil {
		return probeResult{err: err}
	}
	if !info.IsDir() {
		return probeResult{err: errNotDir}
	}
	res := probeResult{info: info}
	var root *os.Root
	if cur != nil && os.SameFile(cur.info, info) {
		root = cur.root
	} else {
		if root, err = w.fs.OpenRoot(path); err != nil {
			return probeResult{err: err}
		}
		res.root = root
	}
	if err := w.listsEntry(root); err != nil {
		return res.failed(err)
	}
	if write {
		if err := w.fs.ProbeWrite(ctx, root); err != nil {
			return res.failed(err)
		}
	}
	return res
}

func (res probeResult) failed(err error) probeResult {
	if res.root != nil {
		_ = res.root.Close()
	}
	return probeResult{err: err}
}

func (w *Writer) listsEntry(root *os.Root) error {
	d, err := w.fs.Open(root, ".")
	if err != nil {
		return err
	}
	defer d.Close()
	switch _, err := d.Readdirnames(1); {
	case errors.Is(err, io.EOF):
		return errEmptyRoot
	case err != nil:
		return err
	}
	return nil
}

// finishProbe applies res to rs unless r turned overdue or rs left the
// configuration, in which case a fresh handle is closed unused.
func (w *Writer) finishProbe(rs *rootState, r *probeRun, borrowed *rootHandle, res probeResult) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if borrowed != nil {
		borrowed.releaseLocked()
	}
	if rs.probe == r {
		rs.probe = nil
	}
	if r.done || rs.gone {
		if res.root != nil {
			_ = res.root.Close()
		}
		r.settleLocked(false)
		return
	}
	if res.err != nil {
		w.unavailableLocked(rs, res.err)
		r.settleLocked(false)
	} else {
		w.availableLocked(rs, res)
		r.settleLocked(true)
	}
	w.wantRecoveryLocked(rs)
}

// overdue marks rs unavailable when r has not settled by its deadline; r's
// goroutine keeps rs's probe slot until its syscall returns.
func (w *Writer) overdue(rs *rootState, r *probeRun) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r.done {
		return
	}
	r.settleLocked(false)
	if rs.gone {
		return
	}
	w.unavailableLocked(rs, errOverdue)
	w.wantRecoveryLocked(rs)
}

func (r *probeRun) settleLocked(ok bool) {
	if r.done {
		return
	}
	r.done, r.ok = true, ok
	close(r.settled)
}

func (w *Writer) unavailableLocked(rs *rootState, cause error) {
	w.unwritable.clearRoot(rs.path)
	if rs.cur != nil {
		rs.cur.retireLocked()
		rs.cur = nil
	}
	w.alerts.Reconcile(status.MediaRootAlert(rs.path), true, fmt.Sprintf("Media root %s is unavailable: %v", rs.path, cause))
}

func (w *Writer) availableLocked(rs *rootState, res probeResult) {
	if rs.cur == nil {
		rs.recovered = false
	}
	if res.root != nil {
		if rs.cur != nil {
			rs.cur.retireLocked()
		}
		rs.cur = &rootHandle{root: res.root, info: res.info}
	}
	w.alerts.Reconcile(status.MediaRootAlert(rs.path), false, "")
}

// probeAndWait runs or joins a probe of rs that writes when write is set,
// and reports whether it found the root available; true when ctx ends
// first, since nothing was learnt against the root. A probe already
// outstanding that does not write is waited out first; an overdue one still
// stuck in its syscall answers false at once.
func (w *Writer) probeAndWait(ctx context.Context, rs *rootState, write bool) bool {
	for {
		w.mu.Lock()
		r := rs.probe
		switch {
		case rs.gone, r != nil && r.done:
			w.mu.Unlock()
			return false
		case r == nil:
			r = w.startProbeLocked(ctx, rs, write)
		}
		w.mu.Unlock()
		if wait(ctx, r.settled) != nil {
			return true
		}
		if r.write || !write {
			return r.ok
		}
	}
}

// writeErr classifies err from a write under b's root: EROFS or EACCES runs
// the write probe, and only a failing probe makes it the root's fault.
func (w *Writer) writeErr(ctx context.Context, b *borrowed, err error) error {
	if !errors.Is(err, syscall.EROFS) && !errors.Is(err, syscall.EACCES) {
		return err
	}
	if w.probeAndWait(ctx, b.state, true) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrRootUnavailable, err)
}
