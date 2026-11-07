package statedb

import (
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

var errNoActivation = errors.New("statedb: no activation in force; Activate runs first")

// evidence is one subtitle an instance holds that coverage counts: a file
// record, a local file or a non-ignored embedded track.
type evidence struct {
	tag   language.Tag
	flags language.Flags
}

// pairJudgement is one configured pair graded against an instance's
// evidence.
type pairJudgement struct {
	tag      language.Tag
	variant  language.Variant
	coverage Coverage
	// servable is whether some evidence serving the variant grades other
	// than ClassUnrelated, which keeps a lock whose coverage reads Missing.
	servable bool
}

// judge grades the pair (target, v) against ev: Exact or Substitute by the
// best grade of the evidence whose flags serve v, else Missing.
func judge(p language.Policy, target language.Tag, v language.Variant, ev []evidence) pairJudgement {
	j := pairJudgement{tag: target, variant: v, coverage: Missing}
	for _, e := range ev {
		if !e.flags.Serves(v) {
			continue
		}
		switch p.Grade(target, e.tag).Class {
		case language.Exact:
			j.coverage, j.servable = Exact, true
		case language.Substitute:
			j.coverage, j.servable = max(j.coverage, Substitute), true
		case language.ManualOnly:
			j.servable = true
		case language.ClassUnrelated:
		}
	}
	return j
}

// configured lists the pairs a's policy wants for an item with the
// sighting's languages, in Policy.TargetsFor order.
func configured(a *activation, sg *sightingRow) []pairJudgement {
	audio := make([]language.Tag, 0, len(sg.AudioLangs))
	for _, t := range sg.AudioLangs {
		audio = append(audio, t.t)
	}
	var pairs []pairJudgement
	for _, target := range a.policy.TargetsFor(sg.OriginalLang.t, audio) {
		for _, v := range target.Variants {
			pairs = append(pairs, pairJudgement{tag: target.Tag, variant: v})
		}
	}
	return pairs
}

// evidenceOf is instance id's evidence under a, and whether its inventory
// is complete: a probe and a local-file set both stored.
func (s *Store) evidenceOf(r *bolttable.ReadTx, a *activation, id InstanceID) ([]evidence, bool, error) {
	var ev []evidence
	if err := s.t.files.Scan(r, bolttable.Range{Prefix: appendUint64(nil, uint64(id))}, func(_ fileKey, f *fileRow) (bool, error) {
		ev = append(ev, evidence{tag: f.Tag.t, flags: flagsOf(f.Flags)})
		return true, nil
	}); err != nil {
		return nil, false, err
	}
	local, haveLocal, err := s.t.local.Get(r, id)
	if err != nil {
		return nil, false, err
	}
	for _, f := range local.Files {
		ev = append(ev, evidence{tag: f.Tag.t, flags: flagsOf(f.Flags)})
	}
	probe, haveProbe, err := s.t.embedded.Get(r, id)
	if err != nil {
		return nil, false, err
	}
	for _, tr := range probe.Tracks {
		if _, ignored := slices.BinarySearch(a.codecs, tr.Codec); tr.Tag.t != (language.Tag{}) && !ignored {
			ev = append(ev, evidence{tag: tr.Tag.t, flags: flagsOf(tr.Flags)})
		}
	}
	return ev, haveLocal && haveProbe, nil
}

// recomputeInstance recomputes the coverage of every item id holds.
func (s *Store) recomputeInstance(tx *bolttable.Tx, a *activation, id InstanceID, inventory bool) error {
	inst, found, err := s.t.instances.Get(tx.Read(), id)
	if err != nil || !found {
		return err
	}
	for _, x := range inst.Items {
		if err := s.recomputeKey(tx, a, x.k, inventory); err != nil {
			return err
		}
	}
	return nil
}

// recomputeKey rewrites k's coverage row from its sighting's languages and
// its instance's evidence (design section 6), and deletes k's locks on
// pairs a no longer configures. With inventory, the recompute of a
// PutExternal or a landed End, it also deletes locks no evidence can serve
// once the instance's inventory is complete. An item of an ownerless
// instance keeps its rows as stored, since its languages went with its
// source; a key with no item record has no coverage.
func (s *Store) recomputeKey(tx *bolttable.Tx, a *activation, k media.Key, inventory bool) error {
	r := tx.Read()
	rec, found, err := s.t.items.Get(r, k)
	if err != nil || !found {
		return err
	}
	ev, complete, ownerless, err := s.itemEvidence(r, a, k, &rec)
	if err != nil || ownerless {
		return err
	}
	sg, named, err := s.t.sightings.Get(r, rec.ref())
	if err != nil {
		return err
	}
	if !named {
		return fmt.Errorf("statedb: item %s names a sighting that is gone", k)
	}
	pairs := configured(a, &sg)
	row := coverageRow{Pairs: make([]coveragePair, 0, len(pairs))}
	for i, p := range pairs {
		pairs[i] = judge(a.policy, p.tag, p.variant, ev)
		row.Pairs = append(row.Pairs, coveragePair{Tag: rowTag{t: p.tag}, Variant: uint8(p.variant), Coverage: uint8(pairs[i].coverage)})
	}
	if len(row.Pairs) == 0 {
		if _, err := s.t.coverage.Delete(tx, k); err != nil {
			return err
		}
	} else if err := s.t.coverage.Put(tx, k, &row); err != nil {
		return err
	}
	return s.judgeLocks(tx, k, pairs, inventory && complete)
}

// itemEvidence is the evidence of the instance rec names, none for no
// instance, and whether that instance's inventory is complete; ownerless
// reports an instance with no owner, whose items keep their rows.
func (s *Store) itemEvidence(r *bolttable.ReadTx, a *activation, k media.Key, rec *itemRow) (ev []evidence, complete, ownerless bool, err error) {
	if rec.Inst == 0 {
		return nil, false, false, nil
	}
	inst, held, err := s.t.instances.Get(r, InstanceID(rec.Inst))
	switch {
	case err != nil:
		return nil, false, false, err
	case !held:
		return nil, false, false, fmt.Errorf("statedb: item %s names instance %d, which is gone", k, rec.Inst)
	case inst.Owner == "":
		return nil, false, true, nil
	}
	ev, complete, err = s.evidenceOf(r, a, InstanceID(rec.Inst))
	return ev, complete, false, err
}

// judgeLocks deletes k's locks on pairs outside pairs and, with
// unservable, on pairs no evidence can serve.
func (s *Store) judgeLocks(tx *bolttable.Tx, k media.Key, pairs []pairJudgement, unservable bool) error {
	var drop []lockKey
	if err := s.t.locks.Scan(tx.Read(), bolttable.Range{Prefix: k.Bytes()}, func(lk lockKey, _ *lockRow) (bool, error) {
		i := slices.IndexFunc(pairs, func(p pairJudgement) bool { return p.tag == lk.Tag && p.variant == lk.Variant })
		if i < 0 || unservable && !pairs[i].servable {
			drop = append(drop, lk)
		}
		return true, nil
	}); err != nil {
		return err
	}
	for _, lk := range drop {
		if _, err := s.t.locks.Delete(tx, lk); err != nil {
			return err
		}
	}
	return nil
}

// recomputeAll recomputes every item record's coverage under a, deleting
// only unconfigured pairs' locks.
func (s *Store) recomputeAll(tx *bolttable.Tx, a *activation) error {
	var keys []media.Key
	if err := s.t.items.Scan(tx.Read(), bolttable.Range{}, func(k media.Key, _ *itemRow) (bool, error) {
		keys = append(keys, k)
		return true, nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if err := s.recomputeKey(tx, a, k, false); err != nil {
			return err
		}
	}
	return nil
}
