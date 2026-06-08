package polling

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
