package mediawrite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subtitlefile"
	"github.com/cplieger/subflux/internal/timing"
)

// maxNameTries bounds the numbered names one Deliver tries.
const maxNameTries = 8

// Delivery is one subtitle a search run delivers for an instance. Tag and
// Flags are the candidate's own, so a substitute says on disk what it is;
// Target and Variant are what it serves, and Lock locks that pair on every
// item of the instance. Candidate keys the delivered attempt in the attempt
// memory; Rejected are the run's other attempts, all undelivered.
type Delivery struct {
	Timing     timing.Timing
	Target     language.Tag
	Tag        language.Tag
	Supersedes []Superseded
	Rejected   []statedb.Download
	Cues       timing.Cues
	Provenance statedb.Provenance
	Attempt    int
	Inst       statedb.InstanceID
	Candidate  [32]byte
	Run        statedb.RunID
	Flags      language.Flags
	Variant    language.Variant
	Lock       bool
	Numbered   bool
}

// Superseded is a file a delivery replaces, as the caller listed it.
type Superseded struct {
	File   FileRef
	Expect Expectation
}

var errInvalidDelivery = errors.New("mediawrite: invalid delivery")

// Deliver cleans and renders d under the post_processing section, publishes
// it beside the instance's video without replacing any file it cannot
// account for, removes each superseded file still holding its listed bytes
// that no other configured pair needs, and returns the landed record. It
// records d's attempt rows exactly once: through RecordAttempts when it
// fails before staging its intent and d's Run and Attempt are valid, else
// when the intent settles, here or in recovery. ErrNameContended is a name
// an outside writer took or eight numbered names all taken.
func (w *Writer) Deliver(ctx context.Context, d Delivery) (statedb.FileRecord, error) { //nolint:gocritic // hugeParam: the delivery is the caller's value
	in, err := w.checkDelivery(ctx, &d)
	if err != nil {
		return statedb.FileRecord{}, w.unbegun(ctx, &d, &in, err)
	}
	b, err := w.borrow(ctx, in.VideoPath)
	if err != nil {
		return statedb.FileRecord{}, w.unbegun(ctx, &d, &in, err)
	}
	defer b.release()
	unlock, err := w.lease(ctx, d.Inst, b.state.path)
	if err != nil {
		return statedb.FileRecord{}, w.unbegun(ctx, &d, &in, err)
	}
	defer unlock()
	p, err := w.prepare(ctx, b, &d, &in)
	if err != nil {
		return statedb.FileRecord{}, w.unbegun(ctx, &d, &in, err)
	}
	defer p.close()
	unlockPaths, err := w.lockPaths(ctx, b.state.path, p.leased()...)
	if err != nil {
		return statedb.FileRecord{}, w.unbegun(ctx, &d, &in, err)
	}
	defer unlockPaths()
	if err := w.store.BeginDeliver(ctx, p.pending()); err != nil {
		return statedb.FileRecord{}, w.unbegun(ctx, &d, &in, fmt.Errorf("%w of %q: %w", errBegin, p.name, err))
	}
	w.fs.Step("begin")
	// Past the intent the call runs to a settled outcome: a cancelled
	// caller would otherwise leave the instance pinned until recovery.
	ctx = context.WithoutCancel(ctx)
	if err := w.land(ctx, b, p); err != nil {
		return statedb.FileRecord{}, err
	}
	return w.record(ctx, d.Inst, p.name)
}

// checkDelivery refuses a delivery the writer cannot act on and returns its
// instance when the store knows it.
func (w *Writer) checkDelivery(ctx context.Context, d *Delivery) (statedb.Instance, error) {
	in, err := w.instance(ctx, d.Inst)
	if errors.Is(err, ErrInvalidRef) {
		return statedb.Instance{}, fmt.Errorf("%w: %w", errInvalidDelivery, err)
	}
	if err != nil {
		return statedb.Instance{}, err
	}
	for _, s := range d.Supersedes {
		if s.File.inst != d.Inst {
			return in, fmt.Errorf("%w: %w: supersede %q is of instance %d, not %d", errInvalidDelivery, ErrInvalidRef, s.File.name, s.File.inst, d.Inst)
		}
	}
	switch {
	case d.Tag == (language.Tag{}), d.Target == (language.Tag{}):
		return in, fmt.Errorf("%w: no language tag or target", errInvalidDelivery)
	case !d.Flags.Serves(d.Variant):
		return in, fmt.Errorf("%w: flags %+v do not serve variant %d", errInvalidDelivery, d.Flags, d.Variant)
	case !d.rowsValid():
		return in, fmt.Errorf("%w: run, attempt or rejected attempts out of range", errInvalidDelivery)
	}
	if err := timing.Validate(d.Timing); err != nil {
		return in, fmt.Errorf("%w: %w", errInvalidDelivery, err)
	}
	return in, nil
}

// rowsValid reports whether d's attempt rows can be stored: a run, an
// attempt from 1 to 3, and rejected attempts of this instance, undelivered,
// each naming an item.
func (d *Delivery) rowsValid() bool {
	if d.Run == (statedb.RunID{}) || d.Attempt < 1 || d.Attempt > 3 {
		return false
	}
	for i := range d.Rejected {
		if r := &d.Rejected[i]; r.Inst != d.Inst || r.Delivered || len(r.Items) == 0 {
			return false
		}
	}
	return true
}

// attempts is d's rows: the delivered attempt first, then Rejected.
func (d *Delivery) attempts(items []media.Key, at time.Time) []statedb.Download {
	rows := make([]statedb.Download, 0, 1+len(d.Rejected))
	rows = append(rows, statedb.Download{
		At: at, Target: d.Target, Variant: d.Variant, Items: items, Provenance: d.Provenance,
		Attempt: d.Attempt, Inst: d.Inst, Run: d.Run, Candidate: d.Candidate, Delivered: true,
	})
	return append(rows, d.Rejected...)
}

// unbegun records d's attempts undelivered for a failure before its intent
// and returns cause; a delivery whose rows cannot be stored records nothing.
func (w *Writer) unbegun(ctx context.Context, d *Delivery, in *statedb.Instance, cause error) error {
	cause = w.refused(d.Inst, cause)
	if !d.rowsValid() || len(in.Items) == 0 {
		return cause
	}
	rows := d.attempts(in.Items, time.Now())
	rows[0].Delivered = false
	if errors.Is(cause, timing.ErrFormatUnsupported) {
		rows[0].Refusal = "format_unsupported"
	}
	if err := w.store.RecordAttempts(context.WithoutCancel(ctx), rows); err != nil {
		return errors.Join(cause, fmt.Errorf("mediawrite: record the attempts of instance %d: %w", d.Inst, err))
	}
	return cause
}

// record reads back the landed record of name.
func (w *Writer) record(ctx context.Context, inst statedb.InstanceID, name string) (statedb.FileRecord, error) {
	recs, err := w.store.Files(ctx, inst)
	if err != nil {
		return statedb.FileRecord{}, fmt.Errorf("mediawrite: read back %q of instance %d: %w", name, inst, err)
	}
	for i := range recs {
		if recs[i].Name == name {
			return recs[i], nil
		}
	}
	return statedb.FileRecord{}, fmt.Errorf("mediawrite: %q of instance %d landed with no record", name, inst)
}

// prepared is a delivery ready to stage: its write, record and baseline,
// and the supersedes it removes.
type prepared struct {
	d        *Delivery
	items    []media.Key
	removes  []statedb.Removal
	baseline timing.Cues
	rec      statedb.FileRecord
	staged
	sameName bool // a supersede holds name and is removed before the publish
}

func (p *prepared) close() {
	p.staged.close()
	_ = p.l.dir.Close()
}

// leased is every path of the delivery under its root: the chosen name and
// each superseded name.
func (p *prepared) leased() []string {
	rels := make([]string, 0, 1+len(p.removes))
	rels = append(rels, filepath.Join(p.l.dirRel, p.name))
	for _, r := range p.removes {
		rels = append(rels, filepath.Join(p.l.dirRel, r.Name))
	}
	return rels
}

func (p *prepared) pending() statedb.PendingDeliver {
	removes := p.removes
	if p.sameName {
		removes = removes[1:]
	}
	return statedb.PendingDeliver{Record: p.rec, Baseline: p.baseline, Attempts: p.d.attempts(p.items, time.Now()), Removes: removes}
}

// prepare runs steps 2 to 5 of a delivery under the instance lease: render,
// keep the unsafe supersedes, choose the name and fix the identity.
func (w *Writer) prepare(ctx context.Context, b *borrowed, d *Delivery, in *statedb.Instance) (*prepared, error) {
	baseline, rendered, err := w.render(d)
	if err != nil {
		return nil, err
	}
	l, err := w.readListing(ctx, b, d.Inst)
	if err != nil {
		return nil, err
	}
	p := &prepared{
		l: l, bytes: rendered, sum: sha256.Sum256(rendered), mtimeNs: time.Now().Truncate(time.Second).UnixNano(), op: statedb.Deliver,
		d: d, baseline: baseline, items: in.Items,
	}
	if err := w.openStaged(&p.staged); err != nil {
		_ = l.dir.Close()
		return nil, err
	}
	if err := w.choose(ctx, p, in); err != nil {
		p.close()
		return nil, err
	}
	n, _ := subtitlefile.Parse(l.stem, p.name)
	p.rec = statedb.FileRecord{
		Inst: d.Inst, Name: p.name, Tag: d.Tag, Flags: d.Flags, Ordinal: n.Ordinal, Timing: d.Timing,
		Provenance: &d.Provenance, Locked: d.Lock,
		Identity: statedb.Identity{Size: int64(len(rendered)), MtimeNs: p.mtimeNs, SHA256: p.sum},
	}
	return p, nil
}

// render cleans d's cues into the baseline and renders it under d's
// timing; either over maxSubtitleBytes is ErrFormatUnsupported, since no
// expectation read could open it.
func (w *Writer) render(d *Delivery) (baseline timing.Cues, rendered []byte, err error) {
	baseline, err = timing.Clean(d.Cues, CleanOptions(*w.cfg.Load()), d.Tag)
	if err != nil {
		return timing.Cues{}, nil, fmt.Errorf("mediawrite: clean the delivery: %w", err)
	}
	doc, err := timing.Render(baseline, timing.Identity{})
	if err == nil && len(doc) <= maxSubtitleBytes {
		rendered, err = timing.Render(baseline, d.Timing)
	}
	switch {
	case err != nil:
		return timing.Cues{}, nil, fmt.Errorf("mediawrite: render the delivery: %w", err)
	case len(doc) > maxSubtitleBytes, len(rendered) > maxSubtitleBytes:
		return timing.Cues{}, nil, fmt.Errorf("%w: the subtitle is over %d bytes", timing.ErrFormatUnsupported, maxSubtitleBytes)
	}
	return baseline, rendered, nil
}

// choose keeps every supersede that changed or that another configured
// pair still needs, then picks the name: unnumbered unless d asks for a
// number, when free or held by a supersede still removed; else the next
// reserved ordinal whose name is free.
func (w *Writer) choose(ctx context.Context, p *prepared, in *statedb.Instance) error {
	set, err := w.removable(ctx, p, in)
	if err != nil {
		return err
	}
	ext := subtitlefile.ExtOf(p.baseline.Format())
	plain, err := subtitlefile.Build(p.l.stem, subtitlefile.Name{Tag: p.d.Tag, Flags: p.d.Flags, Ext: ext})
	if err != nil {
		return fmt.Errorf("mediawrite: name the delivery: %w", err)
	}
	p.removes = set
	if !p.d.Numbered {
		if i := indexOf(set, plain); i >= 0 {
			set[0], set[i] = set[i], set[0]
			p.name, p.sameName = plain, true
			return nil
		}
		if p.l.pending[plain] {
			return fmt.Errorf("%w: %q of instance %d", ErrPathPending, plain, p.d.Inst)
		}
		var free bool
		if free, err = w.free(p.l, plain); err != nil {
			return err
		}
		if free {
			p.name = plain
			return nil
		}
	}
	p.name, err = w.numbered(ctx, p.l, p.d, ext)
	return err
}

// numbered reserves ordinals above the highest on disk for d's tag and
// flags until one names a free file, at most maxNameTries times.
func (w *Writer) numbered(ctx context.Context, l *listing, d *Delivery, ext subtitlefile.Ext) (string, error) {
	highest := 0
	for _, e := range l.entries {
		if n, ok := subtitlefile.Parse(l.stem, e.Name()); ok && n.Tag == d.Tag && n.Flags == d.Flags {
			highest = max(highest, n.Ordinal)
		}
	}
	key := statedb.OrdinalKey{Inst: d.Inst, Tag: d.Tag, Flags: d.Flags}
	for range maxNameTries {
		n, err := w.store.ReserveOrdinal(ctx, key, highest)
		if err != nil {
			return "", fmt.Errorf("mediawrite: reserve an ordinal for instance %d: %w", d.Inst, err)
		}
		name, err := subtitlefile.Build(l.stem, subtitlefile.Name{Tag: d.Tag, Flags: d.Flags, Ordinal: n, Ext: ext})
		if err != nil {
			return "", fmt.Errorf("mediawrite: name the delivery: %w", err)
		}
		if l.pending[name] {
			continue
		}
		free, err := w.free(l, name)
		if err != nil {
			return "", err
		}
		if free {
			return name, nil
		}
	}
	return "", fmt.Errorf("%w: %d numbered names beside instance %d's video are taken", ErrNameContended, maxNameTries, d.Inst)
}

// free reports whether Lstat finds nothing at name, so on a
// case-insensitive share Movie.EN.srt takes movie.en.srt.
func (w *Writer) free(l *listing, name string) (bool, error) {
	_, err := w.fs.Lstat(l.dir, name)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	}
	return false, fmt.Errorf("mediawrite: check %q beside instance %d's video: %w", name, l.inst, err)
}

func indexOf(set []statedb.Removal, name string) int {
	for i, r := range set {
		if r.Name == name {
			return i
		}
	}
	return -1
}
