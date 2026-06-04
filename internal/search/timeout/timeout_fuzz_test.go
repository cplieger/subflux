package timeout

import (
	"errors"
	"testing"
	"time"

	"subflux/internal/api"
)

func FuzzTrackerOperations(f *testing.F) {
	f.Add("provider-1", uint8(5), true)
	f.Add("", uint8(0), false)
	f.Add("opensubtitles", uint8(10), true)

	f.Fuzz(func(t *testing.T, provID string, failures uint8, doReset bool) {
		now := time.Now()
		clock := func() time.Time { return now }
		tracker := New(Config{
			Threshold: 3,
			Window:    10 * time.Minute,
			Cooldown:  5 * time.Minute,
			Now:       clock,
		})

		prov := api.ProviderID(provID)
		n := int(failures) % 20

		for range n {
			tracker.RecordFailure(prov, errors.New("err"))
		}

		timedOut := tracker.IsTimedOut(prov)
		if n >= 3 && !timedOut {
			t.Error("should be timed out after threshold failures")
		}
		if n < 3 && timedOut {
			t.Error("should not be timed out below threshold")
		}

		status := tracker.Status()
		if n > 0 {
			if s, ok := status[prov]; ok {
				if s.TimedOut != timedOut {
					t.Error("status.TimedOut disagrees with IsTimedOut")
				}
			}
		}

		if doReset {
			tracker.Reset()
			if tracker.IsTimedOut(prov) {
				t.Error("still timed out after Reset")
			}
		}
	})
}
