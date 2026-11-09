package mediawrite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subtitlefile"
	"github.com/cplieger/subflux/internal/timing"
)

// Timed is a file as Current reads it, all under one confirmed expectation.
// Flags are the record's, or an unmanaged name's as the grammar reads them;
// Outcome is the record's timing outcome, nil for an unmanaged or adopted
// file; Rigid is set when the record's facts say the subtitle is of a
// different cut, whatever Decision it holds, so no later path gives it a
// model past Linear or runs audio for it.
type Timed struct {
	Timing  timing.Timing
	Outcome *timing.Outcome
	Tag     language.Tag
	Cues    timing.Cues
	Flags   language.Flags
	Rigid   bool
}

// Current returns, under one confirmation of expect, a record's baseline,
// timing, outcome, flags and rigidity, or an unmanaged file's parsed bytes
// at Identity with its name's flags.
func (w *Writer) Current(ctx context.Context, ref FileRef, expect Expectation) (Timed, error) {
	t, err := w.acquire(ctx, ref)
	if err != nil {
		return Timed{}, err
	}
	defer t.release()
	err = w.confirm(ctx, t, expect)
	if err != nil {
		return Timed{}, err
	}
	if t.rec == nil {
		var cues timing.Cues
		cues, err = timing.ParseCues(t.bytes)
		if err != nil {
			return Timed{}, fmt.Errorf("mediawrite: read %q: %w", ref.name, err)
		}
		return Timed{Timing: timing.Identity{}, Cues: cues, Tag: t.parsed.Tag, Flags: t.parsed.Flags}, nil
	}
	cues, err := w.baseline(ctx, t)
	if err != nil {
		return Timed{}, err
	}
	out := Timed{Timing: t.rec.Timing, Cues: cues, Tag: t.rec.Tag, Flags: t.rec.Flags}
	if p := t.rec.Provenance; p != nil {
		out.Outcome = new(p.Timing)
		out.Rigid = p.Facts.Cut == rank.Differs
	}
	return out, nil
}

// target is the one file a Retime, Delete or Current acts on, held under
// its instance and path leases until release: its record, or for an
// unmanaged file its name as the grammar reads it, and once confirmed its
// current bytes.
type target struct {
	info    fs.FileInfo
	l       *listing
	b       *borrowed
	rec     *statedb.FileRecord
	unlock  func()
	name    string
	bytes   []byte
	parsed  subtitlefile.Name
	sum     [32]byte
	present bool
}

// acquire maps ref to its root, takes its leases and reads its directory.
// A name the video does not own and no record names, or one the grammar
// cannot read, is ErrInvalidRef; a name a pending write holds is
// ErrPathPending.
func (w *Writer) acquire(ctx context.Context, ref FileRef) (*target, error) {
	if ref.inst == 0 || ValidName(ref.name) != nil {
		return nil, fmt.Errorf("%w: %q of instance %d", ErrInvalidRef, ref.name, ref.inst)
	}
	in, err := w.instance(ctx, ref.inst)
	if err != nil {
		return nil, err
	}
	b, err := w.borrow(ctx, in.VideoPath)
	if err != nil {
		return nil, err
	}
	unlock, err := w.lease(ctx, ref.inst, b.state.path, filepath.Join(filepath.Dir(b.rel), ref.name))
	if err != nil {
		b.release()
		return nil, err
	}
	t := &target{b: b, unlock: unlock, name: ref.name}
	if t.l, err = w.readListing(ctx, b, ref.inst); err != nil {
		unlock()
		b.release()
		return nil, err
	}
	if err := t.check(); err != nil {
		t.release()
		return nil, err
	}
	return t, nil
}

func (t *target) check() error {
	if t.l.pending[t.name] {
		return fmt.Errorf("%w: %q of instance %d", ErrPathPending, t.name, t.l.inst)
	}
	if t.rec = t.l.records[t.name]; t.rec != nil {
		return nil
	}
	mine, _ := claim(t.l.stems, t.l.stem, t.name)
	n, ok := subtitlefile.Parse(t.l.stem, t.name)
	if !mine || !ok {
		return fmt.Errorf("%w: %q is no subtitle of instance %d's video", ErrInvalidRef, t.name, t.l.inst)
	}
	t.parsed = n
	return nil
}

func (t *target) release() {
	_ = t.l.dir.Close()
	t.unlock()
	t.b.release()
}

// confirm checks expect against the file as it is now, always hashed: a
// record's generation and the file's current hash, or generation 0 and the
// current hash with no record. A record whose file is gone or changed loses
// its record and baseline before ErrExpectation, the change being the
// user's. The fresh hash replaces any cached one, so an outside edit within
// one mtime tick costs one ErrExpectation.
func (w *Writer) confirm(ctx context.Context, t *target, expect Expectation) error {
	if expect == (Expectation{}) {
		return fmt.Errorf("%w: %q was listed without its hash", ErrExpectation, t.name)
	}
	if err := w.read(t); err != nil {
		return err
	}
	if t.rec != nil {
		if !t.present || t.sum != t.rec.Identity.SHA256 {
			if err := w.dropRecords(ctx, t.l.inst, []*statedb.FileRecord{t.rec}); err != nil {
				return err
			}
			return fmt.Errorf("%w: %q changed outside subflux", ErrExpectation, t.name)
		}
		if expect != (Expectation{Generation: t.rec.Generation, SHA256: t.sum}) {
			return fmt.Errorf("%w: %q is at generation %d", ErrExpectation, t.name, t.rec.Generation)
		}
		return nil
	}
	if !t.present || expect != (Expectation{SHA256: t.sum}) {
		return fmt.Errorf("%w: %q changed since it was listed", ErrExpectation, t.name)
	}
	return nil
}

// read loads t's file, refusing a symlink and reading at most
// maxSubtitleBytes; a file gone or too large is not present, since no
// expectation can name it.
func (w *Writer) read(t *target) error {
	f, info, err := w.fs.OpenRead(t.l.dir, t.name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mediawrite: open %q of instance %d: %w", t.name, t.l.inst, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSubtitleBytes+1))
	switch {
	case err != nil:
		return fmt.Errorf("mediawrite: read %q of instance %d: %w", t.name, t.l.inst, err)
	case len(b) > maxSubtitleBytes:
		return nil
	}
	t.bytes, t.info, t.sum, t.present = b, info, sha256.Sum256(b), true
	if int64(len(b)) == info.Size() {
		w.hashes.put(hashKey{root: t.b.state.path, path: filepath.Join(t.l.dirRel, t.name), size: info.Size(), mtimeNs: info.ModTime().UnixNano(), inode: inodeOf(info)}, t.sum)
	}
	return nil
}

// baseline is t's record's stored baseline.
func (w *Writer) baseline(ctx context.Context, t *target) (timing.Cues, error) {
	cues, found, err := w.store.Baseline(ctx, t.l.inst, t.name)
	switch {
	case err != nil:
		return timing.Cues{}, fmt.Errorf("mediawrite: read the baseline of %q: %w", t.name, err)
	case !found:
		return timing.Cues{}, fmt.Errorf("mediawrite: %q of instance %d has a record but no baseline", t.name, t.l.inst)
	}
	return cues, nil
}
