package statedb

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// ErrNotLockable is SetLock's refusal: an unknown or ownerless instance, a
// pair none of its items configures, or locking a pair none can serve.
var ErrNotLockable = errors.New("statedb: nothing to lock")

// CoverageRow is how one configured pair of an item is covered, whether
// the user locked it, and the wire spelling of the last automatic run's
// refusal while the pair is Missing ("" otherwise).
type CoverageRow struct {
	LastRefusal string
	Target      language.Tag
	Variant     language.Variant
	Coverage    Coverage
	Locked      bool
}

// Coverage returns k's configured pairs in Policy.TargetsFor order; none
// for a key with no item record.
func (s *Store) Coverage(ctx context.Context, k media.Key) ([]CoverageRow, error) {
	var out []CoverageRow
	err := s.db.View(ctx, func(r *bolttable.ReadTx) (err error) {
		out, err = s.coverageRows(r, k)
		return err
	})
	return out, err
}

func (s *Store) coverageRows(r *bolttable.ReadTx, k media.Key) ([]CoverageRow, error) {
	row, _, err := s.t.coverage.Get(r, k)
	if err != nil {
		return nil, err
	}
	out := make([]CoverageRow, 0, len(row.Pairs))
	for _, p := range row.Pairs {
		_, locked, err := s.t.locks.Get(r, lockKey{Key: k, Tag: p.Tag.t, Variant: language.Variant(p.Variant)})
		if err != nil {
			return nil, err
		}
		out = append(out, CoverageRow{Target: p.Tag.t, Variant: language.Variant(p.Variant), Coverage: Coverage(p.Coverage), Locked: locked, LastRefusal: p.LastRefusal})
	}
	return out, nil
}

// SetLock sets or clears the pair's lock on every item of inst that
// configures it, in one write, so a double episode is pinned or resumed
// whole. It refuses with ErrNotLockable an unknown or ownerless instance or
// a pair no item configures, and, locking, a pair none of the instance's
// evidence can serve. Locking a locked pair or clearing an unlocked one
// writes nothing.
func (s *Store) SetLock(ctx context.Context, inst InstanceID, tag language.Tag, v language.Variant, locked bool) error {
	s.actMu.RLock()
	defer s.actMu.RUnlock()
	a := s.active.Load()
	if a == nil {
		return errNoActivation
	}
	return s.write(ctx, "set lock", s.db.Update, func(tx *bolttable.Tx) error {
		keys, err := s.lockable(tx.Read(), a, inst, tag, v, locked)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := s.setOneLock(tx, &lockKey{Key: k, Tag: tag, Variant: v}, locked); err != nil {
				return err
			}
		}
		return nil
	})
}

// setOneLock stores or deletes lk; a lock already set keeps its time.
func (s *Store) setOneLock(tx *bolttable.Tx, lk *lockKey, locked bool) error {
	if !locked {
		_, err := s.t.locks.Delete(tx, *lk)
		return err
	}
	_, held, err := s.t.locks.Get(tx.Read(), *lk)
	if err != nil || held {
		return err
	}
	return s.t.locks.Put(tx, *lk, &lockRow{At: s.now().UTC().Round(0)})
}

// lockable answers the items of inst whose sighting configures (tag, v)
// under a, refusing as SetLock documents.
func (s *Store) lockable(r *bolttable.ReadTx, a *activation, inst InstanceID, tag language.Tag, v language.Variant, locking bool) ([]media.Key, error) {
	row, found, err := s.t.instances.Get(r, inst)
	if err != nil {
		return nil, err
	}
	if !found || row.Owner == "" {
		return nil, fmt.Errorf("%w: instance %d is unknown", ErrNotLockable, inst)
	}
	keys, err := s.configuring(r, a, &row, tag, v)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: instance %d configures no %s %d", ErrNotLockable, inst, tag, v)
	}
	if !locking {
		return keys, nil
	}
	ev, _, err := s.evidenceOf(r, a, inst)
	if err != nil {
		return nil, err
	}
	if !judge(a.policy, tag, v, ev).servable {
		return nil, fmt.Errorf("%w: nothing of instance %d serves %s %d", ErrNotLockable, inst, tag, v)
	}
	return keys, nil
}

// configuring lists the items of inst whose sighting configures (tag, v).
func (s *Store) configuring(r *bolttable.ReadTx, a *activation, inst *instanceRow, tag language.Tag, v language.Variant) ([]media.Key, error) {
	var keys []media.Key
	for _, x := range inst.Items {
		rec, _, err := s.t.items.Get(r, x.k)
		if err != nil {
			return nil, err
		}
		sg, named, err := s.t.sightings.Get(r, rec.ref())
		if err != nil {
			return nil, err
		}
		if named && slices.ContainsFunc(configured(a, &sg), func(p pairJudgement) bool { return p.tag == tag && p.variant == v }) {
			keys = append(keys, x.k)
		}
	}
	return keys, nil
}

// NoteRefusal stores refusal, search's wire spelling of why an automatic run
// of the pair delivered nothing, on the pair's coverage of every item of
// inst where it is Missing, in one write; a byte-equal value writes
// nothing. The next recompute leaving the pair not Missing, and a landed
// delivery of the pair, clear it. An unknown or ownerless instance, or an
// empty refusal, is refused, writing nothing.
func (s *Store) NoteRefusal(ctx context.Context, inst InstanceID, tag language.Tag, v language.Variant, refusal string) error {
	if refusal == "" {
		return errors.New("statedb: note refusal: an empty refusal")
	}
	return s.write(ctx, "note refusal", s.db.Update, func(tx *bolttable.Tx) error {
		row, found, err := s.t.instances.Get(tx.Read(), inst)
		if err != nil {
			return err
		}
		if !found || row.Owner == "" {
			return fmt.Errorf("statedb: note refusal: instance %d is unknown", inst)
		}
		for _, x := range row.Items {
			if err := s.setRefusal(tx, x.k, tag, v, refusal); err != nil {
				return err
			}
		}
		return nil
	})
}

// setRefusal sets refusal on k's (tag, v) pair when it is Missing; "" clears
// it whatever the coverage.
func (s *Store) setRefusal(tx *bolttable.Tx, k media.Key, tag language.Tag, v language.Variant, refusal string) error {
	row, found, err := s.t.coverage.Get(tx.Read(), k)
	if err != nil || !found {
		return err
	}
	i := slices.IndexFunc(row.Pairs, func(p coveragePair) bool { return p.Tag.t == tag && p.Variant == uint8(v) })
	if i < 0 || refusal != "" && row.Pairs[i].Coverage != uint8(Missing) {
		return nil
	}
	row.Pairs[i].LastRefusal = refusal
	return s.t.coverage.Put(tx, k, &row)
}
