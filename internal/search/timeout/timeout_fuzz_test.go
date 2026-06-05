package timeout

import (
	"errors"
	"testing"
	"time"

	"subflux/internal/api"
)

func FuzzTrackerRecordFailure(f *testing.F) {
	f.Add("provider-a", "connection timeout", 3)
	f.Add("", "", 0)
	f.Add("prov", "err", 10)

	f.Fuzz(func(t *testing.T, provName, errMsg string, count int) {
		if count < 0 {
			count = -count
		}
		count = count%20 + 1

		now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		tracker := New(Config{
			Now:       func() time.Time { return now },
			Threshold: 5,
			Window:    10 * time.Minute,
			Cooldown:  time.Hour,
		})

		prov := api.ProviderID(provName)
		var e error
		if errMsg != "" {
			e = errors.New(errMsg)
		}
		for range count {
			tracker.RecordFailure(prov, e)
		}
		_ = tracker.IsTimedOut(prov)
		_ = tracker.Status()
	})
}
