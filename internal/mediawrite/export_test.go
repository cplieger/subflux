package mediawrite

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/cplieger/subflux/internal/statedb"
)

const (
	ProbeInterval    = probeInterval
	RootProbeTimeout = rootProbeTimeout
)

// Faults are the writer's filesystem calls a test may replace.
type Faults = fsOps

// Inject hands edit w's current calls to replace; call it before Run.
func Inject(w *Writer, edit func(*Faults)) { edit(&w.fs) }

func Reprobe(w *Writer) { w.reprobe() }

// Settled waits for Run's first reading of the roots and for every
// configured root's first probe.
func Settled(ctx context.Context, w *Writer) error {
	if err := wait(ctx, w.started); err != nil {
		return err
	}
	w.mu.Lock()
	var readies []chan struct{}
	for _, rs := range w.paths {
		readies = append(readies, rs.ready)
	}
	w.mu.Unlock()
	for _, ch := range readies {
		if err := wait(ctx, ch); err != nil {
			return err
		}
	}
	return nil
}

// Available reports whether the configured root at path holds a handle.
func Available(w *Writer, path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	rs := w.paths[filepath.Clean(path)]
	return rs != nil && rs.cur != nil
}

// Borrow holds the handle of video's root until release.
func Borrow(ctx context.Context, w *Writer, video string) (*os.Root, func(), error) {
	b, err := w.borrow(ctx, video)
	if err != nil {
		return nil, nil, err
	}
	return b.h.root, b.release, nil
}

// ProbeNow runs or joins a read-only probe of the root at path and reports
// whether it found the root available, then waits for the recovery a
// returned root runs before serving calls; false when that recovery has
// not finished within 10 s.
func ProbeNow(ctx context.Context, w *Writer, path string) bool {
	w.mu.Lock()
	rs := w.paths[filepath.Clean(path)]
	w.mu.Unlock()
	if rs == nil || !w.probeAndWait(ctx, rs, false) {
		return false
	}
	deadline := time.After(10 * time.Second)
	for {
		w.mu.Lock()
		done := rs.gone || rs.cur == nil || rs.recovered
		w.mu.Unlock()
		if done {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-time.After(time.Millisecond):
		}
	}
}

// RecoverNow runs one recovery attempt of the root at path in the caller's
// goroutine; false when the root is unknown or an attempt is running.
func RecoverNow(ctx context.Context, w *Writer, path string) bool {
	w.mu.Lock()
	rs := w.paths[filepath.Clean(path)]
	ok := rs != nil && !rs.recovering
	if ok {
		rs.recovering = true
	}
	w.mu.Unlock()
	if ok {
		w.recoverRoot(ctx, rs)
	}
	return ok
}

// Outlived is how many intents a pass left pending that no count has yet
// found settled.
func Outlived(w *Writer) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.outlived)
}

// StageName is the stage a delivery of the bytes hashing to sum at name
// beside inst's video uses.
func StageName(inst statedb.InstanceID, name string, sum [32]byte) string {
	return stageName(inst, name, sum)
}

// Lease takes inst's lease and then the path leases of rels under root.
func Lease(ctx context.Context, w *Writer, inst statedb.InstanceID, root string, rels ...string) (func(), error) {
	return w.lease(ctx, inst, root, rels...)
}

// LockPaths takes the path leases of rels under root, as reconcile does
// while holding the instance lease.
func LockPaths(ctx context.Context, w *Writer, root string, rels ...string) (func(), error) {
	return w.lockPaths(ctx, root, rels...)
}
