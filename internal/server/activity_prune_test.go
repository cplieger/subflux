package server

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
)

// The server's prune ticker is the one owner of activity retention: a
// completed entry leaves the log within [PruneAge, PruneAge + one tick) of
// ending — here the ticker starts with the entry's end, so eligibility (a
// strict age > PruneAge) lands on the tick after the PruneAge one — and its
// removal fires the remove hook (the ticker-prune arm of "remove observed
// from all three removal paths").
func TestRunActivityPrune_prunes_on_ticker_and_fires_remove(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := &Server{activity: activity.New(10)}
		var (
			mu      sync.Mutex
			removed []activity.Entry
		)
		s.activity.SetOnRemove(func(e activity.Entry) {
			mu.Lock()
			defer mu.Unlock()
			removed = append(removed, e)
		})
		id := s.activity.Start("Scan", "d", activity.SourceManual)
		s.activity.End(id)

		ctx, cancel := context.WithCancel(t.Context())
		s.bgWg.Go(func() { s.runActivityPrune(ctx) })

		// Just before retention age: every tick so far ran, nothing pruned.
		time.Sleep(activity.DefaultPruneAge - time.Second)
		synctest.Wait()
		if n := len(s.activity.Entries()); n != 1 {
			t.Fatalf("entry pruned after %v, want it kept until PruneAge", activity.DefaultPruneAge-time.Second)
		}

		// One tick past the first eligible instant: gone, remove observed.
		time.Sleep(time.Second + activityPruneInterval)
		synctest.Wait()
		if n := len(s.activity.Entries()); n != 0 {
			t.Fatalf("entry still present %v past PruneAge, want pruned within one tick", activityPruneInterval)
		}
		mu.Lock()
		if len(removed) != 1 || removed[0].ID != id || !removed[0].Done {
			t.Fatalf("ticker prune remove events = %+v, want one for entry %s", removed, id)
		}
		mu.Unlock()

		cancel()
		s.bgWg.Wait()
	})
}
