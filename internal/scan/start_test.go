package scan_test

import (
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/status"
)

// recorded counts every job status holds, live or ended.
func (r *rig) recorded() int {
	snap, _ := r.jobs.Snapshot()
	return len(snap.Jobs)
}

func refetch(owner int64, n int) scan.Request {
	ref := movieRef(n)
	return scan.Request{Owner: owner, Source: src, Root: &ref}
}

func causeOf(err error) mediasource.Cause {
	if se, ok := errors.AsType[*mediasource.SourceError](err); ok {
		return se.Cause
	}
	return 0
}

func TestStartPerOwnerCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		for n := 1; n <= 4; n++ {
			if _, err := r.scanner.Start(t.Context(), refetch(1, n)); err != nil {
				t.Fatalf("owner A's refetch %d: %v", n, err)
			}
		}
		before := r.recorded()
		if _, err := r.scanner.Start(t.Context(), refetch(1, 5)); !errors.Is(err, scan.ErrScanCapacity) {
			t.Errorf("owner A's 5th refetch = %v, want ErrScanCapacity", err)
		}
		if n := r.recorded(); n != before {
			t.Errorf("the refused refetch recorded %d jobs", n-before)
		}
		if _, err := r.scanner.Start(t.Context(), refetch(1, 1)); err != nil {
			t.Errorf("owner A joining its queued refetch of movie 1 = %v, want no slot taken", err)
		}
		if _, err := r.scanner.Start(t.Context(), refetch(2, 5)); err != nil {
			t.Errorf("owner B's 1st refetch = %v, want it accepted", err)
		}
		for i := range 20 {
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: int64(10 + i%5), Source: src}); err != nil {
				t.Fatalf("whole request %d: %v", i, err)
			}
		}
		for n := 6; n <= 15; n++ {
			if _, err := r.scanner.Start(t.Context(), refetch(int64(100+n), n)); err != nil {
				t.Fatalf("refetch of movie %d: %v, want room for 16 slots with the whole requests in one", n, err)
			}
		}
		if _, err := r.scanner.Start(t.Context(), refetch(200, 16)); !errors.Is(err, scan.ErrScanCapacity) {
			t.Errorf("a 17th slot = %v, want ErrScanCapacity", err)
		}
	})
}

// TestStartCapacityConcurrent runs on the real clock: the second Start waits
// on the admission mutex, which never lets a synctest bubble's clock move.
func TestStartCapacityConcurrent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner int64
		hold  func(*testing.T, *rig)
	}{
		{"the source's last slot", 50, func(t *testing.T, r *rig) {
			for n := 1; n <= 15; n++ {
				if _, err := r.scanner.Start(t.Context(), refetch(int64(n), n)); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"the owner's last slot", 1, func(t *testing.T, r *rig) {
			for n := 1; n <= 3; n++ {
				if _, err := r.scanner.Start(t.Context(), refetch(1, n)); err != nil {
					t.Fatal(err)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, movieCaps, daily)
			tc.hold(t, r)
			before := r.recorded()
			var entered sync.WaitGroup
			entered.Add(1)
			second := make(chan struct{})
			var calls int
			var mu sync.Mutex
			r.status.onBegin(func() {
				mu.Lock()
				calls++
				n := calls
				mu.Unlock()
				if n == 1 {
					entered.Done()
					select {
					case <-second:
					case <-time.After(200 * time.Millisecond):
					}
					return
				}
				close(second)
			})
			errs := make(chan error, 2)
			for _, n := range []int{40, 41} {
				go func() {
					_, err := r.scanner.Start(t.Context(), refetch(tc.owner, n))
					errs <- err
				}()
			}
			entered.Wait()
			accepted, refused := 0, 0
			for range 2 {
				switch err := <-errs; {
				case err == nil:
					accepted++
				case errors.Is(err, scan.ErrScanCapacity):
					refused++
				default:
					t.Errorf("Start = %v", err)
				}
			}
			if accepted != 1 || refused != 1 {
				t.Errorf("%d accepted and %d refused, want one each", accepted, refused)
			}
			if n := r.recorded() - before; n != 1 {
				t.Errorf("%d jobs recorded, want only the accepted one", n)
			}
			r.status.onBegin(nil)
			if _, err := r.scanner.Start(t.Context(), refetch(tc.owner, 42)); !errors.Is(err, scan.ErrScanCapacity) {
				t.Errorf("a further refetch = %v, want ErrScanCapacity at the bound", err)
			}
		})
	}
}

func TestStartRefusesUnknownSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: "gone"}); causeOf(err) != mediasource.UnknownSource {
			t.Errorf("Start for an unknown source = %v, want UnknownSource", err)
		}
		show := mediasource.NativeRef{ID: "1", Kind: media.Episode}
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Root: &show}); causeOf(err) != mediasource.Unsupported {
			t.Errorf("a refetch of a kind the source does not serve = %v, want Unsupported", err)
		}
		if n := r.recorded(); n != 0 {
			t.Errorf("refused starts recorded %d jobs", n)
		}
	})
}

func TestStartOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 4)
		key := &status.JobKey{Key: "idem", Fingerprint: sha256.Sum256([]byte("scan src"))}
		first, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Key: key})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := r.job(t, first.ID); got.Outcome != status.Succeeded {
			t.Fatalf("the keyed pass is %v %v, want Succeeded", got.State, got.Outcome)
		}
		for n := 1; n <= 4; n++ {
			if _, err := r.scanner.Start(t.Context(), refetch(1, n)); err != nil {
				t.Fatal(err)
			}
		}
		again, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Key: key})
		if err != nil || again.ID != first.ID || again.Outcome != status.Succeeded {
			t.Errorf("a keyed resend with the owner's slots full = %s %v, %v; want the recorded job %s", again.ID, again.Outcome, err, first.ID)
		}
		show := mediasource.NativeRef{ID: "1", Kind: media.Episode}
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Root: &show}); causeOf(err) != mediasource.Unsupported {
			t.Errorf("an unsupported refetch with the owner full = %v, want Unsupported before capacity", err)
		}
		reused := &status.JobKey{Key: "idem", Fingerprint: sha256.Sum256([]byte("another body"))}
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Key: reused}); !errors.Is(err, status.ErrKeyReused) {
			t.Errorf("the key with another body = %v, want status's ErrKeyReused", err)
		}
	})
}

// TestKeyedResendAfterRemoval: a keyed request already recorded answers its
// job whatever its source has done since, a full queue or a Save removing
// the source included (design 5.3).
func TestKeyedResendAfterRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		b := newAdapter(&sourcetest.Library{}, movieCaps)
		r.over(setOf(t, map[media.SourceID]*adapter{src: r.ad, other: b}))
		r.start(t)
		advance(30 * time.Second)
		keyed := scan.Request{Owner: 1, Source: other, Key: &status.JobKey{Key: "idem", Fingerprint: sha256.Sum256([]byte("scan other"))}}
		first, err := r.scanner.Start(t.Context(), keyed)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := r.job(t, first.ID); got.Outcome != status.Succeeded {
			t.Fatalf("the keyed pass is %v %v, want Succeeded, so a resend would need a new slot", got.State, got.Outcome)
		}
		for n := 1; n <= 16; n++ {
			ref := movieRef(n)
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: int64(n + 1), Source: other, Root: &ref}); err != nil {
				t.Fatalf("refetch %d: %v", n, err)
			}
		}
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: other}); !errors.Is(err, scan.ErrScanCapacity) {
			t.Fatalf("an unkeyed whole request = %v, want the source's 16 slots full", err)
		}
		before := r.recorded()
		again, err := r.scanner.Start(t.Context(), keyed)
		if err != nil || again.ID != first.ID {
			t.Errorf("a keyed resend with the queue full = %s, %v; want the recorded job %s", again.ID, err, first.ID)
		}
		if n := r.recorded() - before; n != 0 {
			t.Errorf("the keyed resend recorded %d jobs", n)
		}
		r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: r.ad}), nil, nil)
		again, err = r.scanner.Start(t.Context(), keyed)
		if err != nil || again.ID != first.ID {
			t.Errorf("a keyed resend after its source was removed = %s, %v; want the recorded job %s", again.ID, err, first.ID)
		}
		if n := r.recorded() - before; n != 0 {
			t.Errorf("the keyed resends recorded %d jobs", n)
		}
	})
}
