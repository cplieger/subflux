package mediawrite

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/cplieger/subflux/internal/status"
)

// rootState is one configured root. It is replaced, never reused, when its
// path leaves the configuration and returns, so a probe started before that
// finds its state gone and installs nothing.
type rootState struct {
	cur          *rootHandle   // nil while unavailable
	probe        *probeRun     // the outstanding probe, nil when none
	ready        chan struct{} // closed once the first probe and its recovery settle, or the root leaves
	check        chan struct{} // FolderState's slot: one check outstanding
	path         string
	kept         int // intents the last recovery attempt left pending
	isReady      bool
	gone         bool // left the configuration
	recovered    bool // the handle in cur has had its recovery; calls wait or fail until then
	wantRecovery bool // a probe settled since the last recovery attempt started
	recovering   bool // a recovery attempt is running
}

// rootHandle is borrowed by every operation for its whole call; a replaced
// or removed handle is retired and closes when its last borrow returns.
type rootHandle struct {
	root    *os.Root
	info    fs.FileInfo // the root directory as opened, for os.SameFile
	borrows int
	retired bool
}

// borrowed is one operation's hold on its video's root: rel is the video's
// path inside it.
type borrowed struct {
	w     *Writer
	state *rootState
	h     *rootHandle
	rel   string
}

func (b *borrowed) release() {
	b.w.mu.Lock()
	defer b.w.mu.Unlock()
	b.h.releaseLocked()
}

func (h *rootHandle) releaseLocked() {
	h.borrows--
	if h.retired && h.borrows == 0 {
		_ = h.root.Close()
	}
}

func (h *rootHandle) retireLocked() {
	h.retired = true
	if h.borrows == 0 {
		_ = h.root.Close()
	}
}

func (rs *rootState) markReadyLocked() {
	if !rs.isReady {
		rs.isReady = true
		close(rs.ready)
	}
}

// applyRootsLocked replaces the configured set with paths, cleaned and
// deduplicated, and retains the media_root alerts of exactly that set.
func (w *Writer) applyRootsLocked(paths []string) {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[filepath.Clean(p)] = true
	}
	for p, rs := range w.paths {
		if want[p] {
			continue
		}
		rs.gone = true
		w.unwritable.clearRoot(p)
		if rs.cur != nil {
			rs.cur.retireLocked()
			rs.cur = nil
		}
		rs.markReadyLocked()
		delete(w.paths, p)
	}
	live := make([]status.AlertSource, 0, len(want))
	for p := range want {
		if w.paths[p] == nil {
			w.paths[p] = &rootState{path: p, ready: make(chan struct{}), check: make(chan struct{}, 1)}
		}
		live = append(live, status.MediaRootAlert(p))
	}
	slices.Sort(live)
	w.alerts.RetainAlerts("media_root", live)
}

// stateOfLocked is the deepest configured root containing path lexically.
func (w *Writer) stateOfLocked(path string) (*rootState, string, bool) {
	var best *rootState
	var bestRel string
	for p, rs := range w.paths {
		if !filepath.IsAbs(p) {
			continue
		}
		rel, err := filepath.Rel(p, path)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		if best == nil || len(p) > len(best.path) {
			best, bestRel = rs, rel
		}
	}
	return best, bestRel, best != nil
}

// borrow maps video, an absolute path, to its root and holds that root's
// handle until release. It waits for Run's first reading of the roots and
// for the root's first probe and recovery.
func (w *Writer) borrow(ctx context.Context, video string) (*borrowed, error) {
	if err := wait(ctx, w.started); err != nil {
		return nil, err
	}
	video = filepath.Clean(video)
	for {
		w.mu.Lock()
		rs, rel, ok := w.stateOfLocked(video)
		w.mu.Unlock()
		if !ok {
			w.log.Warn("mediawrite: video is outside every media root", "video", video)
			return nil, fmt.Errorf("%w: %s", ErrOutsideRoot, video)
		}
		if err := wait(ctx, rs.ready); err != nil {
			return nil, err
		}
		if b, held, err := w.hold(rs, rel); held || err != nil {
			return b, err
		}
	}
}

// hold borrows rs's handle; false when rs left the configuration meanwhile,
// so the caller resolves again.
func (w *Writer) hold(rs *rootState, rel string) (*borrowed, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case rs.gone:
		return nil, false, nil
	case rs.cur == nil:
		return nil, false, fmt.Errorf("%w: %s", ErrRootUnavailable, rs.path)
	case !rs.recovered:
		return nil, false, fmt.Errorf("%w: %s is recovering its pending writes", ErrRootUnavailable, rs.path)
	}
	rs.cur.borrows++
	return &borrowed{w: w, state: rs, h: rs.cur, rel: rel}, true, nil
}

func wait(ctx context.Context, ch <-chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
