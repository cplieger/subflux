package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// presence is what a name in a directory holds against a content hash.
type presence uint8

const (
	absent presence = iota + 1
	holdsSum
	holdsOther
)

// wantRecoveryLocked asks Run for a recovery attempt of rs after a probe
// settled; rs turns ready now unless a returned handle awaits its recovery.
func (w *Writer) wantRecoveryLocked(rs *rootState) {
	rs.wantRecovery = true
	if rs.cur == nil || rs.recovered {
		rs.markReadyLocked()
	}
	select {
	case w.recoverReq <- struct{}{}:
	default:
	}
}

// startRecoveries starts a recovery attempt of every root a probe settled
// since its last one started, one outstanding per root.
func (w *Writer) startRecoveries(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, rs := range w.paths {
		if rs.wantRecovery && !rs.recovering {
			rs.wantRecovery, rs.recovering = false, true
			w.recoveries.Go(func() { w.recoverRoot(ctx, rs) })
		}
	}
}

// recoverRoot runs one recovery attempt of rs through its handle, or only
// counts rs's intents while it is unavailable, then marks the handle
// recovered when the attempt read the pending writes.
func (w *Writer) recoverRoot(ctx context.Context, rs *rootState) {
	w.mu.Lock()
	h := rs.cur
	if h != nil {
		h.borrows++
	}
	w.mu.Unlock()
	var kept []statedb.Intent
	read := false
	// Deferred so a recovery ended mid-attempt still frees rs's slot and
	// its borrow.
	defer func() {
		w.finishRecovery(rs, h, kept, read)
		w.countPending(ctx)
	}()
	kept, err := w.recoverIntents(ctx, rs, h)
	if err != nil {
		w.log.Warn("mediawrite: reading the pending writes for recovery failed", "root", rs.path, "err", err)
		return
	}
	read = true
}

// finishRecovery records an attempt of rs through h that left kept intents
// pending, when it read the pending writes, and asks for the next one a
// probe wanted.
func (w *Writer) finishRecovery(rs *rootState, h *rootHandle, kept []statedb.Intent, read bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if h != nil {
		h.releaseLocked()
	}
	rs.recovering = false
	if read {
		w.outliveLocked(kept)
		rs.recovered = rs.recovered || h != nil && rs.cur == h
	}
	if !rs.gone {
		rs.markReadyLocked()
	}
	if rs.wantRecovery {
		select {
		case w.recoverReq <- struct{}{}:
		default:
		}
	}
}

// recoverIntents settles each intent of a snapshot of the pending writes
// whose video lies under rs, through h; with h nil it only counts them. It
// returns the intents that stay pending.
func (w *Writer) recoverIntents(ctx context.Context, rs *rootState, h *rootHandle) ([]statedb.Intent, error) {
	intents, err := w.store.PendingWrites(ctx)
	if err != nil {
		return nil, err
	}
	w.fs.Step("recover-snapshot")
	var kept []statedb.Intent
	for i := range intents {
		it := &intents[i]
		owner, rel, err := w.rootOf(ctx, it.Inst)
		switch {
		case err != nil, owner != rs:
			continue // settleOrphans keeps an intent whose instance cannot be read
		case h == nil:
			kept = append(kept, *it)
			continue
		}
		if !w.recoverIntent(ctx, &borrowed{w: w, state: rs, h: h, rel: rel}, it) {
			kept = append(kept, *it)
		}
	}
	return kept, nil
}

// rootOf is the configured root holding inst's video and the video's path
// inside it; nil when no configured root holds it.
func (w *Writer) rootOf(ctx context.Context, inst statedb.InstanceID) (*rootState, string, error) {
	in, err := w.instance(ctx, inst)
	if err != nil {
		return nil, "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	rs, rel, _ := w.stateOfLocked(filepath.Clean(in.VideoPath))
	return rs, rel, nil
}

// recoverIntent settles it under its leases by recovery's table, acting
// only while the name's intent still matches it; false when it stays
// pending.
func (w *Writer) recoverIntent(ctx context.Context, b *borrowed, it *statedb.Intent) bool {
	unlock, err := w.lease(ctx, it.Inst, b.state.path)
	if err != nil {
		return false
	}
	defer unlock()
	current, err := w.matches(ctx, it)
	if err != nil {
		w.log.Warn("mediawrite: re-reading a pending write failed; it stays pending", "instance", it.Inst, "name", it.Name, "err", err)
		return false
	}
	if !current {
		return true
	}
	var removes []statedb.Removal
	if it.Op == statedb.Deliver {
		d, found, rerr := w.store.PendingDeliverOf(ctx, it.Inst, it.Name)
		if rerr != nil || !found {
			w.log.Warn("mediawrite: reading a pending delivery failed; it stays pending", "instance", it.Inst, "name", it.Name, "err", rerr)
			return false
		}
		removes = d.Removes
	}
	dirRel := filepath.Dir(b.rel)
	rels := []string{filepath.Join(dirRel, it.Name)}
	for _, r := range removes {
		rels = append(rels, filepath.Join(dirRel, r.Name))
	}
	unlockPaths, err := w.lockPaths(ctx, b.state.path, rels...)
	if err != nil {
		return false
	}
	defer unlockPaths()
	err = w.settle(ctx, b, it, removes)
	switch {
	case errors.Is(err, ErrPathPending):
		return false
	case err != nil:
		w.log.Warn("mediawrite: recovering a pending write failed; it stays pending", "instance", it.Inst, "name", it.Name, "err", err)
		return false
	}
	return true
}

// matches reports whether it's name still has an intent of its op and hash.
func (w *Writer) matches(ctx context.Context, it *statedb.Intent) (bool, error) {
	intents, err := w.store.PendingWrites(ctx)
	if err != nil {
		return false, err
	}
	return slices.Contains(intents, *it), nil
}

// settle decides it from the disk and ends it. A video directory that is
// gone settles with nothing removed only when its deepest existing
// ancestor lists an entry: an empty one is a nested mount that is down, and
// settling then would strip a landed file of its record (ErrPathPending).
func (w *Writer) settle(ctx context.Context, b *borrowed, it *statedb.Intent, removes []statedb.Removal) error {
	dirRel := filepath.Dir(b.rel)
	dir, openErr := w.fs.OpenDir(b.h.root, dirRel)
	if isErrno(openErr, syscall.ENOENT, syscall.ENOTDIR) {
		return w.settleGone(ctx, b, it, dirRel)
	}
	if openErr != nil {
		return fmt.Errorf("mediawrite: open the video directory: %w", openErr)
	}
	defer dir.Close()
	s := staged{l: &listing{dir: dir, b: b, inst: it.Inst, dirRel: dirRel}, name: it.Name, sum: it.SHA256}
	if err := w.openStaged(&s); err != nil {
		return err
	}
	defer s.close()
	landed, err := w.onDisk(&s, it.Op)
	if err != nil {
		return err
	}
	if err := w.tidy(&s, it.Op, landed, removes); err != nil {
		return err
	}
	if err := w.fs.FsyncDir(s.dirFile); err != nil {
		return fmt.Errorf("mediawrite: fsync the video directory: %w", err)
	}
	w.fs.Step("recover-synced")
	if err := w.fs.End(ctx, it.Op, it.Inst, it.Name, landed); err != nil {
		return err
	}
	w.log.Info("mediawrite: recovered a pending write", "instance", it.Inst, "name", it.Name, "op", opText(it.Op), "landed", landed)
	return nil
}

// tidy removes what a settled intent of op leaves behind: a landed
// delivery's superseded files still holding their bytes, and the stage of
// a delivery or retime.
func (w *Writer) tidy(s *staged, op statedb.Op, landed bool, removes []statedb.Removal) error {
	if landed && op == statedb.Deliver {
		for _, r := range removes {
			if err := w.removeHolding(s.l.dir, r); err != nil {
				return err
			}
		}
		w.fs.Step("recover-removed")
	}
	if op == statedb.Delete {
		return nil
	}
	return w.removeStage(s)
}

// onDisk is recovery's landed rule for op: a delivery whose name is free
// and whose stage holds its bytes lands by publishing the stage.
func (w *Writer) onDisk(s *staged, op statedb.Op) (bool, error) {
	at, err := w.holds(s.l.dir, s.name, s.sum)
	if err != nil {
		return false, err
	}
	switch op {
	case statedb.Retime:
		return at == holdsSum, nil
	case statedb.Delete:
		return at != holdsSum, nil
	}
	if at != absent {
		return at == holdsSum, nil
	}
	stage := s.stageName()
	if st, serr := w.holds(s.l.dir, stage, s.sum); serr != nil || st != holdsSum {
		return false, serr
	}
	err = w.publish(s, stage)
	switch {
	case errors.Is(err, ErrNameContended):
		return false, nil
	case err != nil:
		return false, err
	}
	w.fs.Step("recover-published")
	return true, nil
}

// removeStage removes s's derived stage whatever it holds: a stage torn by
// a crash mid-write holds a prefix of s's bytes, and stageName owns why the
// name is subflux's.
func (w *Writer) removeStage(s *staged) error {
	if err := w.fs.Remove(s.l.dir, s.stageName()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("mediawrite: remove the stage of %q: %w", s.name, err)
	}
	return nil
}

// holds is what name in dir holds against sum: anything but a regular file
// within maxSubtitleBytes holds other bytes.
func (w *Writer) holds(dir *os.Root, name string, sum [32]byte) (presence, error) {
	info, err := w.fs.Lstat(dir, name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return absent, nil
	case err != nil:
		return 0, fmt.Errorf("mediawrite: check %q: %w", name, err)
	case !info.Mode().IsRegular(), info.Size() > maxSubtitleBytes:
		return holdsOther, nil
	}
	got, _, err := w.hashFile(dir, name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return absent, nil
	case err != nil:
		return 0, fmt.Errorf("mediawrite: hash %q: %w", name, err)
	case got != sum:
		return holdsOther, nil
	}
	return holdsSum, nil
}

// settleGone ends it, whose video directory is gone, by the table with its
// name absent, once the deepest existing ancestor shows a mounted folder.
func (w *Writer) settleGone(ctx context.Context, b *borrowed, it *statedb.Intent, dirRel string) error {
	mounted, err := w.ancestorLists(b.h.root, dirRel)
	switch {
	case err != nil:
		return err
	case !mounted:
		return fmt.Errorf("%w: the folder of %q is on a mount that is down", ErrPathPending, it.Name)
	}
	landed := it.Op == statedb.Delete
	if err := w.fs.End(ctx, it.Op, it.Inst, it.Name, landed); err != nil {
		return err
	}
	w.log.Info("mediawrite: the folder of a pending write is gone; it is settled by what is on disk",
		"instance", it.Inst, "name", it.Name, "op", opText(it.Op), "landed", landed)
	return nil
}

// ancestorLists reports whether the deepest existing ancestor of dirRel
// inside root lists an entry, the probe's rule for a mounted folder.
func (w *Writer) ancestorLists(root *os.Root, dirRel string) (bool, error) {
	for d := dirRel; d != "."; {
		d = filepath.Dir(d)
		dir, err := w.fs.OpenDir(root, d)
		if isErrno(err, syscall.ENOENT, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("mediawrite: open %q: %w", d, err)
		}
		err = w.listsEntry(dir)
		_ = dir.Close()
		switch {
		case errors.Is(err, errEmptyRoot):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("mediawrite: list %q: %w", d, err)
		}
		return true, nil
	}
	return false, nil
}

// startOrphansLocked starts a pass settling the intents whose video no
// configured root holds, unless one is running.
func (w *Writer) startOrphansLocked(ctx context.Context) {
	if w.orphaning {
		return
	}
	w.orphaning = true
	w.recoveries.Go(func() {
		kept, ok := w.settleOrphans(ctx)
		w.mu.Lock()
		w.orphaning = false
		if ok {
			w.outliveLocked(kept)
		}
		w.mu.Unlock()
		w.countPending(ctx)
		w.fs.Pause("orphans-settled")
	})
}

// settleOrphans settles not landed, touching no file, every pending intent
// whose video lies under no configured root. It returns those that stay
// pending and those whose instance cannot be read, which no root's
// recovery can claim; false when the pending writes cannot be read.
func (w *Writer) settleOrphans(ctx context.Context) (kept []statedb.Intent, ok bool) {
	intents, err := w.store.PendingWrites(ctx)
	if err != nil {
		w.log.Warn("mediawrite: reading the pending writes failed", "err", err)
		return nil, false
	}
	for i := range intents {
		it := &intents[i]
		owner, _, err := w.rootOf(ctx, it.Inst)
		switch {
		case err != nil:
			w.log.Warn("mediawrite: a pending write's instance cannot be read; it stays pending", "instance", it.Inst, "name", it.Name, "err", err)
			kept = append(kept, *it)
		case owner == nil && !w.settleOrphan(ctx, it):
			kept = append(kept, *it)
		}
	}
	return kept, true
}

func (w *Writer) settleOrphan(ctx context.Context, it *statedb.Intent) bool {
	unlock, err := w.leases.Lock(ctx, leaseKey{kind: instanceLease, inst: it.Inst})
	if err != nil {
		return false
	}
	defer unlock()
	if current, err := w.matches(ctx, it); err != nil || !current {
		return err == nil
	}
	if err := w.fs.End(ctx, it.Op, it.Inst, it.Name, false); err != nil {
		w.log.Warn("mediawrite: settling a pending write of an unconfigured root failed", "instance", it.Inst, "name", it.Name, "err", err)
		return false
	}
	w.log.Warn("mediawrite: the media root of a pending write is no longer configured; it is settled not landed and its file left alone",
		"instance", it.Inst, "name", it.Name, "op", opText(it.Op))
	return true
}

// outliveLocked records kept, the intents a pass left pending, for
// countPending.
func (w *Writer) outliveLocked(kept []statedb.Intent) {
	for _, it := range kept {
		w.outlived[it] = struct{}{}
	}
}

// countPending raises pending_writes while any intent a pass left pending
// is still stored, naming where each waits by a fresh read of its
// instance. Every pass that records kept intents counts after it.
// An intent leaves the count only once a count's own read of the pending
// writes lacks it: a pass that cannot claim an intent another pass kept
// (an orphan pass for one under a configured root, a root's recovery for
// one whose instance turned unreadable) must not end its count, or the
// alert clears while the write still waits.
func (w *Writer) countPending(ctx context.Context) {
	w.countMu.Lock()
	defer w.countMu.Unlock()
	// Copied before the read: an intent kept after it may be newer than the
	// read, and the pass that kept it counts it next.
	w.mu.Lock()
	before := maps.Clone(w.outlived)
	w.mu.Unlock()
	intents, err := w.store.PendingWrites(ctx)
	if err != nil {
		w.log.Warn("mediawrite: reading the pending writes to count them failed", "err", err)
		return
	}
	w.fs.Pause("pending-read")
	stored := make(map[statedb.Intent]bool, len(intents))
	for _, it := range intents {
		stored[it] = true
	}
	c := pendingCount{under: map[string]int{}}
	for it := range before {
		if !stored[it] {
			continue
		}
		switch owner, _, err := w.rootOf(ctx, it.Inst); {
		case err != nil:
			c.unread++
		case owner == nil:
			c.removed++
		default:
			c.under[owner.path]++
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for it := range before {
		if !stored[it] {
			delete(w.outlived, it)
		}
	}
	w.alerts.Reconcile(status.AlertPendingWrites, c.total() > 0, c.text())
}

// pendingCount is where the intents of one count wait: under a removed
// media root, with an instance that cannot be read, or under each
// configured root by path.
type pendingCount struct {
	under           map[string]int
	removed, unread int
}

func (c *pendingCount) total() int {
	n := c.removed + c.unread
	for _, k := range c.under {
		n += k
	}
	return n
}

func (c *pendingCount) text() string {
	total := c.total()
	if total == 0 {
		return ""
	}
	var parts []string
	if c.removed > 0 {
		parts = append(parts, fmt.Sprintf("%d under a removed media root", c.removed))
	}
	if c.unread > 0 {
		parts = append(parts, fmt.Sprintf("%d whose video cannot be looked up", c.unread))
	}
	for _, p := range slices.Sorted(maps.Keys(c.under)) {
		parts = append(parts, fmt.Sprintf("%d under %s", c.under[p], p))
	}
	return fmt.Sprintf("%d subtitle writes wait for their media folders: %s", total, strings.Join(parts, ", "))
}

func opText(op statedb.Op) string {
	switch op {
	case statedb.Deliver:
		return "deliver"
	case statedb.Retime:
		return "retime"
	case statedb.Delete:
		return "delete"
	}
	return fmt.Sprintf("op %d", op)
}
