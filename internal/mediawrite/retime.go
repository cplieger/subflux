package mediawrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

// Retime renders ref's baseline under tm over the file, after confirming
// expect under the leases, and returns the landed record. An unmanaged file
// is adopted as a Locked record with no provenance, its bytes the baseline;
// one whose name has no language is ErrInvalidRef. The record keeps its
// Decision and Facts; a non-nil outcome (a sync apply) supplies the rest,
// and nil is a user edit: MethodNone, confidence 0, Verified, tm's offset.
// Equal bytes update only the record. A result past 16 MiB, or a file
// timing cannot parse, is ErrFormatUnsupported with nothing changed.
func (w *Writer) Retime(ctx context.Context, ref FileRef, expect Expectation, tm timing.Timing, outcome *timing.Outcome) (statedb.FileRecord, error) {
	t, rendered, adopt, err := w.retimeInput(ctx, ref, expect, tm)
	if err != nil {
		return statedb.FileRecord{}, w.refused(ref.inst, err)
	}
	defer t.release()
	rec := retimed(t, tm, outcome)
	if bytes.Equal(rendered, t.bytes) {
		rec.Identity = statedb.Identity{Size: t.info.Size(), MtimeNs: t.info.ModTime().UnixNano(), SHA256: t.sum}
		return w.retimeRecord(ctx, &rec, adopt)
	}
	s := staged{l: t.l, name: t.name, bytes: rendered, sum: sha256.Sum256(rendered), mtimeNs: time.Now().Truncate(time.Second).UnixNano(), op: statedb.Retime}
	if err = w.openStaged(&s); err != nil {
		return statedb.FileRecord{}, w.refused(ref.inst, err)
	}
	defer s.close()
	rec.Identity = statedb.Identity{Size: int64(len(rendered)), MtimeNs: s.mtimeNs, SHA256: s.sum}
	if err = w.store.BeginRetime(ctx, rec, adopt); err != nil {
		return statedb.FileRecord{}, w.refused(ref.inst, fmt.Errorf("%w of %q: %w", errBegin, ref.name, err))
	}
	w.fs.Step("begin")
	// Past the intent the call runs to a settled outcome, as Deliver does.
	ctx = context.WithoutCancel(ctx)
	stage, err := w.stage(ctx, t.b, &s)
	if err != nil {
		return statedb.FileRecord{}, err
	}
	if err := w.replace(ctx, t, &s, stage); err != nil {
		return statedb.FileRecord{}, err
	}
	return w.record(ctx, ref.inst, ref.name)
}

// retimeInput takes ref's leases, confirms expect and renders the file
// under tm, returning the held target, the rendered bytes and, for an
// unmanaged file, the baseline its adoption stores; on an error nothing is
// held.
func (w *Writer) retimeInput(ctx context.Context, ref FileRef, expect Expectation, tm timing.Timing) (*target, []byte, *timing.Cues, error) {
	if err := timing.Validate(tm); err != nil {
		return nil, nil, nil, fmt.Errorf("mediawrite: retime %q: %w", ref.name, err)
	}
	t, err := w.acquire(ctx, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	rendered, adopt, err := w.retimeRender(ctx, t, expect, tm)
	if err != nil {
		t.release()
		return nil, nil, nil, err
	}
	return t, rendered, adopt, nil
}

// retimeRender confirms expect on t and renders t's baseline under tm.
func (w *Writer) retimeRender(ctx context.Context, t *target, expect Expectation, tm timing.Timing) ([]byte, *timing.Cues, error) {
	if t.rec == nil && t.parsed.Tag == (language.Tag{}) {
		return nil, nil, fmt.Errorf("%w: %q names no language, so it cannot be adopted", ErrInvalidRef, t.name)
	}
	if err := w.confirm(ctx, t, expect); err != nil {
		return nil, nil, err
	}
	baseline, adopt, err := w.retimeBaseline(ctx, t)
	if err != nil {
		return nil, nil, err
	}
	rendered, err := timing.Render(baseline, tm)
	switch {
	case err != nil:
		return nil, nil, fmt.Errorf("mediawrite: retime %q: %w", t.name, err)
	case len(rendered) > maxSubtitleBytes:
		return nil, nil, fmt.Errorf("%w: %q retimed is over %d bytes", timing.ErrFormatUnsupported, t.name, maxSubtitleBytes)
	}
	return rendered, adopt, nil
}

// retimeBaseline is the record's stored baseline, or for an unmanaged file
// its parsed bytes, which adopt then carries into the store.
func (w *Writer) retimeBaseline(ctx context.Context, t *target) (baseline timing.Cues, adopt *timing.Cues, err error) {
	if t.rec != nil {
		baseline, err = w.baseline(ctx, t)
		return baseline, nil, err
	}
	if baseline, err = timing.ParseCues(t.bytes); err != nil {
		return timing.Cues{}, nil, fmt.Errorf("mediawrite: adopt %q: %w", t.name, err)
	}
	return baseline, &baseline, nil
}

// retimeRecord settles a retime whose bytes are already on disk: only the
// record changes.
func (w *Writer) retimeRecord(ctx context.Context, rec *statedb.FileRecord, adopt *timing.Cues) (statedb.FileRecord, error) {
	if err := w.store.BeginRetime(ctx, *rec, adopt); err != nil {
		return statedb.FileRecord{}, w.refused(rec.Inst, fmt.Errorf("%w of %q: %w", errBegin, rec.Name, err))
	}
	if err := w.fs.End(context.WithoutCancel(ctx), statedb.Retime, rec.Inst, rec.Name, true); err != nil {
		err = fmt.Errorf("%w of %q: %w", errSettle, rec.Name, err)
		w.log.Error("mediawrite: a write is left for recovery", "instance", rec.Inst, "name", rec.Name, "err", err)
		return statedb.FileRecord{}, fmt.Errorf("mediawrite: retime %q of instance %d is left for recovery: %w", rec.Name, rec.Inst, err)
	}
	return w.record(ctx, rec.Inst, rec.Name)
}

// replace renames stage over t's name once a re-hash shows the bytes t
// confirmed: the protocol's one replace. A file changed since is an
// outside write, and the retime settles not landed.
func (w *Writer) replace(ctx context.Context, t *target, s *staged, stage string) error {
	sum, _, err := w.hashFile(t.l.dir, t.name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return w.abandon(ctx, s, stage, fmt.Errorf("%w: %q was removed before its retime", ErrExpectation, t.name))
	case err != nil:
		return w.abandon(ctx, s, stage, fmt.Errorf("mediawrite: re-hash %q: %w", t.name, err))
	case sum != t.sum:
		return w.abandon(ctx, s, stage, fmt.Errorf("%w: %q changed before its retime", ErrExpectation, t.name))
	}
	if err := w.fs.Rename(t.l.dir, stage, t.name); err != nil {
		return w.leave(s, w.writeErr(ctx, t.b, err))
	}
	w.fs.Step("published")
	if err := w.fs.FsyncDir(s.dirFile); err != nil {
		return w.leave(s, err)
	}
	w.fs.Step("synced")
	if err := w.end(ctx, s, true); err != nil {
		return w.leave(s, err)
	}
	return nil
}

// retimed is t's record under tm, or the record adopting t's unmanaged
// file.
func retimed(t *target, tm timing.Timing, outcome *timing.Outcome) statedb.FileRecord {
	if t.rec == nil {
		return statedb.FileRecord{
			Inst: t.l.inst, Name: t.name, Tag: t.parsed.Tag, Flags: t.parsed.Flags, Ordinal: t.parsed.Ordinal, Timing: tm, Locked: true,
		}
	}
	rec := *t.rec
	rec.Timing = tm
	if rec.Provenance != nil {
		p := *rec.Provenance
		next := timing.Outcome{Method: timing.MethodNone, Verdict: timing.Verified, OffsetMs: timing.OffsetMs(tm)}
		if outcome != nil {
			next = *outcome
		}
		next.Decision = p.Timing.Decision
		p.Timing = next
		rec.Provenance = &p
	}
	return rec
}
