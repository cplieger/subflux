package statedb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/timing"
)

// RunID names one search run, which numbers its attempts from 1.
type RunID [16]byte

// Provenance is where a managed file came from and how its timing was
// decided. A user's upload is Manual and Uploaded with no Provider, and is
// matched and fitted by nothing.
type Provenance struct {
	Provider, Release string
	Timing            timing.Outcome
	Facts             rank.Facts
	MatchedBy         rank.MatchedBy
	Fit               rank.Fit
	Manual            bool
	Uploaded          bool
	MachineTranslated bool
}

// Download is one attempt of a run at one target for an instance's items,
// delivered or not. Refusal is stored as given; Candidate tells one
// candidate from another with the same release name.
type Download struct {
	At         time.Time
	Target     language.Tag
	Refusal    string
	Items      []media.Key
	Provenance Provenance
	Attempt    int
	Inst       InstanceID
	Run        RunID
	Candidate  [32]byte
	Variant    language.Variant
	Delivered  bool
}

var errNoItems = errors.New("statedb: a download names no item")

// RecordAttempts appends ds to the download log as undelivered attempts in
// one write. A delivered attempt is EndDeliver's to record and is refused,
// as is an attempt naming no item, writing nothing.
func (s *Store) RecordAttempts(ctx context.Context, ds []Download) error {
	rows := make([]downloadRow, 0, len(ds))
	for i := range ds {
		if ds[i].Delivered {
			return errors.New("statedb: RecordAttempts records undelivered attempts only")
		}
		row, err := downloadRowOf(&ds[i])
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	return s.write(ctx, "record attempts", s.db.Update, func(tx *bolttable.Tx) error {
		return s.appendDownloads(tx, rows)
	})
}

// appendDownloads appends each row under the next sequence; no write
// rewrites or deletes one (DECISIONS 1).
func (s *Store) appendDownloads(tx *bolttable.Tx, rows []downloadRow) error {
	for i := range rows {
		seq, err := s.t.downloads.NextID(tx)
		if err != nil {
			return err
		}
		if err := s.t.downloads.Put(tx, seq, &rows[i]); err != nil {
			return err
		}
	}
	return nil
}

func downloadRowOf(d *Download) (downloadRow, error) {
	if len(d.Items) == 0 {
		return downloadRow{}, errNoItems
	}
	items := make([]rowKey, 0, len(d.Items))
	for _, k := range d.Items {
		items = append(items, rowKey{k: k})
	}
	return downloadRow{
		At: d.At.UTC().Round(0), Target: rowTag{t: d.Target}, Refusal: d.Refusal, Items: items,
		Provenance: provenanceRowOf(&d.Provenance), Attempt: d.Attempt, Inst: uint64(d.Inst),
		Run: id16(d.Run), Candidate: sum32(d.Candidate), Variant: uint8(d.Variant), Delivered: d.Delivered,
	}, nil
}

func (r *downloadRow) download() Download {
	items := make([]media.Key, 0, len(r.Items))
	for _, x := range r.Items {
		items = append(items, x.k)
	}
	return Download{
		At: r.At, Target: r.Target.t, Refusal: r.Refusal, Items: items, Provenance: r.Provenance.provenance(),
		Attempt: r.Attempt, Inst: InstanceID(r.Inst), Run: RunID(r.Run), Candidate: r.Candidate,
		Variant: language.Variant(r.Variant), Delivered: r.Delivered,
	}
}

func provenanceRowOf(p *Provenance) provenanceRow {
	f, o := p.Facts, p.Timing
	return provenanceRow{
		Provider: p.Provider, Release: p.Release, MatchedBy: uint8(p.MatchedBy), Fit: uint8(p.Fit),
		Manual: p.Manual, Uploaded: p.Uploaded, MachineTranslated: p.MachineTranslated,
		Facts: factsRow{
			Source: uint8(f.Source), Group: uint8(f.Group), Service: uint8(f.Service), Cut: uint8(f.Cut), Rate: uint8(f.Rate),
			Hash: f.Hash, PreRelease: f.PreRelease, SeasonPack: f.SeasonPack, EpisodeByNumber: f.EpisodeByNumber,
		},
		Timing: outcomeRow{
			Confidence: o.Confidence, OffsetMs: o.OffsetMs, Action: uint8(o.Decision.Action), Skip: uint8(o.Decision.Skip),
			Method: uint8(o.Method), Verdict: uint8(o.Verdict),
		},
	}
}

func (r *provenanceRow) provenance() Provenance {
	f, o := r.Facts, r.Timing
	return Provenance{
		Provider: r.Provider, Release: r.Release, MatchedBy: rank.MatchedBy(r.MatchedBy), Fit: rank.Fit(r.Fit),
		Manual: r.Manual, Uploaded: r.Uploaded, MachineTranslated: r.MachineTranslated,
		Facts: rank.Facts{
			Source: rank.Tri(f.Source), Group: rank.Tri(f.Group), Service: rank.Tri(f.Service), Cut: rank.Tri(f.Cut), Rate: rank.Tri(f.Rate),
			Hash: f.Hash, PreRelease: f.PreRelease, SeasonPack: f.SeasonPack, EpisodeByNumber: f.EpisodeByNumber,
		},
		Timing: timing.Outcome{
			Confidence: o.Confidence, OffsetMs: o.OffsetMs, Decision: timing.Decision{Action: timing.Action(o.Action), Skip: timing.SkipReason(o.Skip)},
			Method: timing.Method(o.Method), Verdict: timing.Verdict(o.Verdict),
		},
	}
}

// checkAttempts refuses staged attempts that name another instance than
// inst; the first, whose items a locked record locks, must name only items
// inst holds.
func checkAttempts(ds []Download, inst InstanceID, held []rowKey) error {
	if len(ds) == 0 {
		return errors.New("statedb: a delivery stages at least its delivered attempt")
	}
	for i := range ds {
		if ds[i].Inst != inst {
			return fmt.Errorf("statedb: attempt %d is for instance %d, not %d", i, ds[i].Inst, inst)
		}
	}
	for _, k := range ds[0].Items {
		if !slices.ContainsFunc(held, func(x rowKey) bool { return x.k == k }) {
			return fmt.Errorf("statedb: the delivered attempt names %s, which instance %d does not hold", k, inst)
		}
	}
	return nil
}
