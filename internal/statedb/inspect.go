package statedb

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
)

// InspectOptions selects what Inspect checks beyond counts and sizes.
// Repair implies Verify.
type InspectOptions struct{ Verify, Repair bool }

// TableStat is one table's row count and the bytes its pages use.
type TableStat struct {
	Name  string
	Rows  int
	Bytes int64
}

// Mismatch is one table, index or counter, named namespace.name, or the
// derived coverage or counts rows, named statedb.coverage and
// statedb.counts, whose stored state differs from a rescan. For derived
// rows, Missing counts keys whose row is absent or differs and Extra keys
// whose row is present but unwanted or differs.
type Mismatch struct {
	Name                        string
	Missing, Extra, Undecodable int
	Repaired                    bool
}

// NamespaceStat is one namespace's stored format digest and its tables.
type NamespaceStat struct {
	Name   string
	Tables []TableStat
	Format [32]byte
}

// Inspection is the store's counts and sizes, and with Verified the
// mismatches a rescan found. It holds no key, row value or secret.
type Inspection struct {
	Namespaces     []NamespaceStat
	Mismatches     []Mismatch
	Rev            uint64
	OldestRetained uint64
	ConfigRev      uint64
	Commits        uint64
	FileBytes      int64
	FreeBytes      int64
	Epoch          [16]byte
	Verified       bool
}

// The Mismatch names of the derived-row checks, outside every table's
// namespace.name, so they never read as one of bolttable's.
const (
	coverageName = "statedb.coverage"
	countsName   = "statedb.counts"
)

// Inspect reports the store's revisions, commits, file space and per-table
// sizes. With Verify it rescans every index and counter, recomputes every
// owned item's coverage under the activation in force and recounts every
// root; with Repair it rewrites the index entries, counters, coverage and
// counts rows that differ, never another row. A row a recompute reads that
// does not decode fails Inspect with ErrCorrupt naming its table.
// Verify before the first Activate is an error.
func (s *Store) Inspect(ctx context.Context, o InspectOptions) (Inspection, error) {
	var in Inspection
	if o.Verify || o.Repair {
		var err error
		if in.Mismatches, err = s.verify(ctx, o.Repair); err != nil {
			return Inspection{}, fmt.Errorf("statedb: inspect: %w", err)
		}
		in.Verified = true
	}
	if err := s.inspectStats(ctx, &in); err != nil {
		return Inspection{}, fmt.Errorf("statedb: inspect: %w", err)
	}
	return in, nil
}

func (s *Store) inspectStats(ctx context.Context, in *Inspection) error {
	// config_rev is read before Stats, so ConfigRev never exceeds Rev.
	if err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		act, _, err := s.t.activation.Get(r, constKey{})
		in.ConfigRev = act.ConfigRev
		return err
	}); err != nil {
		return err
	}
	st, err := s.db.Stats(ctx)
	if err != nil {
		return err
	}
	in.Epoch = s.db.Epoch()
	in.Rev, in.OldestRetained, in.Commits = st.Rev, st.Oldest, st.Commits
	in.FileBytes, in.FreeBytes = st.Space.FileBytes, st.Space.FreeBytes
	in.Namespaces = make([]NamespaceStat, 0, len(st.Namespaces))
	for _, ns := range st.Namespaces {
		tables := make([]TableStat, 0, len(ns.Tables))
		for _, t := range ns.Tables {
			tables = append(tables, TableStat{Name: t.Name, Rows: t.Rows, Bytes: t.Bytes})
		}
		in.Namespaces = append(in.Namespaces, NamespaceStat{Name: ns.Name, Format: ns.Format, Tables: tables})
	}
	return nil
}

// verify runs bolttable's rescan, then the coverage check and the counts
// check, all under the activation lock shared, so no Activate replaces the
// activation the coverage is judged under. Counts are checked after the
// coverage repair, whose write recounts the roots it touched.
func (s *Store) verify(ctx context.Context, repair bool) ([]Mismatch, error) {
	s.actMu.RLock()
	defer s.actMu.RUnlock()
	a := s.active.Load()
	if a == nil {
		return nil, errNoActivation
	}
	rep, err := s.db.Verify(ctx, repair)
	if err != nil {
		return nil, err
	}
	out := make([]Mismatch, 0, len(rep.Mismatches)+2)
	for _, m := range rep.Mismatches {
		out = append(out, Mismatch{Name: m.Name, Missing: m.Missing, Extra: m.Extra, Undecodable: m.Undecodable, Repaired: m.Repaired})
	}
	checks := []func(*bolttable.ReadTx) (func(*bolttable.Tx) error, Mismatch, error){
		func(r *bolttable.ReadTx) (func(*bolttable.Tx) error, Mismatch, error) {
			return driftOf(coverageName, s.t.coverage)(s.coverageDrift(r, a))
		},
		func(r *bolttable.ReadTx) (func(*bolttable.Tx) error, Mismatch, error) {
			return driftOf(countsName, s.t.counts)(s.countsDrift(r))
		},
	}
	for _, drift := range checks {
		m, err := s.verifyDerived(ctx, repair, drift)
		if err != nil {
			return nil, err
		}
		if m.Missing > 0 || m.Extra > 0 {
			out = append(out, m)
		}
	}
	return out, nil
}

// rowFix is one key's derived row as a recompute would leave it.
type rowFix[K, V any] struct {
	row V
	k   K
	// want is whether the recompute stores a row, held whether one is
	// stored now.
	want, held bool
}

// driftOf turns a drift scan of tb into the write that repairs it and its
// Mismatch.
func driftOf[K, V any](name string, tb *bolttable.Table[K, V]) func([]rowFix[K, V], error) (func(*bolttable.Tx) error, Mismatch, error) {
	return func(fixes []rowFix[K, V], err error) (func(*bolttable.Tx) error, Mismatch, error) {
		return func(tx *bolttable.Tx) error { return applyFixes(tx, tb, fixes) }, mismatchOf(name, fixes), err
	}
}

// mismatchOf counts a wanted row Missing and a stored row it replaces or
// deletes Extra.
func mismatchOf[K, V any](name string, fixes []rowFix[K, V]) Mismatch {
	m := Mismatch{Name: name}
	for _, f := range fixes {
		if f.want {
			m.Missing++
		}
		if f.held {
			m.Extra++
		}
	}
	return m
}

func applyFixes[K, V any](tx *bolttable.Tx, tb *bolttable.Table[K, V], fixes []rowFix[K, V]) error {
	for i := range fixes {
		f := &fixes[i]
		if !f.want {
			if _, err := tb.Delete(tx, f.k); err != nil {
				return err
			}
		} else if err := tb.Put(tx, f.k, &f.row); err != nil {
			return err
		}
	}
	return nil
}

// verifyDerived reports drift's Mismatch and, with repair, rewrites what is
// still wrong inside one write: a writer may have committed since the View.
func (s *Store) verifyDerived(ctx context.Context, repair bool, drift func(*bolttable.ReadTx) (func(*bolttable.Tx) error, Mismatch, error)) (Mismatch, error) {
	var m Mismatch
	if err := s.db.View(ctx, func(r *bolttable.ReadTx) (err error) {
		_, m, err = drift(r)
		return err
	}); err != nil {
		return Mismatch{}, err
	}
	if !repair || m.Missing == 0 && m.Extra == 0 {
		return m, nil
	}
	err := s.write(ctx, "inspect repair", s.db.Update, func(tx *bolttable.Tx) error {
		apply, again, err := drift(tx.Read())
		if err != nil {
			return err
		}
		m = again
		return apply(tx)
	})
	if err != nil {
		return Mismatch{}, err
	}
	m.Repaired = true
	return m, nil
}

// coverageDrift lists, in key order, every key whose stored coverage row
// differs from the recompute under a: an item record's wanted row, or none
// for a key with no item record. Items of ownerless instances keep their
// rows (design section 6), and locks are never touched.
func (s *Store) coverageDrift(r *bolttable.ReadTx, a *activation) ([]rowFix[media.Key, coverageRow], error) {
	stored := map[media.Key]coverageRow{}
	if err := s.t.coverage.Scan(r, bolttable.Range{}, func(k media.Key, row *coverageRow) (bool, error) {
		stored[k] = *row
		return true, nil
	}); err != nil {
		return nil, err
	}
	var keys []media.Key
	if err := s.t.items.Scan(r, bolttable.Range{}, func(k media.Key, _ *itemRow) (bool, error) {
		keys = append(keys, k)
		return true, nil
	}); err != nil {
		return nil, err
	}
	var fixes []rowFix[media.Key, coverageRow]
	for _, k := range keys {
		j, _, err := s.judgeKey(r, a, k)
		if err != nil {
			return nil, err
		}
		have, held := stored[k]
		delete(stored, k)
		if j.ownerless {
			continue
		}
		want, wanted := j.row(&have)
		if held != wanted || wanted && !slices.Equal(have.Pairs, want.Pairs) {
			fixes = append(fixes, rowFix[media.Key, coverageRow]{k: k, row: want, want: wanted, held: held})
		}
	}
	for k := range stored {
		fixes = append(fixes, rowFix[media.Key, coverageRow]{k: k, held: true})
	}
	slices.SortFunc(fixes, func(x, y rowFix[media.Key, coverageRow]) int { return bytes.Compare(x.k.Bytes(), y.k.Bytes()) })
	return fixes, nil
}

// countsDrift lists, in root order, every root whose stored counts row
// differs from a recount of its stored rows.
func (s *Store) countsDrift(r *bolttable.ReadTx) ([]rowFix[media.RootKey, countsRow], error) {
	roots := map[media.RootKey]bool{}
	if err := s.t.items.Scan(r, bolttable.Range{}, func(k media.Key, _ *itemRow) (bool, error) {
		roots[k.Root()] = true
		return true, nil
	}); err != nil {
		return nil, err
	}
	if err := s.t.counts.Scan(r, bolttable.Range{}, func(k media.RootKey, _ *countsRow) (bool, error) {
		roots[k] = true
		return true, nil
	}); err != nil {
		return nil, err
	}
	var fixes []rowFix[media.RootKey, countsRow]
	for root := range roots {
		want, err := s.countRoot(r, root)
		if err != nil {
			return nil, err
		}
		have, held, err := s.t.counts.Get(r, root)
		if err != nil {
			return nil, err
		}
		wanted := len(want.Pairs) > 0
		if held != wanted || wanted && !slices.Equal(have.Pairs, want.Pairs) {
			fixes = append(fixes, rowFix[media.RootKey, countsRow]{k: root, row: want, want: wanted, held: held})
		}
	}
	slices.SortFunc(fixes, func(x, y rowFix[media.RootKey, countsRow]) int { return bytes.Compare(x.k.Bytes(), y.k.Bytes()) })
	return fixes, nil
}
