package statedb

import (
	"context"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// Activation is the configuration every source fact is judged under: the
// sources in force, the language policy and the embedded codecs coverage
// ignores. Auth, when set, is the sign-in configuration table 2B records,
// and Guard judges a change to it (design 5.4).
type Activation struct {
	Auth          *AuthMethodState
	Guard         MethodGuard
	Languages     language.Policy
	Sources       []media.SourceID
	IgnoredCodecs []string
}

// activation is an Activation as stored: sources and codecs sorted and
// deduplicated.
type activation struct {
	policy  language.Policy
	sources []media.SourceID
	codecs  []string
}

func (a *activation) row(configRev uint64) activationRow {
	return activationRow{Sources: a.sources, Codecs: a.codecs, Policy: a.policy.Digest(), ConfigRev: configRev}
}

func (a *activation) sameAs(r *activationRow) bool {
	return slices.Equal(a.sources, r.Sources) && slices.Equal(a.codecs, r.Codecs) && a.policy.Digest() == [32]byte(r.Policy)
}

// Activate installs a. Changed sources, policy or codecs make one write
// that applies design 5.4's removal and marks config, so every reader
// re-reads in full; the first Activate after Open, and a policy or codec
// change, also recompute coverage there, deleting only unconfigured pairs'
// locks. An a.Auth that differs from table 2B's row is judged by a.Guard and
// recorded in that write, under an equal digest too. A failed Activate, a
// Guard refusal included, writes nothing and keeps the previous activation.
func (s *Store) Activate(ctx context.Context, a Activation) error {
	next, err := normalize(a)
	if err != nil {
		return err
	}
	s.actMu.Lock()
	defer s.actMu.Unlock()
	stored, authBefore, authStored, err := s.storedActivation(ctx)
	if err != nil {
		return err
	}
	changed := !next.sameAs(&stored)
	// The first Activate after Open recomputes whatever the stored digest:
	// the digest names the configuration, not this binary's grading code.
	recompute := s.active.Load() == nil || next.policy.Digest() != [32]byte(stored.Policy) || !slices.Equal(next.codecs, stored.Codecs)
	authChanged := a.Auth != nil && (!authStored || authBefore != *a.Auth)
	if changed || recompute || authChanged {
		err := s.accountWrite(ctx, "activate", s.db.Update, func(x *accountTx) error {
			return s.writeActivation(ctx, x, &a, &stored, next, changed, recompute)
		})
		if err != nil {
			return err
		}
	}
	s.active.Store(next)
	return nil
}

// storedActivation reads the stored activation and table 2B's row in one
// View.
func (s *Store) storedActivation(ctx context.Context) (stored activationRow, auth AuthMethodState, authStored bool, err error) {
	err = s.db.View(ctx, func(r *bolttable.ReadTx) (err error) {
		if stored, _, err = s.t.activation.Get(r, constKey{}); err != nil {
			return err
		}
		auth, authStored, err = authReader{a: s.a, r: r}.methodState()
		return err
	})
	return stored, auth, authStored, err
}

func (s *Store) writeActivation(ctx context.Context, x *accountTx, a *Activation, stored *activationRow, next *activation, changed, recompute bool) error {
	if a.Auth != nil {
		if err := guardAndWrite(ctx, x, a.Auth, a.Guard); err != nil {
			return err
		}
	}
	if changed {
		if err := s.applyActivation(x.tx, stored, next); err != nil {
			return err
		}
	}
	if recompute {
		return s.recomputeAll(x.tx, next)
	}
	return nil
}

// Policy returns the language policy of the activation in force, the zero
// Policy before the first Activate.
func (s *Store) Policy() language.Policy {
	if a := s.active.Load(); a != nil {
		return a.policy
	}
	return language.Policy{}
}

// normalize sorts a's sources and codecs; a source id outside media's
// grammar is refused by the activation row's encoder, writing nothing.
func normalize(a Activation) (*activation, error) {
	if a.Auth != nil && a.Guard == nil {
		return nil, errNoGuard
	}
	sources := slices.Clone(a.Sources)
	slices.Sort(sources)
	for i := 1; i < len(sources); i++ {
		if sources[i-1] == sources[i] {
			return nil, fmt.Errorf("statedb: activation names source %q twice", sources[i])
		}
	}
	codecs := slices.Clone(a.IgnoredCodecs)
	slices.Sort(codecs)
	return &activation{policy: a.Languages, sources: sources, codecs: slices.Compact(codecs)}, nil
}

func (s *Store) applyActivation(tx *bolttable.Tx, stored *activationRow, next *activation) error {
	removed := make(map[media.SourceID]bool)
	for _, src := range stored.Sources {
		if !slices.Contains(next.sources, src) {
			removed[src] = true
		}
	}
	if len(removed) > 0 {
		if err := s.removeSources(tx, next, removed); err != nil {
			return err
		}
	}
	tx.Mark(MarkConfig)
	row := next.row(stored.ConfigRev)
	return s.t.activation.Put(tx, constKey{}, &row)
}

// removeSources applies design 5.4's removal for every source in removed,
// judging what it hands on under next.
func (s *Store) removeSources(tx *bolttable.Tx, next *activation, removed map[media.SourceID]bool) error {
	for src := range removed {
		prefix := appendText(nil, string(src))
		if err := deleteRange(tx, s.t.roots, prefix); err != nil {
			return err
		}
		if err := deleteRange(tx, s.t.sightings, prefix); err != nil {
			return err
		}
		if err := deleteRange(tx, s.t.stamps, prefix); err != nil {
			return err
		}
		if err := deleteRange(tx, s.t.overlaps, prefix); err != nil {
			return err
		}
		if _, err := s.t.cursors.Delete(tx, src); err != nil {
			return err
		}
		if _, err := s.t.passes.Delete(tx, src); err != nil {
			return err
		}
	}
	if err := s.orphanInstances(tx, removed); err != nil {
		return err
	}
	if err := s.releaseNoVideoItems(tx, next, removed); err != nil {
		return err
	}
	return s.dropOverlapsNaming(tx, removed)
}

// deleteRange deletes every row of t under prefix.
func deleteRange[K, V any](tx *bolttable.Tx, t *bolttable.Table[K, V], prefix []byte) error {
	return deleteWhere(tx, t, bolttable.Range{Prefix: prefix}, func(*V) bool { return true })
}

// deleteWhere deletes every row of t in rg that drop selects. Keys are
// collected first, since a bbolt cursor must not see its bucket change
// under it.
func deleteWhere[K, V any](tx *bolttable.Tx, t *bolttable.Table[K, V], rg bolttable.Range, drop func(*V) bool) error { //nolint:gocritic // hugeParam: bolttable's Scan takes a Range by value
	var keys []K
	if err := t.Scan(tx.Read(), rg, func(k K, v *V) (bool, error) {
		if drop(v) {
			keys = append(keys, k)
		}
		return true, nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := t.Delete(tx, k); err != nil {
			return err
		}
	}
	return nil
}

// orphanInstances makes every instance a removed source owns ownerless,
// keeping its path, items, records, locks and history.
func (s *Store) orphanInstances(tx *bolttable.Tx, removed map[media.SourceID]bool) error {
	owned := map[InstanceID]instanceRow{}
	if err := s.t.instances.Scan(tx.Read(), bolttable.Range{}, func(id InstanceID, v *instanceRow) (bool, error) {
		if removed[v.Owner] {
			owned[id] = *v
		}
		return true, nil
	}); err != nil {
		return err
	}
	now := s.now().UTC().Round(0)
	for id, row := range owned {
		row.Owner, row.Orphaned = "", now
		if err := s.t.instances.Put(tx, id, &row); err != nil {
			return err
		}
	}
	return nil
}

// releaseNoVideoItems applies the key-loss rule to the item records with
// no instance that name a removed source's sighting.
func (s *Store) releaseNoVideoItems(tx *bolttable.Tx, next *activation, removed map[media.SourceID]bool) error {
	var released []media.Key
	if err := s.t.items.Scan(tx.Read(), bolttable.Range{}, func(k media.Key, v *itemRow) (bool, error) {
		if v.Inst == 0 && removed[v.Source] {
			released = append(released, k)
		}
		return true, nil
	}); err != nil {
		return err
	}
	return s.handOn(tx, next, released)
}

// handOn is the key-loss rule for lost, keys whose record names no instance
// and no sighting still reporting them with no video: a live no-video
// sighting of the key takes the record over and the key's coverage is
// recomputed under a; with none the key loses its record, coverage and
// locks, and its history stays (DECISIONS 1).
func (s *Store) handOn(tx *bolttable.Tx, a *activation, lost []media.Key) error {
	if len(lost) == 0 {
		return nil
	}
	heirs, err := s.noVideoSightings(tx.Read(), lost)
	if err != nil {
		return err
	}
	for _, k := range lost {
		heir, ok := heirs[k]
		if !ok {
			if err := s.loseKey(tx, k); err != nil {
				return err
			}
			continue
		}
		if err := s.t.items.Put(tx, k, &heir); err != nil {
			return err
		}
		if err := s.recomputeKey(tx, a, k, false); err != nil {
			return err
		}
	}
	return nil
}

// noVideoSightings answers, for each of keys, the item record naming the
// first stored sighting that claims it with no video. Removed sources'
// sightings are deleted before this runs, so every sighting found is live.
func (s *Store) noVideoSightings(r *bolttable.ReadTx, keys []media.Key) (map[media.Key]itemRow, error) {
	want := make(map[media.Key]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	heirs := map[media.Key]itemRow{}
	err := s.t.sightings.Scan(r, bolttable.Range{}, func(ref itemRef, v *sightingRow) (bool, error) {
		if v.Key == nil || v.VideoPath != "" || !want[v.Key.k] {
			return true, nil
		}
		if _, taken := heirs[v.Key.k]; !taken {
			heirs[v.Key.k] = itemRow{Source: ref.Source, Item: ref.Item, Root: nativeRefRow{ID: ref.Root.ID, Kind: uint8(ref.Root.Kind)}}
		}
		return true, nil
	})
	return heirs, err
}

// loseKey deletes k's item record, coverage and locks.
func (s *Store) loseKey(tx *bolttable.Tx, k media.Key) error {
	if _, err := s.t.items.Delete(tx, k); err != nil {
		return err
	}
	if _, err := s.t.coverage.Delete(tx, k); err != nil {
		return err
	}
	return deleteRange(tx, s.t.locks, k.Bytes())
}

// dropOverlapsNaming deletes every overlap row whose owner was removed.
func (s *Store) dropOverlapsNaming(tx *bolttable.Tx, removed map[media.SourceID]bool) error {
	return deleteWhere(tx, s.t.overlaps, bolttable.Range{}, func(v *overlapRow) bool { return removed[v.Owner] })
}
