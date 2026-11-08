package statedb

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/timing"
)

// FileRecord is one file subflux manages beside an instance's video, at the
// Generation its last landed write gave it. Provenance is nil for a file
// adopted from the user.
type FileRecord struct {
	Timing     timing.Timing
	Provenance *Provenance
	Name       string
	Tag        language.Tag
	Identity   Identity
	Ordinal    int
	Inst       InstanceID
	Generation uint64
	Flags      language.Flags
	Locked     bool
}

// Identity is a file's size, modification time and content hash as written.
type Identity struct {
	Size, MtimeNs int64
	SHA256        [32]byte
}

// Intent is one pending write: the operation, the file it names and the
// content hash its outcome is judged by.
type Intent struct {
	Name   string
	Inst   InstanceID
	SHA256 [32]byte
	Op     Op
}

// Removal names a file a delivery supersedes, with the bytes it must still
// hold to be removed.
type Removal struct {
	Name   string
	SHA256 [32]byte
}

// PendingDeliver is what a delivery stages: the record, its baseline cues,
// its attempts (the delivered one first) and the files it supersedes.
type PendingDeliver struct {
	Baseline timing.Cues
	Attempts []Download
	Removes  []Removal
	Record   FileRecord
}

var errNoBaseline = errors.New("statedb: the baseline holds no parsed cues")

// errPinned refuses a change to an instance a pending write pins (design 7).
var errPinned = errors.New("statedb: instance has a pending write")

// BeginDeliver stages d under its record's name: nothing live changes until
// EndDeliver. It refuses, writing nothing, a name with a pending intent, an
// unknown instance, a Timing Validate refuses, the zero baseline, no
// attempt, an attempt for another instance, and a delivered attempt naming
// an item the instance does not hold.
func (s *Store) BeginDeliver(ctx context.Context, d PendingDeliver) error { //nolint:gocritic // hugeParam: the staged rows are the caller's values
	if d.Baseline.Format() == 0 {
		return errNoBaseline
	}
	rec, err := fileRowOf(&d.Record)
	if err != nil {
		return err
	}
	attempts := make([]downloadRow, 0, len(d.Attempts))
	for i := range d.Attempts {
		row, err := downloadRowOf(&d.Attempts[i])
		if err != nil {
			return err
		}
		attempts = append(attempts, row)
	}
	removes := make([]removalRow, 0, len(d.Removes))
	for _, r := range d.Removes {
		removes = append(removes, removalRow{Name: r.Name, SHA256: r.SHA256})
	}
	row := intentRow{
		Op: uint8(Deliver), Record: &rec, Baseline: timing.AppendCues(nil, d.Baseline), Attempts: attempts, Removes: removes,
		SHA256: rec.Identity.SHA256,
	}
	return s.begin(ctx, "begin deliver", fileKey{Inst: d.Record.Inst, Name: d.Record.Name}, &row, func(_ *bolttable.ReadTx, inst *instanceRow) error {
		return checkAttempts(d.Attempts, d.Record.Inst, inst.Items)
	})
}

// BeginRetime stages rec, the retimed record of a managed file, or with
// adopt the baseline of an unmanaged file rec adopts. It refuses, writing
// nothing, a name with a pending intent, an unknown instance, a Timing
// Validate refuses, a retime of a name with no record, and an adoption of
// one that has a record or of the zero baseline.
func (s *Store) BeginRetime(ctx context.Context, rec FileRecord, adopt *timing.Cues) error { //nolint:gocritic // hugeParam: the record is the caller's value
	if adopt != nil && adopt.Format() == 0 {
		return errNoBaseline
	}
	fr, err := fileRowOf(&rec)
	if err != nil {
		return err
	}
	row := intentRow{Op: uint8(Retime), Record: &fr, SHA256: fr.Identity.SHA256}
	if adopt != nil {
		row.Baseline = timing.AppendCues(nil, *adopt)
	}
	fk := fileKey{Inst: rec.Inst, Name: rec.Name}
	return s.begin(ctx, "begin retime", fk, &row, func(r *bolttable.ReadTx, _ *instanceRow) error {
		_, managed, err := s.t.files.Get(r, fk)
		switch {
		case err != nil:
			return err
		case managed && adopt != nil:
			return fmt.Errorf("statedb: %q of instance %d is managed; adopting it is refused", rec.Name, rec.Inst)
		case !managed && adopt == nil:
			return fmt.Errorf("statedb: %q of instance %d has no record to retime", rec.Name, rec.Inst)
		}
		return nil
	})
}

// BeginDelete stages the removal of name, which must hold removed when the
// removal lands. It refuses, writing nothing, a name with a pending intent
// and an unknown instance.
func (s *Store) BeginDelete(ctx context.Context, inst InstanceID, name string, removed [32]byte) error {
	row := intentRow{Op: uint8(Delete), SHA256: removed}
	return s.begin(ctx, "begin delete", fileKey{Inst: inst, Name: name}, &row, nil)
}

// begin stores row as fk's intent after check, which reads under the same
// write; only the silent intents table changes.
func (s *Store) begin(ctx context.Context, op string, fk fileKey, row *intentRow, check func(*bolttable.ReadTx, *instanceRow) error) error {
	if fk.Name == "" {
		return fmt.Errorf("statedb: %s: no file name", op)
	}
	return s.write(ctx, op, s.db.Update, func(tx *bolttable.Tx) error {
		r := tx.Read()
		inst, known, err := s.t.instances.Get(r, fk.Inst)
		if err != nil {
			return err
		}
		if !known {
			return fmt.Errorf("statedb: %s: instance %d is unknown", op, fk.Inst)
		}
		_, pending, err := s.t.intents.Get(r, fk)
		if err != nil {
			return err
		}
		if pending {
			return fmt.Errorf("statedb: %s: %q of instance %d already has a pending write", op, fk.Name, fk.Inst)
		}
		if check != nil {
			if err := check(r, &inst); err != nil {
				return err
			}
		}
		return s.t.intents.Put(tx, fk, row)
	})
}

// EndDeliver settles name's pending delivery in one write. Landed, it
// writes the record one generation above the stored one (1 for none), its
// baseline, a lock on every item of the delivered attempt when the record
// is locked, every attempt, and removes each superseded name other than the
// record's own, then recomputes the instance's coverage. Not landed, it
// records the attempts undelivered. With no pending delivery it writes
// nothing and returns nil, so recovery may repeat it.
func (s *Store) EndDeliver(ctx context.Context, inst InstanceID, name string, landed bool) error {
	return s.end(ctx, "end deliver", Deliver, fileKey{Inst: inst, Name: name}, landed, s.landDeliver)
}

// EndRetime settles name's pending retime in one write. Landed, it writes
// the record one generation above the stored one and, for an adoption, its
// baseline and a lock on every configured pair the adopted file makes
// servable, then recomputes the instance's coverage. With no pending retime
// it writes nothing and returns nil.
func (s *Store) EndRetime(ctx context.Context, inst InstanceID, name string, landed bool) error {
	return s.end(ctx, "end retime", Retime, fileKey{Inst: inst, Name: name}, landed, s.landRetime)
}

// EndDelete settles name's pending removal in one write. Landed, it deletes
// the record and baseline, or for an unmanaged name removes it from the
// local-file set, then recomputes the instance's coverage. With no pending
// removal it writes nothing and returns nil.
func (s *Store) EndDelete(ctx context.Context, inst InstanceID, name string, landed bool) error {
	return s.end(ctx, "end delete", Delete, fileKey{Inst: inst, Name: name}, landed, s.landDelete)
}

type lander func(tx *bolttable.Tx, a *activation, fk fileKey, ir *intentRow) error

// end clears fk's intent of op want and, landed, applies it under the
// activation in force; a landed End before the first Activate is refused,
// since its recompute needs the policy.
func (s *Store) end(ctx context.Context, op string, want Op, fk fileKey, landed bool, land lander) error {
	s.actMu.RLock()
	defer s.actMu.RUnlock()
	a := s.active.Load()
	return s.write(ctx, op, s.db.Update, func(tx *bolttable.Tx) error {
		ir, pending, err := s.takeIntent(tx, op, want, fk)
		if err != nil || !pending {
			return err
		}
		if !landed {
			return s.unlanded(tx, want, &ir)
		}
		if a == nil {
			return errNoActivation
		}
		if err := land(tx, a, fk, &ir); err != nil {
			return err
		}
		return s.recomputeInstance(tx, a, fk.Inst, true)
	})
}

// takeIntent deletes and returns fk's intent, refusing one of another op
// than want; false when none is pending.
func (s *Store) takeIntent(tx *bolttable.Tx, op string, want Op, fk fileKey) (intentRow, bool, error) {
	ir, pending, err := s.t.intents.Get(tx.Read(), fk)
	if err != nil || !pending {
		return intentRow{}, false, err
	}
	if Op(ir.Op) != want {
		return intentRow{}, false, fmt.Errorf("statedb: %s: %q of instance %d has a pending op %d", op, fk.Name, fk.Inst, ir.Op)
	}
	if _, err := s.t.intents.Delete(tx, fk); err != nil {
		return intentRow{}, false, err
	}
	return ir, true, nil
}

// unlanded records a delivery's staged attempts undelivered; a retime or
// removal that did not land leaves nothing.
func (s *Store) unlanded(tx *bolttable.Tx, op Op, ir *intentRow) error {
	if op != Deliver {
		return nil
	}
	for i := range ir.Attempts {
		ir.Attempts[i].Delivered = false
	}
	return s.appendDownloads(tx, ir.Attempts)
}

func (s *Store) landDeliver(tx *bolttable.Tx, _ *activation, fk fileKey, ir *intentRow) error {
	if err := s.putRecord(tx, fk, ir); err != nil {
		return err
	}
	first := &ir.Attempts[0]
	if ir.Record.Locked {
		for _, x := range first.Items {
			if err := s.setOneLock(tx, &lockKey{Key: x.k, Tag: first.Target.t, Variant: language.Variant(first.Variant)}, true); err != nil {
				return err
			}
		}
	}
	if err := s.clearRefusals(tx, fk.Inst, first.Target.t, language.Variant(first.Variant)); err != nil {
		return err
	}
	if err := s.appendDownloads(tx, ir.Attempts); err != nil {
		return err
	}
	for _, rm := range ir.Removes {
		if rm.Name == fk.Name {
			continue
		}
		if err := s.removeFile(tx, fileKey{Inst: fk.Inst, Name: rm.Name}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) landRetime(tx *bolttable.Tx, a *activation, fk fileKey, ir *intentRow) error {
	if err := s.putRecord(tx, fk, ir); err != nil {
		return err
	}
	if ir.Baseline == nil {
		return nil
	}
	return s.lockAdopted(tx, a, fk.Inst, evidence{tag: ir.Record.Tag.t, flags: flagsOf(ir.Record.Flags)})
}

func (s *Store) landDelete(tx *bolttable.Tx, _ *activation, fk fileKey, _ *intentRow) error {
	return s.removeFile(tx, fk)
}

// putRecord writes ir's record at the generation after the stored one, and
// its baseline when ir stages one.
func (s *Store) putRecord(tx *bolttable.Tx, fk fileKey, ir *intentRow) error {
	stored, _, err := s.t.files.Get(tx.Read(), fk)
	if err != nil {
		return err
	}
	rec := *ir.Record
	rec.Generation = stored.Generation + 1
	if err := s.t.files.Put(tx, fk, &rec); err != nil {
		return err
	}
	if ir.Baseline == nil {
		return nil
	}
	return s.t.baselines.Put(tx, fk, &baselineRow{Cues: ir.Baseline})
}

// removeFile deletes fk's record and baseline, or drops an unmanaged name
// from the local-file set, touching the written subject of the instance's
// roots, since the local table maps to their rows only.
func (s *Store) removeFile(tx *bolttable.Tx, fk fileKey) error {
	managed, err := s.t.files.Delete(tx, fk)
	if err != nil {
		return err
	}
	if _, err = s.t.baselines.Delete(tx, fk); err != nil || managed {
		return err
	}
	r := tx.Read()
	set, found, err := s.t.local.Get(r, fk.Inst)
	if err != nil || !found {
		return err
	}
	kept := slices.DeleteFunc(slices.Clone(set.Files), func(f localFileRow) bool { return f.Name == fk.Name })
	if len(kept) == len(set.Files) {
		return nil
	}
	inst, _, err := s.t.instances.Get(r, fk.Inst)
	if err != nil {
		return err
	}
	for _, x := range inst.Items {
		tx.Touch(rootSubject(subjectWritten, x.k.Root()))
	}
	set.Files = kept
	return s.t.local.Put(tx, fk.Inst, &set)
}

// lockAdopted locks, on every item of an owned instance, each configured
// pair the adopted file alone makes servable; an ownerless instance's items
// have no languages to configure from.
func (s *Store) lockAdopted(tx *bolttable.Tx, a *activation, id InstanceID, file evidence) error {
	inst, _, err := s.t.instances.Get(tx.Read(), id)
	if err != nil || inst.Owner == "" {
		return err
	}
	for _, x := range inst.Items {
		if err := s.lockAdoptedItem(tx, a, x.k, file); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) lockAdoptedItem(tx *bolttable.Tx, a *activation, k media.Key, file evidence) error {
	r := tx.Read()
	rec, _, err := s.t.items.Get(r, k)
	if err != nil {
		return err
	}
	sg, named, err := s.t.sightings.Get(r, rec.ref())
	if err != nil || !named {
		return err
	}
	for _, p := range configured(a, &sg) {
		if !judge(a.policy, p.tag, p.variant, []evidence{file}).servable {
			continue
		}
		if err := s.setOneLock(tx, &lockKey{Key: k, Tag: p.tag, Variant: p.variant}, true); err != nil {
			return err
		}
	}
	return nil
}

// PendingWrites lists every pending intent, by instance and name.
func (s *Store) PendingWrites(ctx context.Context) ([]Intent, error) {
	var out []Intent
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		return s.t.intents.Scan(r, bolttable.Range{}, func(fk fileKey, v *intentRow) (bool, error) {
			out = append(out, Intent{Op: Op(v.Op), Inst: fk.Inst, Name: fk.Name, SHA256: v.SHA256})
			return true, nil
		})
	})
	return out, err
}

// PendingDeliverOf returns what name's pending delivery staged; false when
// name has no pending delivery.
func (s *Store) PendingDeliverOf(ctx context.Context, inst InstanceID, name string) (PendingDeliver, bool, error) {
	fk := fileKey{Inst: inst, Name: name}
	var ir intentRow
	var found bool
	if err := s.db.View(ctx, func(r *bolttable.ReadTx) (err error) {
		ir, found, err = s.t.intents.Get(r, fk)
		return err
	}); err != nil || !found || Op(ir.Op) != Deliver {
		return PendingDeliver{}, false, err
	}
	d := PendingDeliver{Attempts: make([]Download, 0, len(ir.Attempts)), Removes: make([]Removal, 0, len(ir.Removes))}
	var err error
	if d.Record, err = ir.Record.record(fk); err != nil {
		return PendingDeliver{}, false, err
	}
	if d.Baseline, err = timing.DecodeCues(ir.Baseline); err != nil {
		return PendingDeliver{}, false, fmt.Errorf("%w: intent baseline of %q: %w", bolttable.ErrCorrupt, name, err)
	}
	for i := range ir.Attempts {
		d.Attempts = append(d.Attempts, ir.Attempts[i].download())
	}
	for _, r := range ir.Removes {
		d.Removes = append(d.Removes, Removal{Name: r.Name, SHA256: r.SHA256})
	}
	return d, true, nil
}

// Baseline returns the baseline cues of name's record; false when none is
// stored.
func (s *Store) Baseline(ctx context.Context, inst InstanceID, name string) (timing.Cues, bool, error) {
	var row baselineRow
	var found bool
	if err := s.db.View(ctx, func(r *bolttable.ReadTx) (err error) {
		row, found, err = s.t.baselines.Get(r, fileKey{Inst: inst, Name: name})
		return err
	}); err != nil || !found {
		return timing.Cues{}, false, err
	}
	c, err := timing.DecodeCues(row.Cues)
	if err != nil {
		return timing.Cues{}, false, fmt.Errorf("%w: baseline of %q: %w", bolttable.ErrCorrupt, name, err)
	}
	return c, true, nil
}

// checkUnpinned refuses a change to instance id while an intent pins it.
func (s *Store) checkUnpinned(r *bolttable.ReadTx, id InstanceID) error {
	pinned, err := s.pinned(r, id)
	if err != nil {
		return err
	}
	if pinned {
		return fmt.Errorf("%w: instance %d", errPinned, id)
	}
	return nil
}

func (s *Store) pinned(r *bolttable.ReadTx, id InstanceID) (bool, error) {
	var pinned bool
	err := s.t.intents.Scan(r, bolttable.Range{Prefix: appendUint64(nil, uint64(id))}, func(fileKey, *intentRow) (bool, error) {
		pinned = true
		return false, nil
	})
	return pinned, err
}

// fileRowOf is rec as stored, less Inst and Name, which the key holds, and
// Generation, which End sets; a Timing Validate refuses is refused.
func fileRowOf(rec *FileRecord) (fileRow, error) {
	if err := timing.Validate(rec.Timing); err != nil {
		return fileRow{}, fmt.Errorf("statedb: record %q: %w", rec.Name, err)
	}
	row := fileRow{
		Tag: rowTag{t: rec.Tag}, Timing: timing.AppendTiming(nil, rec.Timing), Ordinal: rec.Ordinal, Flags: flagsByte(rec.Flags), Locked: rec.Locked,
		Identity: identityRow{Size: rec.Identity.Size, MtimeNs: rec.Identity.MtimeNs, SHA256: rec.Identity.SHA256},
	}
	if rec.Provenance != nil {
		row.Provenance = new(provenanceRowOf(rec.Provenance))
	}
	return row, nil
}

func (r *fileRow) record(fk fileKey) (FileRecord, error) {
	t, err := timing.DecodeTiming(r.Timing)
	if err != nil {
		return FileRecord{}, fmt.Errorf("%w: record %q of instance %d: %w", bolttable.ErrCorrupt, fk.Name, fk.Inst, err)
	}
	rec := FileRecord{
		Timing: t, Name: fk.Name, Tag: r.Tag.t, Ordinal: r.Ordinal, Inst: fk.Inst, Generation: r.Generation, Flags: flagsOf(r.Flags), Locked: r.Locked,
		Identity: Identity{Size: r.Identity.Size, MtimeNs: r.Identity.MtimeNs, SHA256: r.Identity.SHA256},
	}
	if r.Provenance != nil {
		rec.Provenance = new(r.Provenance.provenance())
	}
	return rec, nil
}

// clearRefusals clears the pair's refusal on every item of inst: a landed
// delivery ends what the refusal explained.
func (s *Store) clearRefusals(tx *bolttable.Tx, inst InstanceID, tag language.Tag, v language.Variant) error {
	row, _, err := s.t.instances.Get(tx.Read(), inst)
	if err != nil {
		return err
	}
	for _, x := range row.Items {
		if err := s.setRefusal(tx, x.k, tag, v, ""); err != nil {
			return err
		}
	}
	return nil
}
