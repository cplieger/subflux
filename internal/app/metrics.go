package app

import (
	"context"

	"github.com/cplieger/subflux/internal/status"
)

type alertSeries struct{ kind, id string }

// alertGauge is the alerts_active family; *obs.Metrics implements it.
type alertGauge interface {
	SetAlertActive(kind, id string)
	DeleteAlertActive(kind, id string)
}

// alertMetrics mirrors the status store's active alerts into alerts_active
// on every status change until ctx ends. It sets each active series and
// deletes only those that cleared, never resetting the family: a scrape
// between a reset and the sets would see no alert and restart every pending
// rule's for: window.
func alertMetrics(ctx context.Context, st *status.Store, m alertGauge) error {
	shown := map[alertSeries]bool{}
	for {
		wake := st.Changed()
		snap, _ := st.Snapshot()
		active := make(map[alertSeries]bool, len(snap.Alerts))
		for _, a := range snap.Alerts {
			// Reconcile stores no source of an unknown kind.
			kind, id, _ := status.AlertKindID(a.Source)
			active[alertSeries{kind, id}] = true
			m.SetAlertActive(kind, id)
		}
		for s := range shown {
			if !active[s] {
				m.DeleteAlertActive(s.kind, s.id)
			}
		}
		shown = active
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		}
	}
}
