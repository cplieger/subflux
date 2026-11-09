package mediawrite

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/cplieger/subflux/internal/statedb"
)

// A stage is ".sfx-" + the first 32 hex digits of the SHA-256 of the
// instance id, the final name and the staged bytes' SHA-256 + ".tmp" (41
// bytes): recovery derives an intent's stage from the intent alone, two
// final names never share one, and only a write of that intent, whole or
// torn by a crash, creates the name, so whatever it holds is subflux's.
const (
	stageMarker   = ".sfx-"
	stageTokenLen = 32
	stageSuffix   = ".tmp"
)

func stageName(inst statedb.InstanceID, name string, sum [32]byte) string {
	h := sha256.New()
	h.Write(binary.BigEndian.AppendUint64(nil, uint64(inst)))
	h.Write([]byte(name))
	h.Write(sum[:])
	return stageMarker + hex.EncodeToString(h.Sum(nil))[:stageTokenLen] + stageSuffix
}

// staged is one write of op beside the video: a Deliver or a Retime
// staging its bytes, or a Delete.
type staged struct {
	l       *listing
	dirFile *os.File // the directory's own descriptor: fsynced, and renameat2's
	name    string
	bytes   []byte
	sum     [32]byte
	mtimeNs int64
	dirID   dirID
	op      statedb.Op
	haveDir bool
}

// openStaged opens s's directory descriptor and reads its fstat.
func (w *Writer) openStaged(s *staged) error {
	f, err := w.fs.Open(s.l.dir, ".")
	if err != nil {
		return fmt.Errorf("mediawrite: open the video directory of instance %d: %w", s.l.inst, err)
	}
	s.dirFile = f
	if info, err := w.fs.DirStat(s.l.dir); err == nil {
		s.dirID, s.haveDir = dirIDOf(info)
	}
	return nil
}

func (s *staged) close() {
	if s.dirFile != nil {
		_ = s.dirFile.Close()
	}
}

func (s *staged) stageName() string { return stageName(s.l.inst, s.name, s.sum) }

// errSettle marks a failed store write settling an intent.
var errSettle = errors.New("mediawrite: settle the intent")

// end settles s's intent, landed or not.
func (w *Writer) end(ctx context.Context, s *staged, landed bool) error {
	if err := w.fs.End(ctx, s.op, s.l.inst, s.name, landed); err != nil {
		return fmt.Errorf("%w of %q: %w", errSettle, s.name, err)
	}
	return nil
}

// land runs steps 7 to 9 of a delivery whose intent is stored. A failure
// known to precede the publish removes this call's stage and settles the
// intent not landed; any other leaves it for recovery.
func (w *Writer) land(ctx context.Context, b *borrowed, p *prepared) error {
	stage, err := w.stage(ctx, b, &p.staged)
	if err != nil {
		return err
	}
	removes := p.removes
	if p.sameName {
		if err := w.removeSameName(ctx, b, p, stage); err != nil {
			return err
		}
		removes = removes[1:]
	}
	if err := w.publish(&p.staged, stage); err != nil {
		if errors.Is(err, ErrNameContended) {
			return w.abandon(ctx, &p.staged, stage, err)
		}
		return w.leave(&p.staged, w.writeErr(ctx, b, err))
	}
	w.fs.Step("published")
	for _, r := range removes {
		if err := w.removeHolding(p.l.dir, r); err != nil {
			return w.leave(&p.staged, w.writeErr(ctx, b, err))
		}
	}
	w.fs.Step("removed")
	if err := w.fs.FsyncDir(p.dirFile); err != nil {
		return w.leave(&p.staged, err)
	}
	w.fs.Step("synced")
	if err := w.end(ctx, &p.staged, true); err != nil {
		return w.leave(&p.staged, err)
	}
	return nil
}

// stage creates s's stage with O_EXCL and makes it and its modification
// time durable before the directory entry, so no power loss keeps a later
// unlink but loses the stage. A file already at the stage name is the
// leftover of a settled write of these bytes (one settled while its root
// was unconfigured): it is removed and the creation retried once. A
// refusal of the creation itself is what marks the directory unwritable.
func (w *Writer) stage(ctx context.Context, b *borrowed, s *staged) (string, error) {
	stage := s.stageName()
	key := dirKey{root: b.state.path, dir: s.l.dirRel}
	f, err := w.fs.CreateStage(s.l.dir, stage)
	if errors.Is(err, fs.ErrExist) {
		w.log.Warn("mediawrite: a leftover stage of an earlier write is removed", "dir", filepath.Join(b.state.path, s.l.dirRel), "stage", stage)
		if rmErr := w.fs.Remove(s.l.dir, stage); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return "", w.abandon(ctx, s, "", fmt.Errorf("mediawrite: remove the leftover stage of %q: %w", s.name, w.writeErr(ctx, b, rmErr)))
		}
		f, err = w.fs.CreateStage(s.l.dir, stage)
	}
	if err != nil {
		err = w.writeErr(ctx, b, err)
		if denied(err) && !errors.Is(err, ErrRootUnavailable) && s.haveDir {
			w.unwritable.record(key, s.dirID, time.Now())
		}
		return "", w.abandon(ctx, s, "", fmt.Errorf("mediawrite: create the stage of %q: %w", s.name, err))
	}
	w.unwritable.clear(key)
	if err := w.fill(s, f); err != nil {
		return "", w.abandon(ctx, s, stage, fmt.Errorf("mediawrite: write the stage of %q: %w", s.name, w.writeErr(ctx, b, err)))
	}
	w.fs.Step("stage")
	return stage, nil
}

// fill writes the staged bytes, then the modification time, then fsyncs
// the stage and its directory, closing the stage on every path.
func (w *Writer) fill(s *staged, f *os.File) error {
	err := w.fs.Write(f, s.bytes)
	if err == nil {
		err = w.fs.Futimens(f, time.Unix(0, s.mtimeNs))
	}
	if err == nil {
		err = w.fs.Fsync(f)
	}
	if cerr := w.fs.Close(f); err == nil {
		err = cerr
	}
	if err == nil {
		err = w.fs.FsyncDir(s.dirFile)
	}
	return err
}

// removeSameName removes the superseded file holding the chosen name once
// a re-hash shows its listed bytes; one changed since is an outside write,
// and the delivery settles not landed.
func (w *Writer) removeSameName(ctx context.Context, b *borrowed, p *prepared, stage string) error {
	want := p.removes[0].SHA256
	sum, _, err := w.hashFile(p.l.dir, p.name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return w.abandon(ctx, &p.staged, stage, fmt.Errorf("mediawrite: re-hash superseded %q: %w", p.name, err))
	case sum != want:
		return w.abandon(ctx, &p.staged, stage, fmt.Errorf("%w: superseded %q changed before its removal", ErrExpectation, p.name))
	}
	if err := w.fs.Remove(p.l.dir, p.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return w.leave(&p.staged, w.writeErr(ctx, b, err))
	}
	w.fs.Step("same-name-removed")
	return nil
}

// removeHolding removes r's file when it still holds r's bytes; a file gone
// or holding other bytes is left alone.
func (w *Writer) removeHolding(dir *os.Root, r statedb.Removal) error {
	sum, _, err := w.hashFile(dir, r.Name)
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, errTooLarge):
		return nil
	case err != nil:
		return fmt.Errorf("mediawrite: re-hash superseded %q: %w", r.Name, err)
	case sum != r.SHA256:
		return nil
	}
	if err := w.fs.Remove(dir, r.Name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("mediawrite: remove superseded %q: %w", r.Name, err)
	}
	return nil
}

// abandon settles a write that failed before its final mutation: it
// removes stage when this call created one, fsyncs the directory and ends
// the intent not landed, returning cause. A failed step leaves the intent
// for recovery.
func (w *Writer) abandon(ctx context.Context, s *staged, stage string, cause error) error {
	if stage != "" {
		if err := w.fs.Remove(s.l.dir, stage); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return w.leave(s, errors.Join(cause, fmt.Errorf("mediawrite: remove the stage of %q: %w", s.name, err)))
		}
	}
	if err := w.fs.FsyncDir(s.dirFile); err != nil {
		return w.leave(s, errors.Join(cause, err))
	}
	if err := w.end(ctx, s, false); err != nil {
		return w.leave(s, errors.Join(cause, err))
	}
	level := slog.LevelWarn
	if errors.Is(cause, ErrNameContended) {
		level = slog.LevelInfo
	}
	w.log.Log(context.Background(), level, "mediawrite: a write is settled not landed", "instance", s.l.inst, "name", s.name, "err", cause)
	return cause
}

// leave reports a write whose intent stays for recovery, at ERROR when the
// store failed to settle it.
func (w *Writer) leave(s *staged, cause error) error {
	level := slog.LevelWarn
	if errors.Is(cause, errSettle) {
		level = slog.LevelError
	}
	w.log.Log(context.Background(), level, "mediawrite: a write is left for recovery", "instance", s.l.inst, "name", s.name, "err", cause)
	return fmt.Errorf("mediawrite: the write of %q beside instance %d's video is left for recovery: %w", s.name, s.l.inst, cause)
}

// errBegin marks a failed store write staging an intent.
var errBegin = errors.New("mediawrite: stage the intent")

// refused logs a write that failed before its intent at the level the
// error table gives its cause, and returns cause: ERROR for an invalid
// delivery or a failed Begin*, INFO for ErrNameContended, WARN for a
// path-level filesystem error, nothing for the refusals only the caller
// acts on.
func (w *Writer) refused(inst statedb.InstanceID, cause error) error {
	var level slog.Level
	switch {
	case errors.Is(cause, errInvalidDelivery), errors.Is(cause, errBegin):
		level = slog.LevelError
	case errors.Is(cause, ErrNameContended):
		level = slog.LevelInfo
	case pathErr(cause):
		level = slog.LevelWarn
	default:
		return cause
	}
	w.log.Log(context.Background(), level, "mediawrite: a write failed before its intent", "instance", inst, "err", cause)
	return cause
}

// pathErr reports whether err is a filesystem error on a path under an
// available root; ErrOutsideRoot logs where it is decided.
func pathErr(err error) bool {
	var pe *fs.PathError
	var le *os.LinkError
	return (errors.As(err, &pe) || errors.As(err, &le)) && !errors.Is(err, ErrRootUnavailable) && !errors.Is(err, ErrOutsideRoot)
}
