package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// FolderState is what one check of a reported folder found. Zero is
// invalid.
type FolderState uint8

// The folder states: no configured root contains the folder; its root
// cannot be opened or the check overran; a directory; absent or not a
// directory.
const (
	FolderOutside FolderState = iota + 1
	FolderUnavailable
	FolderFound
	FolderMissing
)

var folderText = [...]string{FolderOutside: "outside", FolderUnavailable: "unavailable", FolderFound: "found", FolderMissing: "missing"}

// MarshalText is outside, unavailable, found or missing, refusing any other
// value.
func (s FolderState) MarshalText() ([]byte, error) {
	if s < FolderOutside || s > FolderMissing {
		return nil, fmt.Errorf("mediawrite: folder state %d is not outside, unavailable, found or missing", s)
	}
	return []byte(folderText[s]), nil
}

// UnmarshalText reads exactly what MarshalText writes, leaving s unchanged
// on a refusal.
func (s *FolderState) UnmarshalText(b []byte) error {
	for v := FolderOutside; v <= FolderMissing; v++ {
		if string(b) == folderText[v] {
			*s = v
			return nil
		}
	}
	return fmt.Errorf("mediawrite: folder state %q is not outside, unavailable, found or missing", b)
}

// FolderState checks path with one Lstat through the deepest containing
// root's os.Root (its live handle, else one opened for the call and closed
// after it) within rootProbeTimeout. Each root runs one check at a time,
// its open and Lstat included: a call that cannot take the root's slot or
// finish its check in time answers FolderUnavailable, and an overrun check
// keeps the slot until its syscall returns.
func (w *Writer) FolderState(ctx context.Context, path string) FolderState {
	ctx, cancel := context.WithTimeout(ctx, rootProbeTimeout)
	defer cancel()
	if wait(ctx, w.started) != nil {
		return FolderUnavailable
	}
	w.mu.Lock()
	rs, rel, ok := w.stateOfLocked(filepath.Clean(path))
	w.mu.Unlock()
	if !ok {
		return FolderOutside
	}
	select {
	case rs.check <- struct{}{}:
	case <-ctx.Done():
		return FolderUnavailable
	}
	answer := make(chan FolderState, 1)
	go func() {
		defer func() { <-rs.check }()
		answer <- w.checkFolder(rs, rel)
	}()
	select {
	case s := <-answer:
		return s
	case <-ctx.Done():
		return FolderUnavailable
	}
}

// checkFolder runs while holding rs's check slot.
func (w *Writer) checkFolder(rs *rootState, rel string) FolderState {
	root, done := w.folderRoot(rs)
	if root == nil {
		return FolderUnavailable
	}
	defer done()
	info, err := w.fs.Lstat(root, rel)
	switch {
	case err == nil && info.IsDir():
		return FolderFound
	case err == nil, errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return FolderMissing
	}
	return FolderUnavailable
}

// folderRoot borrows rs's live handle or opens one for the call; done
// returns or closes it. A nil root is a root that cannot be opened.
func (w *Writer) folderRoot(rs *rootState) (root *os.Root, done func()) {
	w.mu.Lock()
	h := rs.cur
	if h != nil {
		h.borrows++
	}
	w.mu.Unlock()
	if h != nil {
		return h.root, func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			h.releaseLocked()
		}
	}
	root, err := w.fs.OpenRoot(rs.path)
	if err != nil {
		return nil, nil
	}
	return root, func() { _ = root.Close() }
}
