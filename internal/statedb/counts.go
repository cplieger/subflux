package statedb

import (
	"cmp"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// CoverageCount is one configured pair summed over a root's items of owned
// instances that are not excluded: Exact, Substitute and Missing partition
// Items, and Locked counts set locks whatever the coverage.
type CoverageCount struct {
	Target     language.Tag
	Items      int
	Exact      int
	Substitute int
	Missing    int
	Locked     int
	Variant    language.Variant
}

// recount rewrites each root's counts row from its source rows as they
// stand in tx. beforeCommit calls it for every root a change names, so a
// writer cannot leave a count stale; byte-equal rows write nothing.
func (s *Store) recount(tx *bolttable.Tx, roots []media.RootKey) error {
	for _, root := range roots {
		row, err := s.countRoot(tx.Read(), root)
		if err != nil {
			return err
		}
		if len(row.Pairs) == 0 {
			if _, err := s.t.counts.Delete(tx, root); err != nil {
				return err
			}
		} else if err := s.t.counts.Put(tx, root, &row); err != nil {
			return err
		}
	}
	return nil
}

// countRoot sums root's coverage rows and locks over the items it counts:
// those whose record names an owned instance and a sighting not excluded.
func (s *Store) countRoot(r *bolttable.ReadTx, root media.RootKey) (countsRow, error) {
	c := counter{sums: map[pairID]*countPair{}, owned: map[uint64]bool{}, locks: map[lockKey]bool{}}
	if err := s.t.locks.Scan(r, bolttable.Range{Prefix: root.Bytes()}, func(lk lockKey, _ *lockRow) (bool, error) {
		c.locks[lk] = true
		return true, nil
	}); err != nil {
		return countsRow{}, err
	}
	if err := s.t.items.Scan(r, bolttable.Range{Prefix: root.Bytes()}, func(k media.Key, rec *itemRow) (bool, error) {
		counted, err := s.counted(r, rec, c.owned)
		if err != nil || !counted {
			return err == nil, err
		}
		cov, _, err := s.t.coverage.Get(r, k)
		if err != nil {
			return false, err
		}
		c.add(k, &cov)
		return true, nil
	}); err != nil {
		return countsRow{}, err
	}
	row := countsRow{Pairs: make([]countPair, 0, len(c.sums))}
	for _, p := range c.sums {
		row.Pairs = append(row.Pairs, *p)
	}
	slices.SortFunc(row.Pairs, func(a, b countPair) int {
		return cmp.Or(cmp.Compare(a.Tag.t.String(), b.Tag.t.String()), cmp.Compare(a.Variant, b.Variant))
	})
	return row, nil
}

type pairID struct {
	tag language.Tag
	v   language.Variant
}

// counter is one root's recount in progress.
type counter struct {
	sums  map[pairID]*countPair
	owned map[uint64]bool
	locks map[lockKey]bool
}

func (c *counter) add(k media.Key, cov *coverageRow) {
	for _, p := range cov.Pairs {
		id := pairID{tag: p.Tag.t, v: language.Variant(p.Variant)}
		sum := c.sums[id]
		if sum == nil {
			sum = &countPair{Tag: p.Tag, Variant: p.Variant}
			c.sums[id] = sum
		}
		sum.Items++
		switch Coverage(p.Coverage) {
		case Exact:
			sum.Exact++
		case Substitute:
			sum.Substitute++
		case Missing:
			sum.Missing++
		}
		if c.locks[lockKey{Key: k, Tag: id.tag, Variant: id.v}] {
			sum.Locked++
		}
	}
}

// counted reports whether rec's item counts toward its root: it names an
// owned instance, cached in owned by id, and a sighting not excluded.
func (s *Store) counted(r *bolttable.ReadTx, rec *itemRow, owned map[uint64]bool) (bool, error) {
	if rec.Inst == 0 {
		return false, nil
	}
	isOwned, seen := owned[rec.Inst]
	if !seen {
		inst, found, err := s.t.instances.Get(r, InstanceID(rec.Inst))
		if err != nil {
			return false, err
		}
		isOwned = found && inst.Owner != ""
		owned[rec.Inst] = isOwned
	}
	if !isOwned {
		return false, nil
	}
	sg, found, err := s.t.sightings.Get(r, rec.ref())
	return found && !sg.Excluded, err
}

// rootCounts is root's stored counts in the order of targets, the policy's
// Targets; a pair the policy no longer lists follows in stored order.
func (s *Store) rootCounts(r *bolttable.ReadTx, root media.RootKey, targets []language.Target) ([]CoverageCount, error) {
	row, _, err := s.t.counts.Get(r, root)
	if err != nil || len(row.Pairs) == 0 {
		return nil, err
	}
	rank := map[pairID]int{}
	for _, t := range targets {
		for _, v := range t.Variants {
			rank[pairID{t.Tag, v}] = len(rank)
		}
	}
	at := func(c *CoverageCount) int {
		if i, ok := rank[pairID{c.Target, c.Variant}]; ok {
			return i
		}
		return len(rank)
	}
	out := make([]CoverageCount, 0, len(row.Pairs))
	for _, p := range row.Pairs {
		out = append(out, CoverageCount{
			Target: p.Tag.t, Variant: language.Variant(p.Variant), Items: p.Items,
			Exact: p.Exact, Substitute: p.Substitute, Missing: p.Missing, Locked: p.Locked,
		})
	}
	slices.SortStableFunc(out, func(a, b CoverageCount) int { return cmp.Compare(at(&a), at(&b)) })
	return out, nil
}
