package statedb

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// PassID names one pass of one source. Zero is none.
type PassID uint64

// PassScope is what a pass covers: Roots nil is the whole source; any other
// value, empty included, is a change batch over those roots.
type PassScope struct{ Roots []mediasource.NativeRef }

// The refusals of a pass call.
var (
	ErrSourceInactive = errors.New("statedb: source is not in the activation")
	ErrPassUnknown    = errors.New("statedb: not the source's open pass")
)

// maxKeep bounds FinishPass's keep: the roots one whole pass could not read.
const maxKeep = 32

// BeginPass opens a pass of src over scope, abandoning any open one.
func (s *Store) BeginPass(ctx context.Context, src media.SourceID, scope PassScope) (PassID, error) {
	var roots []nativeRefRow
	if scope.Roots != nil {
		roots = make([]nativeRefRow, 0, len(scope.Roots))
		for _, r := range scope.Roots {
			if err := checkNativeRef(r); err != nil {
				return 0, err
			}
			roots = append(roots, nativeRefRow{ID: r.ID, Kind: uint8(r.Kind)})
		}
	}
	s.actMu.RLock()
	defer s.actMu.RUnlock()
	var id uint64
	err := s.write(ctx, "begin pass", s.db.Update, func(tx *bolttable.Tx) (err error) {
		if !s.isActive(src) {
			return fmt.Errorf("%w: %s", ErrSourceInactive, src)
		}
		if id, err = s.t.passes.NextID(tx); err != nil {
			return err
		}
		return s.t.passes.Put(tx, src, &passRow{Pass: id, Roots: roots})
	})
	return PassID(id), err
}

func (s *Store) isActive(src media.SourceID) bool {
	a := s.active.Load()
	if a == nil {
		return false
	}
	_, found := slices.BinarySearch(a.sources, src)
	return found
}

// openPass answers src's pass record when pass is its open pass and src is
// in the activation in force.
func (s *Store) openPass(r *bolttable.ReadTx, src media.SourceID, pass PassID) (passRow, error) {
	if !s.isActive(src) {
		return passRow{}, fmt.Errorf("%w: %s", ErrSourceInactive, src)
	}
	p, found, err := s.t.passes.Get(r, src)
	if err != nil {
		return passRow{}, err
	}
	if !found || PassID(p.Pass) != pass {
		return passRow{}, fmt.Errorf("%w: %s pass %d", ErrPassUnknown, src, pass)
	}
	return p, nil
}

// FinishPass closes src's open pass and returns the distinct owners all of
// src's overlap rows name (design 5.3). Incomplete, it retires nothing.
// Complete, in one write, it retires what in scope the pass did not stamp,
// except under a root in keep, stores next as the cursor, after a whole pass
// queues Scan work for src's instances outside keep, and expires instances
// ownerless for more than 30 days. keep, the roots a whole pass could not
// read, is refused, writing nothing, for a batch, an incomplete pass, or
// past 32 roots.
func (s *Store) FinishPass(ctx context.Context, src media.SourceID, pass PassID, complete bool, next mediasource.Cursor, keep []mediasource.NativeRef) ([]media.SourceID, error) {
	if len(keep) > maxKeep {
		return nil, fmt.Errorf("statedb: a pass keeps at most %d roots, not %d", maxKeep, len(keep))
	}
	if keep != nil && !complete {
		return nil, errors.New("statedb: an incomplete pass keeps no roots")
	}
	cursor, err := next.MarshalBinary()
	if err != nil {
		return nil, err
	}
	s.actMu.RLock()
	defer s.actMu.RUnlock()
	var owners []media.SourceID
	err = s.write(ctx, "finish pass", s.db.Update, func(tx *bolttable.Tx) (ferr error) {
		owners, ferr = s.finishPass(tx, src, pass, complete, keep, cursor)
		return ferr
	})
	if err != nil {
		return nil, err
	}
	return owners, nil
}

func (s *Store) finishPass(tx *bolttable.Tx, src media.SourceID, pass PassID, complete bool, keep []mediasource.NativeRef, cursor []byte) ([]media.SourceID, error) {
	p, err := s.openPass(tx.Read(), src, pass)
	if err != nil {
		return nil, err
	}
	if keep != nil && p.Roots != nil {
		return nil, errors.New("statedb: a change batch keeps no roots")
	}
	if _, err := s.t.passes.Delete(tx, src); err != nil {
		return nil, err
	}
	if complete {
		if err := s.completePass(tx, src, &p, keep, cursor); err != nil {
			return nil, err
		}
	}
	return s.overlapOwners(tx.Read(), src)
}

func (s *Store) completePass(tx *bolttable.Tx, src media.SourceID, p *passRow, keep []mediasource.NativeRef, cursor []byte) error {
	o := s.newObservation(tx, src, p.Pass)
	kept := make(map[mediasource.NativeRef]bool, len(keep))
	for _, r := range keep {
		kept[r] = true
	}
	if err := o.retire(p, kept); err != nil {
		return err
	}
	if err := o.expireOrphans(); err != nil {
		return err
	}
	if err := o.settle(); err != nil {
		return err
	}
	if err := s.t.cursors.Put(tx, src, &cursorRow{Cursor: cursor}); err != nil {
		return err
	}
	if p.Roots != nil {
		return nil
	}
	ids, err := o.ownedOutside(kept)
	if err != nil {
		return err
	}
	return s.enqueue(tx, ids, Scan)
}

// retire deletes every sighting and root record in the pass scope whose
// stamp is not this pass, outside the kept roots, releasing each claim.
func (o *observation) retire(p *passRow, kept map[mediasource.NativeRef]bool) error {
	prefixes := [][]byte{appendText(nil, string(o.src))}
	if p.Roots != nil {
		prefixes = prefixes[:0]
		for _, r := range p.Roots {
			prefixes = append(prefixes, appendRootRef(nil, rootRef{Source: o.src, Root: r.ref()}))
		}
	}
	var stale []itemRef
	for _, prefix := range prefixes {
		if err := o.s.t.stamps.Scan(o.r, bolttable.Range{Prefix: prefix}, func(ref itemRef, st *stampRow) (bool, error) {
			if st.Pass != o.pass && !kept[ref.Root] {
				stale = append(stale, ref)
			}
			return true, nil
		}); err != nil {
			return err
		}
	}
	for _, ref := range stale {
		if err := o.retireOne(ref); err != nil {
			return err
		}
	}
	return nil
}

func (o *observation) retireOne(ref itemRef) error {
	if _, err := o.s.t.stamps.Delete(o.tx, ref); err != nil {
		return err
	}
	if ref.Item == "" {
		_, err := o.s.t.roots.Delete(o.tx, ref.rootRef)
		return err
	}
	old, found, gerr := o.s.t.sightings.Get(o.r, ref)
	if gerr != nil || !found {
		return gerr
	}
	if err := o.release(ref, &old); err != nil {
		return err
	}
	if old.Key == nil {
		o.tx.Mark(MarkUnidentified)
	}
	_, err := o.s.t.sightings.Delete(o.tx, ref)
	return err
}

// expireOrphans expires every ownerless instance orphaned more than
// expireAfter ago, skipping a pinned one, so recovery always finds it.
func (o *observation) expireOrphans() error {
	var ids []InstanceID
	if err := o.s.t.orphaned.Scan(o.r, bolttable.Range{To: appendTime(nil, o.now.Add(-expireAfter))}, func(_ []byte, id InstanceID, _ []byte) (bool, error) {
		pinned, err := o.s.pinned(o.r, id)
		if !pinned {
			ids = append(ids, id)
		}
		return err == nil, err
	}); err != nil {
		return err
	}
	for _, id := range ids {
		if err := o.expire(id); err != nil {
			return err
		}
	}
	return nil
}

// ownedOutside lists src's instances with no item recorded under a kept
// root.
func (o *observation) ownedOutside(kept map[mediasource.NativeRef]bool) ([]InstanceID, error) {
	var ids []InstanceID
	err := o.s.t.instances.Scan(o.r, bolttable.Range{}, func(id InstanceID, v *instanceRow) (bool, error) {
		if v.Owner != o.src {
			return true, nil
		}
		for _, x := range v.Items {
			if len(kept) == 0 {
				break
			}
			rec, _, err := o.s.t.items.Get(o.r, x.k)
			if err != nil {
				return false, err
			}
			if kept[rec.Root.ref()] {
				return true, nil
			}
		}
		ids = append(ids, id)
		return true, nil
	})
	return ids, err
}

func (s *Store) overlapOwners(r *bolttable.ReadTx, src media.SourceID) ([]media.SourceID, error) {
	var owners []media.SourceID
	err := s.t.overlaps.Scan(r, bolttable.Range{Prefix: appendText(nil, string(src))}, func(_ itemRef, v *overlapRow) (bool, error) {
		if !slices.Contains(owners, v.Owner) {
			owners = append(owners, v.Owner)
		}
		return true, nil
	})
	slices.Sort(owners)
	return owners, err
}

// SourceCursor returns the cursor src's last complete pass stored; false
// when none.
func (s *Store) SourceCursor(ctx context.Context, src media.SourceID) (mediasource.Cursor, bool, error) {
	var c mediasource.Cursor
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		row, ok, err := s.t.cursors.Get(r, src)
		if err != nil || !ok {
			return err
		}
		found = true
		return c.UnmarshalBinary(row.Cursor)
	})
	if err != nil {
		return mediasource.Cursor{}, false, err
	}
	return c, found, nil
}

func (n nativeRefRow) ref() mediasource.NativeRef {
	return mediasource.NativeRef{ID: n.ID, Kind: media.Kind(n.Kind)}
}
