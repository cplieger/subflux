package polling

import (
	"context"
	"testing"
	"time"

	"subflux/internal/api"
)

// FuzzPollCacheRoundtrip verifies that Set(k, t) followed by Get(k) returns
// t (write-through cache roundtrip invariant).
func FuzzPollCacheRoundtrip(f *testing.F) {
	f.Add(int64(1717500000), true)
	f.Add(int64(0), false)
	f.Add(int64(-1), true)

	f.Fuzz(func(t *testing.T, unixSec int64, useSonarr bool) {
		ts := time.Unix(unixSec, 0).UTC()
		key := api.PollKeySonarr
		if !useSonarr {
			key = api.PollKeyRadarr
		}

		var stored time.Time
		cache := NewPollCache(
			func(_ context.Context, _ api.PollKey) (time.Time, error) {
				return time.Time{}, nil
			},
			func(_ context.Context, _ api.PollKey, t time.Time) error {
				stored = t
				return nil
			},
		)

		ctx := context.Background()
		cache.Set(ctx, key, ts)

		// Verify DB write-through.
		if !stored.Equal(ts) {
			t.Fatalf("write-through failed: stored=%v, want=%v", stored, ts)
		}

		// Verify in-memory roundtrip.
		got := cache.Get(ctx, key)
		if !got.Equal(ts) {
			t.Fatalf("Get after Set: got=%v, want=%v", got, ts)
		}
	})
}
