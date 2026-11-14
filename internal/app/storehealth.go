package app

import (
	"context"
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// writeHealth is the store's write outcomes; *statedb.Store implements it.
type writeHealth interface{ WriteHealth() statedb.WriteHealth }

var _ writeHealth = (*statedb.Store)(nil)

// storeHealthTick is how often storeHealth reads the store's write outcomes.
const storeHealthTick = 30 * time.Second

// storeHealth raises the store alert on each tick while the store's latest
// write failure is newer than its latest commit, and clears it otherwise,
// until ctx ends.
func storeHealth(ctx context.Context, h writeHealth, st *status.Store) error {
	t := time.NewTicker(storeHealthTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		w := h.WriteHealth()
		failing := w.LastFailed.After(w.LastOK)
		text := ""
		if failing && w.Last != nil {
			text = fmt.Sprintf("the state store could not write (%d failed writes since start): %v", w.Failures, w.Last)
		}
		st.Reconcile(status.AlertStore, failing, text)
	}
}
