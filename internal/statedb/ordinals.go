package statedb

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/cplieger/bolttable"
)

// ReserveOrdinal returns the next ordinal of k's numbered files: above
// highestOnDisk and above every ordinal it returned before for k, so none is
// ever reused while k's instance lives. It refuses a negative highestOnDisk,
// an unknown instance, and an ordinal past math.MaxInt, storing nothing; once
// math.MaxInt is returned every later call for k is refused, across restarts.
func (s *Store) ReserveOrdinal(ctx context.Context, k OrdinalKey, highestOnDisk int) (int, error) {
	if highestOnDisk < 0 {
		return 0, fmt.Errorf("statedb: highest ordinal on disk %d is negative", highestOnDisk)
	}
	var n uint64
	err := s.write(ctx, "reserve ordinal", s.db.Update, func(tx *bolttable.Tx) error {
		r := tx.Read()
		_, known, err := s.t.instances.Get(r, k.Inst)
		if err != nil {
			return err
		}
		if !known {
			return fmt.Errorf("statedb: reserve ordinal: instance %d is unknown", k.Inst)
		}
		row, _, err := s.t.ordinals.Get(r, k)
		if err != nil {
			return err
		}
		n = max(row.Next, uint64(highestOnDisk)+1) //nolint:gosec // G115: highestOnDisk >= 0, checked above
		if n > math.MaxInt {
			return errOrdinalsExhausted
		}
		return s.t.ordinals.Put(tx, k, &ordinalRow{Next: n + 1})
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

var errOrdinalsExhausted = errors.New("statedb: no ordinal is left below math.MaxInt")
