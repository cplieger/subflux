package polling

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/api"
)

func TestPollCache_Get_calls_readFn_on_miss(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	expected := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pc := NewPollCache(
		func(_ context.Context, key api.PollKey) (time.Time, error) {
			calls.Add(1)
			return expected, nil
		},
		func(_ context.Context, _ api.PollKey, _ time.Time) error { return nil },
	)
	got := pc.Get(context.Background(), api.PollKey("sonarr"))
	if !got.Equal(expected) {
		t.Errorf("Get() = %v, want %v", got, expected)
	}
	if calls.Load() != 1 {
		t.Errorf("readFn called %d times, want 1", calls.Load())
	}
}

func TestPollCache_Get_returns_cached_after_Set(t *testing.T) {
	t.Parallel()
	var readCalls atomic.Int32
	pc := NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) {
			readCalls.Add(1)
			return time.Time{}, nil
		},
		func(_ context.Context, _ api.PollKey, _ time.Time) error { return nil },
	)
	now := time.Now()
	pc.Set(context.Background(), api.PollKey("radarr"), now)
	got := pc.Get(context.Background(), api.PollKey("radarr"))
	if !got.Equal(now) {
		t.Errorf("Get() after Set = %v, want %v", got, now)
	}
	if readCalls.Load() != 0 {
		t.Errorf("readFn called %d times after Set, want 0", readCalls.Load())
	}
}

func TestPollCache_Set_with_failing_setFn_still_caches(t *testing.T) {
	t.Parallel()
	pc := NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) {
			return time.Time{}, errors.New("should not be called")
		},
		func(_ context.Context, _ api.PollKey, _ time.Time) error {
			return errors.New("db write failed")
		},
	)
	now := time.Now()
	pc.Set(context.Background(), api.PollKey("key"), now)
	got := pc.Get(context.Background(), api.PollKey("key"))
	if !got.Equal(now) {
		t.Errorf("Get() after failed Set = %v, want %v", got, now)
	}
}

func TestPollCache_concurrent(t *testing.T) {
	t.Parallel()
	pc := NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) {
			return time.Now(), nil
		},
		func(_ context.Context, _ api.PollKey, _ time.Time) error { return nil },
	)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			if i%2 == 0 {
				pc.Set(context.Background(), api.PollKey("k"), time.Now())
			} else {
				pc.Get(context.Background(), api.PollKey("k"))
			}
		})
	}
	wg.Wait()
}

// dirtyWarnMsg is the S13 dirty-cursor WARN emitted when a durable write fails.
const dirtyWarnMsg = "PollCache: durable cursor write failed; in-memory position is ahead of disk (restart would replay)"

// A failed write-through (setFn error) must be WARN-logged and mark the
// cursor dirty; the cache still advances (verified by
// TestPollCache_Set_with_failing_setFn_still_caches).
func TestPollCacheSet_warns_when_setFn_errors(t *testing.T) {
	sink := capture.Default(t)
	pc := NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) { return time.Time{}, nil },
		func(_ context.Context, _ api.PollKey, _ time.Time) error { return errors.New("db boom") },
	)
	pc.Set(context.Background(), api.PollKeySonarr, time.Now())
	if sink.CountLevel(slog.LevelWarn, dirtyWarnMsg) == 0 {
		t.Errorf("Set with failing setFn: want the dirty-cursor WARN")
	}
	if got := pc.DirtyCount(); got != 1 {
		t.Errorf("DirtyCount after failed persist = %d, want 1", got)
	}
}

// A successful write-through must not emit the dirty WARN or mark dirty.
func TestPollCacheSet_silent_when_setFn_ok(t *testing.T) {
	sink := capture.Default(t)
	pc := NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) { return time.Time{}, nil },
		func(_ context.Context, _ api.PollKey, _ time.Time) error { return nil },
	)
	pc.Set(context.Background(), api.PollKeySonarr, time.Now())
	if sink.CountLevel(slog.LevelWarn, dirtyWarnMsg) > 0 {
		t.Errorf("Set with ok setFn: unexpected dirty-cursor WARN")
	}
	if got := pc.DirtyCount(); got != 0 {
		t.Errorf("DirtyCount after clean persist = %d, want 0", got)
	}
}

// RetryDirty persists the CURRENT in-memory position once the store heals,
// clears the dirty state, announces recovery, and drives the gauge 1 -> 0.
func TestPollCacheRetryDirty_heals_and_persists_latest(t *testing.T) {
	sink := capture.Default(t)
	var failing atomic.Bool
	failing.Store(true)
	var persisted []time.Time
	var gaugeVals []int

	pc := NewPollCache(
		func(_ context.Context, _ api.PollKey) (time.Time, error) { return time.Time{}, nil },
		func(_ context.Context, _ api.PollKey, t time.Time) error {
			if failing.Load() {
				return errors.New("disk full")
			}
			persisted = append(persisted, t)
			return nil
		},
	)
	pc.SetDirtyGauge(func(n int) { gaugeVals = append(gaugeVals, n) })

	first := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	pc.Set(context.Background(), api.PollKeySonarr, first)  // fails -> dirty
	pc.Set(context.Background(), api.PollKeySonarr, second) // fails -> still dirty, memory advanced

	// While dirty, retries against a still-failing store keep it dirty.
	pc.RetryDirty(context.Background())
	if got := pc.DirtyCount(); got != 1 {
		t.Fatalf("DirtyCount while store failing = %d, want 1", got)
	}

	failing.Store(false)
	pc.RetryDirty(context.Background())
	if got := pc.DirtyCount(); got != 0 {
		t.Errorf("DirtyCount after heal = %d, want 0", got)
	}
	if len(persisted) != 1 || !persisted[0].Equal(second) {
		t.Errorf("persisted = %v, want exactly the LATEST in-memory position %v", persisted, second)
	}
	if sink.CountLevel(slog.LevelInfo, "PollCache: dirty cursor persisted; memory and disk agree again") == 0 {
		t.Errorf("want recovery INFO after heal")
	}
	if len(gaugeVals) == 0 || gaugeVals[len(gaugeVals)-1] != 0 {
		t.Errorf("gauge transitions = %v, want final 0", gaugeVals)
	}
	// Memory still serves the advanced position throughout.
	if got := pc.Get(context.Background(), api.PollKeySonarr); !got.Equal(second) {
		t.Errorf("Get = %v, want in-memory %v", got, second)
	}
}
