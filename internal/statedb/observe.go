package statedb

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// expireAfter is how long an ownerless instance waits for a source to claim
// its path before it expires.
const expireAfter = 30 * 24 * time.Hour

// ObserveItems records what src's open pass saw of root, in one write
// (design 5.2): the root record and its evidence (deleted when nil), then
// each item's sighting, claim and stamp in order. It refuses with
// ErrSourceInactive or ErrPassUnknown, and with a plain error for a root ref
// of no media kind or no id, evidence failing Validate or naming another
// TVDB series, an empty or duplicate item id, or a video path neither empty
// nor absolute and clean, writing nothing. Under a change batch it also
// queues Import work for every instance an observed item is attached to.
func (s *Store) ObserveItems(ctx context.Context, src media.SourceID, pass PassID, root mediasource.Root, items []mediasource.Item) error { //nolint:gocritic // hugeParam: the source facts are the caller's values
	if err := checkReport(&root, items); err != nil {
		return err
	}
	s.actMu.RLock()
	defer s.actMu.RUnlock()
	return s.write(ctx, "observe items", s.db.Update, func(tx *bolttable.Tx) error {
		p, err := s.openPass(tx.Read(), src, pass)
		if err != nil {
			return err
		}
		o := s.newObservation(tx, src, p.Pass)
		if err := o.observe(&root, items); err != nil {
			return err
		}
		if err := o.settle(); err != nil {
			return err
		}
		if p.Roots == nil {
			return nil
		}
		return s.enqueue(tx, o.attachedIDs(), Import)
	})
}

func checkReport(root *mediasource.Root, items []mediasource.Item) error {
	if err := checkNativeRef(root.Native); err != nil {
		return err
	}
	if err := checkEvidence(root); err != nil {
		return err
	}
	seen := make(map[mediasource.NativeID]bool, len(items))
	for i := range items {
		it := &items[i]
		switch {
		case it.Native == "":
			return fmt.Errorf("statedb: root %q reports an item with no native id", root.Native.ID)
		case seen[it.Native]:
			return fmt.Errorf("statedb: root %q reports item %q twice", root.Native.ID, it.Native)
		case it.VideoPath != "" && (!filepath.IsAbs(it.VideoPath) || filepath.Clean(it.VideoPath) != it.VideoPath):
			return fmt.Errorf("statedb: item %q has video path %q, which is not absolute and clean", it.Native, it.VideoPath)
		}
		seen[it.Native] = true
	}
	return nil
}

func checkNativeRef(r mediasource.NativeRef) error {
	if r.ID == "" || (r.Kind != media.Episode && r.Kind != media.Movie) {
		return fmt.Errorf("statedb: root ref %q of kind %d names no series or movie", r.ID, r.Kind)
	}
	return nil
}

// itemKey keys an item (design 5.1): an episode by its series' ids and its
// aired numbering, a movie by its own ids, each falling back to its root's.
func itemKey(root *mediasource.Root, it *mediasource.Item) (media.Key, bool) {
	if root.Native.Kind == media.Episode {
		return media.Choose(media.Episode, root.External, it.Numbering)
	}
	ids := it.External
	ids.TVDB = cmp.Or(ids.TVDB, root.External.TVDB)
	ids.TMDB = cmp.Or(ids.TMDB, root.External.TMDB)
	ids.IMDb = cmp.Or(ids.IMDb, root.External.IMDb)
	return media.Choose(media.Movie, ids, media.Numbering{})
}

// observation is one observing or finishing write: the source, its open
// pass, and what the write has released and attached so far.
type observation struct {
	s   *Store
	tx  *bolttable.Tx
	r   *bolttable.ReadTx
	now time.Time
	// released holds the keys whose item record lost its claim; settle
	// decides each one's fate on its final state.
	released map[media.Key]struct{}
	// emptied holds the instances a release left with no item, with the
	// keys they held; settle deletes those still empty, so a renumbered
	// episode at an unchanged path keeps its instance and managed files.
	emptied  map[InstanceID][]media.Key
	attached map[InstanceID]struct{}
	// touched holds every key observed or released; settle recomputes the
	// coverage of each one still recorded.
	touched map[media.Key]struct{}
	// scene is the scene name of the first item reported at each path.
	scene map[string]string
	src   media.SourceID
	pass  uint64
}

func (s *Store) newObservation(tx *bolttable.Tx, src media.SourceID, pass uint64) *observation {
	return &observation{
		s: s, tx: tx, r: tx.Read(), now: s.now().UTC(), src: src, pass: pass,
		released: map[media.Key]struct{}{}, emptied: map[InstanceID][]media.Key{}, attached: map[InstanceID]struct{}{},
		touched: map[media.Key]struct{}{}, scene: map[string]string{},
	}
}

func (o *observation) observe(root *mediasource.Root, items []mediasource.Item) error {
	rr := rootRef{Source: o.src, Root: root.Native}
	row := rootRowOf(root)
	if err := o.s.t.roots.Put(o.tx, rr, &row); err != nil {
		return err
	}
	if err := o.putEvidence(rr, root.Evidence); err != nil {
		return err
	}
	if err := o.stamp(itemRef{rootRef: rr}); err != nil {
		return err
	}
	for i := range items {
		if p := items[i].VideoPath; p != "" {
			if _, ok := o.scene[p]; !ok {
				o.scene[p] = items[i].SceneName
			}
		}
	}
	for i := range items {
		if err := o.observeItem(itemRef{rootRef: rr, Item: items[i].Native}, root, &items[i]); err != nil {
			return err
		}
	}
	return nil
}

func (o *observation) stamp(ref itemRef) error {
	return o.s.t.stamps.Put(o.tx, ref, &stampRow{Pass: o.pass})
}

// observeItem stores one item's sighting: a claim that differs from the
// stored one is released first, then the first of design 5.2's rules that
// matches decides the new one.
func (o *observation) observeItem(ref itemRef, root *mediasource.Root, it *mediasource.Item) error {
	old, had, err := o.s.t.sightings.Get(o.r, ref)
	if err != nil {
		return err
	}
	next := sightingRowOf(it)
	key, identified := itemKey(root, it)
	if identified {
		next.Key = &rowKey{k: key}
		o.touched[key] = struct{}{}
	}
	if had && (!sameKey(old.Key, next.Key) || old.VideoPath != next.VideoPath) {
		if err := o.release(ref, &old); err != nil {
			return err
		}
	}
	if identified {
		keyed, err := o.claim(ref, key, it.VideoPath)
		if err != nil {
			return err
		}
		if !keyed {
			next.Key = nil
		}
	}
	if unidentifiedChanged(had, &old, &next) {
		o.tx.Mark(MarkUnidentified)
	}
	if err := o.s.t.sightings.Put(o.tx, ref, &next); err != nil {
		return err
	}
	return o.stamp(ref)
}

// claim applies rules 2 to 5 to an identified sighting; keyed is false
// when the sighting is stored with no key.
func (o *observation) claim(ref itemRef, k media.Key, p string) (keyed bool, err error) {
	if p == "" {
		return true, o.noVideo(ref, k)
	}
	return o.attachOrOverlap(ref, k, p)
}

// unidentifiedChanged reports whether storing next over old makes an
// unidentified sighting appear, change or go.
func unidentifiedChanged(had bool, old, next *sightingRow) bool {
	if !had {
		return next.Key == nil
	}
	return (next.Key == nil || old.Key == nil) && !sameRow(sightingCodec, old, next)
}

func sameKey(a, b *rowKey) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.k == b.k
}

func sameRow[V any](c bolttable.Codec[V], a, b *V) bool {
	ab, aerr := c.Append(nil, a)
	bb, berr := c.Append(nil, b)
	return aerr == nil && berr == nil && bytes.Equal(ab, bb)
}

// release drops the claim the stored sighting old holds: its overlap row,
// and the key it attached or recorded with no video, which settle later
// keeps, hands on or loses.
func (o *observation) release(ref itemRef, old *sightingRow) error {
	if _, err := o.s.t.overlaps.Delete(o.tx, ref); err != nil {
		return err
	}
	if old.Key == nil {
		return nil
	}
	k := old.Key.k
	rec, found, err := o.s.t.items.Get(o.r, k)
	if err != nil || !found || !rec.names(ref) {
		return err
	}
	if rec.Inst != 0 {
		if err := o.detach(InstanceID(rec.Inst), k); err != nil {
			return err
		}
		rec.Inst = 0
		if err := o.s.t.items.Put(o.tx, k, &rec); err != nil {
			return err
		}
	}
	o.released[k] = struct{}{}
	o.touched[k] = struct{}{}
	return nil
}

// detach removes k from instance id; settle deletes it if it stays empty.
func (o *observation) detach(id InstanceID, k media.Key) error {
	inst, found, err := o.s.t.instances.Get(o.r, id)
	if err != nil || !found {
		return err
	}
	keep := slices.DeleteFunc(slices.Clone(inst.Items), func(x rowKey) bool { return x.k == k })
	if len(keep) == len(inst.Items) {
		return nil
	}
	if err := o.s.checkUnpinned(o.r, id); err != nil {
		return err
	}
	if len(keep) == 0 {
		delete(o.attached, id)
	}
	o.emptied[id] = append(o.emptied[id], k)
	inst.Items = keep
	return o.s.t.instances.Put(o.tx, id, &inst)
}

// dropEmptied deletes, with its cascade, every instance a release emptied
// and no attach refilled, first touching the written subject of each root
// its keys were under, so the cascaded file records reach Written.
func (o *observation) dropEmptied() error {
	for id, keys := range o.emptied {
		inst, found, err := o.s.t.instances.Get(o.r, id)
		if err != nil {
			return err
		}
		if !found || len(inst.Items) > 0 {
			continue
		}
		for _, k := range keys {
			o.tx.Touch(rootSubject(subjectWritten, k.Root()))
		}
		if _, _, err := o.s.deleteInstance(o.tx, id); err != nil {
			return err
		}
	}
	return nil
}

// noVideo is rule 2: the sighting provides k's item record unless the
// record names another source's sighting, an instance, or another sighting
// of this source stamped in the open pass.
func (o *observation) noVideo(ref itemRef, k media.Key) error {
	rec, found, err := o.s.t.items.Get(o.r, k)
	if err != nil {
		return err
	}
	if found && !rec.names(ref) {
		taken, err := o.heldElsewhere(&rec)
		if err != nil || taken || rec.Inst != 0 {
			return err
		}
	}
	return o.s.t.items.Put(o.tx, k, new(recordOf(ref, 0)))
}

// heldElsewhere reports whether rec, which names another sighting than the
// caller's, keeps its claim: it names another source's sighting, or one of
// this source's stamped in the open pass.
func (o *observation) heldElsewhere(rec *itemRow) (bool, error) {
	if rec.Source != o.src {
		return true, nil
	}
	st, found, err := o.s.t.stamps.Get(o.r, rec.ref())
	return found && st.Pass == o.pass, err
}

// attachOrOverlap is rules 3 to 5 for a key reported at video path p. keyed
// is false when the sighting is stored with no key: a second path for a key
// this source already attached in the open pass.
func (o *observation) attachOrOverlap(ref itemRef, k media.Key, p string) (keyed bool, err error) {
	at, inst, atPath, err := o.instanceAt(p)
	if err != nil {
		return false, err
	}
	rec, found, err := o.s.t.items.Get(o.r, k)
	if err != nil {
		return false, err
	}
	held := found && rec.Inst != 0
	onAt := held && atPath && InstanceID(rec.Inst) == at
	owner, byKey, err := o.overlapOwner(&inst, atPath, &rec, held && !onAt)
	if err != nil {
		return false, err
	}
	if owner != "" {
		return true, o.overlap(ref, owner, byKey)
	}
	if held && !rec.names(ref) && rec.Source == o.src {
		taken, terr := o.heldElsewhere(&rec)
		if terr != nil {
			return false, terr
		}
		if taken {
			// Another sighting of this source attached k in the open pass:
			// at p it is the same file reported twice, which keeps its key
			// with no claim; at another path the sighting is stored with no
			// key, so nothing alternates over passes.
			return onAt, o.dropOverlap(ref)
		}
	}
	return true, o.attachKey(ref, k, p, at, &inst, atPath, &rec, held)
}

func (o *observation) dropOverlap(ref itemRef) error {
	_, err := o.s.t.overlaps.Delete(o.tx, ref)
	return err
}

// attachKey attaches k at p for the sighting at ref, releasing it from the
// instance that held it elsewhere (supersession), and records the claim.
func (o *observation) attachKey(ref itemRef, k media.Key, p string, at InstanceID, inst *instanceRow, atPath bool, rec *itemRow, held bool) error {
	id, err := o.attach(at, inst, atPath, k, p)
	if err != nil {
		return err
	}
	if held && InstanceID(rec.Inst) != id {
		if err := o.detach(InstanceID(rec.Inst), k); err != nil {
			return err
		}
	}
	if err := o.dropOverlap(ref); err != nil {
		return err
	}
	o.attached[id] = struct{}{}
	return o.s.t.items.Put(o.tx, k, new(recordOf(ref, id)))
}

// overlapOwner is rules 3 and 4: the live source other than this one that
// owns the instance at the path, else the instance holding the key
// elsewhere; "" when neither.
func (o *observation) overlapOwner(inst *instanceRow, atPath bool, rec *itemRow, elsewhere bool) (owner media.SourceID, byKey bool, err error) {
	if atPath && inst.Owner != "" && inst.Owner != o.src {
		return inst.Owner, false, nil
	}
	if !elsewhere {
		return "", false, nil
	}
	holder, _, err := o.s.t.instances.Get(o.r, InstanceID(rec.Inst))
	if err != nil || holder.Owner == "" || holder.Owner == o.src {
		return "", false, err
	}
	return holder.Owner, true, nil
}

func (o *observation) instanceAt(p string) (InstanceID, instanceRow, bool, error) {
	id, _, found, err := o.s.t.byPath.Lookup(o.r, appendText(nil, p))
	if err != nil || !found {
		return 0, instanceRow{}, false, err
	}
	inst, found, err := o.s.t.instances.Get(o.r, id)
	return id, inst, found, err
}

// attach is rule 5: k joins the instance at p, which this source owns, is
// claimed from no owner, or is created. A claimed ownerless instance keeps
// only the keys this write attaches at p; one orphaned past expireAfter
// expires first. A pinned instance is neither claimed nor given a key.
func (o *observation) attach(id InstanceID, inst *instanceRow, atPath bool, k media.Key, p string) (InstanceID, error) {
	if atPath && (inst.Owner == "" || !slices.ContainsFunc(inst.Items, func(x rowKey) bool { return x.k == k })) {
		if err := o.s.checkUnpinned(o.r, id); err != nil {
			return 0, err
		}
	}
	if atPath && inst.Owner == "" && o.expired(inst) {
		if err := o.expire(id); err != nil {
			return 0, err
		}
		atPath = false
	}
	switch {
	case !atPath:
		next, err := o.s.t.instances.NextID(o.tx)
		if err != nil {
			return 0, err
		}
		id, *inst = InstanceID(next), instanceRow{VideoPath: p}
	case inst.Owner == "":
		if err := o.releaseAll(id, inst); err != nil {
			return 0, err
		}
		inst.Items, inst.Orphaned = nil, time.Time{}
	}
	inst.Owner, inst.SceneName = o.src, o.scene[p]
	if !slices.ContainsFunc(inst.Items, func(x rowKey) bool { return x.k == k }) {
		inst.Items = append(inst.Items, rowKey{k: k})
	}
	return id, o.s.t.instances.Put(o.tx, id, inst)
}

// releaseAll detaches every key of an ownerless instance being claimed;
// settle keeps, hands on or loses those this write does not attach again.
func (o *observation) releaseAll(id InstanceID, inst *instanceRow) error {
	for _, x := range inst.Items {
		if err := o.unlinkRecord(x.k, id); err != nil {
			return err
		}
	}
	return nil
}

// unlinkRecord clears k's item record's instance when it is id.
func (o *observation) unlinkRecord(k media.Key, id InstanceID) error {
	rec, found, err := o.s.t.items.Get(o.r, k)
	if err != nil || !found || InstanceID(rec.Inst) != id {
		return err
	}
	rec.Inst = 0
	o.released[k] = struct{}{}
	o.touched[k] = struct{}{}
	return o.s.t.items.Put(o.tx, k, &rec)
}

func (o *observation) expired(inst *instanceRow) bool {
	return o.now.Sub(inst.Orphaned) > expireAfter
}

// expire deletes an ownerless instance with its cascade; settle applies
// the key-loss rule to every key it held.
func (o *observation) expire(id InstanceID) error {
	row, found, err := o.s.deleteInstance(o.tx, id)
	if err != nil || !found {
		return err
	}
	for _, x := range row.Items {
		if err := o.unlinkRecord(x.k, id); err != nil {
			return err
		}
	}
	return nil
}

// overlap is rules 3 and 4: the sighting attaches nothing and names the
// live source owning its path or key.
func (o *observation) overlap(ref itemRef, owner media.SourceID, byKey bool) error {
	return o.s.t.overlaps.Put(o.tx, ref, &overlapRow{Owner: owner, ByKey: byKey})
}

// settle deletes the instances left empty, then applies the key-loss rule
// (Store.handOn) to every released key on its final state. Last it
// recomputes every other touched key's coverage, deleting only
// unconfigured pairs' locks.
func (o *observation) settle() error {
	if err := o.dropEmptied(); err != nil {
		return err
	}
	lost, err := o.unheld()
	if err != nil {
		return err
	}
	a := o.s.active.Load()
	if err := o.s.handOn(o.tx, a, lost); err != nil {
		return err
	}
	for _, k := range lost {
		delete(o.touched, k)
	}
	keys := slices.SortedFunc(maps.Keys(o.touched), func(x, y media.Key) int { return bytes.Compare(x.Bytes(), y.Bytes()) })
	for _, k := range keys {
		if err := o.s.recomputeKey(o.tx, a, k, false); err != nil {
			return err
		}
	}
	return nil
}

// unheld lists, in key order, the released keys whose record names no
// instance and whose named sighting no longer reports them with no video.
func (o *observation) unheld() ([]media.Key, error) {
	var lost []media.Key
	for k := range o.released {
		rec, found, err := o.s.t.items.Get(o.r, k)
		if err != nil {
			return nil, err
		}
		if !found || rec.Inst != 0 {
			continue
		}
		sg, named, err := o.s.t.sightings.Get(o.r, rec.ref())
		if err != nil {
			return nil, err
		}
		if !named || !sg.reportsNoVideo(k) {
			lost = append(lost, k)
		}
	}
	slices.SortFunc(lost, func(x, y media.Key) int { return bytes.Compare(x.Bytes(), y.Bytes()) })
	return lost, nil
}

func (o *observation) attachedIDs() []InstanceID {
	ids := make([]InstanceID, 0, len(o.attached))
	for id := range o.attached {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (r *sightingRow) reportsNoVideo(k media.Key) bool {
	return r.Key != nil && r.Key.k == k && r.VideoPath == ""
}

func (r *itemRow) ref() itemRef {
	return itemRef{Source: r.Source, Root: r.Root.ref(), Item: r.Item}
}

func (r *itemRow) names(ref itemRef) bool { return r.ref() == ref }

func recordOf(ref itemRef, id InstanceID) itemRow {
	return itemRow{Source: ref.Source, Item: ref.Item, Root: nativeRefRow{ID: ref.Root.ID, Kind: uint8(ref.Root.Kind)}, Inst: uint64(id)}
}
