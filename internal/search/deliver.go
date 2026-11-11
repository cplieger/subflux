package search

import (
	"context"
	"fmt"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
)

// deliver publishes j's aligned attempt a, whose row is row, as the pair's
// file with a's flags and t's other attempts as Rejected; Deliver records
// every row and its error is returned as it is.
func (e *Engine) deliver(ctx context.Context, t *trial, j *judged, row *statedb.Download, a *aligned) error {
	c := j.cand
	d := mediawrite.Delivery{
		Timing: a.Timing, Cues: a.Cues, Target: t.want.target, Variant: t.want.variant, Tag: c.Lang, Flags: a.flags,
		Rejected: t.rows, Provenance: row.Provenance, Attempt: row.Attempt, Inst: row.Inst, Candidate: row.Candidate,
		Run: row.Run, Numbered: t.manual, Lock: t.manual,
	}
	if !t.manual {
		d.Supersedes = t.r.supersedes(t.want.pair)
	}
	if _, err := e.deliverTo(ctx, d); err != nil {
		return fmt.Errorf("search: deliver instance %d's %s: %w", row.Inst, t.want.target, err)
	}
	e.log.Info("search: delivered", "instance", row.Inst, "target", t.want.target, "provider", row.Provenance.Provider,
		"release", c.Release, "fit", j.fit, "verdict", a.Outcome.Verdict, "manual", t.manual)
	return nil
}

// supersedes is every managed, automatic record serving p, at the
// expectation List read it with.
func (r *run) supersedes(p pair) []mediawrite.Superseded {
	h := &r.held
	var out []mediawrite.Superseded
	for i := range h.inv.Files {
		f := &h.inv.Files[i]
		if !f.Managed {
			continue
		}
		for k := range h.records {
			rec := &h.records[k]
			if rec.Name != f.File.Name() || !supersedableRecord(rec) {
				continue
			}
			if _, ok := h.serves(p, rec.Tag, rec.Flags); ok {
				out = append(out, mediawrite.Superseded{File: f.File, Expect: f.Expect})
			}
		}
	}
	return out
}
