package statedb

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// NoResult is how many searches of Provider for Key at target Tag found
// nothing, and when the next may run.
type NoResult struct {
	Next     time.Time
	Provider string
	Tag      language.Tag
	Key      media.Key
	Count    int
}

// NoResults returns every row of root's season (0 for a movie) with one
// prefix scan, in key, tag and provider order.
func (s *Store) NoResults(ctx context.Context, root media.RootKey, season uint16) ([]NoResult, error) {
	prefix := binary.BigEndian.AppendUint16(root.Bytes(), season)
	var out []NoResult
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		return s.t.noResults.Scan(r, bolttable.Range{Prefix: prefix}, func(k noResultKey, v *noResultRow) (bool, error) {
			out = append(out, NoResult{Key: k.Key, Tag: k.Tag, Provider: k.Provider, Count: v.Count, Next: v.Next})
			return true, nil
		})
	})
	return out, err
}

// PutNoResults writes rows in one write: a row with Count 0 deletes the
// stored one, and a row for a key with no item record is skipped, since the
// item record's deletion is what removes its rows. A negative Count is
// refused, writing nothing.
func (s *Store) PutNoResults(ctx context.Context, rows []NoResult) error {
	for i := range rows {
		if rows[i].Count < 0 {
			return fmt.Errorf("statedb: no-result count %d is negative", rows[i].Count)
		}
	}
	return s.write(ctx, "put no results", s.db.Update, func(tx *bolttable.Tx) error {
		for i := range rows {
			if err := s.putNoResult(tx, &rows[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) putNoResult(tx *bolttable.Tx, nr *NoResult) error {
	k := noResultKey{Key: nr.Key, Tag: nr.Tag, Provider: nr.Provider}
	if nr.Count == 0 {
		_, err := s.t.noResults.Delete(tx, k)
		return err
	}
	_, recorded, err := s.t.items.Get(tx.Read(), nr.Key)
	if err != nil || !recorded {
		return err
	}
	return s.t.noResults.Put(tx, k, &noResultRow{Count: nr.Count, Next: nr.Next.UTC().Round(0)})
}
