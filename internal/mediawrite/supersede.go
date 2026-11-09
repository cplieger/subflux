package mediawrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
)

// removable is the supersedes of p's delivery it may remove, in the
// delivery's order: each a managed file still at its listed generation and
// bytes, and none that another configured pair grades Exact or holds as
// its only Substitute, counting the new file. A supersede a pending write
// names is ErrPathPending.
func (w *Writer) removable(ctx context.Context, p *prepared, in *statedb.Instance) ([]statedb.Removal, error) {
	var set []statedb.Removal
	var recs []*statedb.FileRecord
	for _, s := range p.d.Supersedes {
		if p.l.pending[s.File.name] {
			return nil, fmt.Errorf("%w: supersede %q of instance %d", ErrPathPending, s.File.name, p.d.Inst)
		}
		if rec := w.confirmed(p, s); rec != nil && indexOf(set, rec.Name) < 0 {
			set = append(set, statedb.Removal{Name: rec.Name, SHA256: rec.Identity.SHA256})
			recs = append(recs, rec)
		}
	}
	if len(set) == 0 {
		return nil, nil
	}
	pairs, err := w.otherPairs(ctx, in, p.d)
	if err != nil {
		return nil, err
	}
	kept := slices.DeleteFunc(p.l.held(), func(f statedb.LocalFile) bool { return indexOf(set, f.Name) >= 0 })
	kept = append(kept, statedb.LocalFile{Tag: p.d.Tag, Flags: p.d.Flags})
	policy := w.policy()
	out := set[:0]
	for i, r := range set {
		f := statedb.LocalFile{Name: r.Name, Tag: recs[i].Tag, Flags: recs[i].Flags}
		if needed(policy, pairs, &f, kept) {
			kept = append(kept, f)
			w.log.Info("mediawrite: a superseded subtitle stays: another configured language needs it", "instance", p.d.Inst, "name", r.Name)
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// confirmed is s's record when the file still holds the generation and
// bytes s was listed at; nil when it moved, is gone or is unmanaged.
func (w *Writer) confirmed(p *prepared, s Superseded) *statedb.FileRecord {
	rec := p.l.records[s.File.name]
	if rec == nil || s.Expect != (Expectation{Generation: rec.Generation, SHA256: rec.Identity.SHA256}) {
		return nil
	}
	sum, _, err := w.hashFile(p.l.dir, rec.Name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, errTooLarge) {
		w.log.Warn("mediawrite: hashing a superseded subtitle failed; it stays", "instance", p.d.Inst, "name", rec.Name, "err", err)
	}
	if err != nil || sum != s.Expect.SHA256 {
		return nil
	}
	return rec
}

// otherPairs is every pair the instance's items configure but d's own.
func (w *Writer) otherPairs(ctx context.Context, in *statedb.Instance, d *Delivery) ([]statedb.CoverageRow, error) {
	var pairs []statedb.CoverageRow
	for _, k := range in.Items {
		rows, err := w.store.Coverage(ctx, k)
		if err != nil {
			return nil, fmt.Errorf("mediawrite: read the configured pairs of instance %d: %w", d.Inst, err)
		}
		for _, r := range rows {
			same := func(q statedb.CoverageRow) bool { return q.Target == r.Target && q.Variant == r.Variant }
			if (r.Target != d.Target || r.Variant != d.Variant) && !slices.ContainsFunc(pairs, same) {
				pairs = append(pairs, r)
			}
		}
	}
	return pairs, nil
}

// needed reports whether removing f would cost a pair: f serves it Exact,
// or as a Substitute that none of others matches. Embedded tracks are not
// counted: the writer cannot tell which codecs coverage ignores, and
// keeping a file is the safe error.
func needed(p language.Policy, pairs []statedb.CoverageRow, f *statedb.LocalFile, others []statedb.LocalFile) bool {
	for i := range pairs {
		pr := &pairs[i]
		if !f.Flags.Serves(pr.Variant) {
			continue
		}
		switch p.Grade(pr.Target, f.Tag).Class {
		case language.Exact:
			return true
		case language.Substitute:
			if !servedBy(p, pr, others) {
				return true
			}
		case language.ManualOnly, language.ClassUnrelated:
		}
	}
	return false
}

// servedBy reports whether some file of files serves pr Exact or as a
// Substitute.
func servedBy(p language.Policy, pr *statedb.CoverageRow, files []statedb.LocalFile) bool {
	for i := range files {
		if !files[i].Flags.Serves(pr.Variant) {
			continue
		}
		if c := p.Grade(pr.Target, files[i].Tag).Class; c == language.Exact || c == language.Substitute {
			return true
		}
	}
	return false
}
