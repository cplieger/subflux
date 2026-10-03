package scheduler_test

import (
	"context"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/scheduler"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// scriptedMedia fails a preflight while the matching flag is set: retry for
// the scheduler's own {Roots, Raise} request, scan for the full scan's
// {Roots, RecheckBad, Raise} request.
type scriptedMedia struct {
	mu                 sync.Mutex
	failRetry, failScn bool
	retries, scans     int
}

func (m *scriptedMedia) Preflight(_ context.Context, req mediawrite.PreflightRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	fail := m.failRetry
	if req.RecheckBad {
		m.scans++
		fail = m.failScn
	} else {
		m.retries++
	}
	if fail {
		return &mediawrite.UnwritableError{Folder: "/media", Root: "/media", Op: "probe", Err: syscall.EROFS}
	}
	return nil
}

func (m *scriptedMedia) set(failRetry, failScan bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failRetry, m.failScn = failRetry, failScan
}

func (m *scriptedMedia) counts() (retries, scans int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retries, m.scans
}

func (*scriptedMedia) Blocked(string) (string, bool) { return "", false }

// countingStore counts reconcile passes, the DB maintenance a refused retry
// must skip.
type countingStore struct {
	*testsupport.NopStore
	mu         sync.Mutex
	reconciles int
}

func (c *countingStore) ReconcileState(context.Context, func(context.Context, string) (bool, error), func(string) (string, bool)) (subflux.ReconcileResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reconciles++
	return subflux.ReconcileResult{}, nil
}

func (c *countingStore) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reconciles
}

const testScanInterval = 24 * time.Hour

func retryRig(media *scriptedMedia) (*scheduler.Deps, *countingStore, *activity.Log) {
	store := &countingStore{NopStore: &testsupport.NopStore{}}
	log := activity.New(50)
	deps := prepDeps(log, &activity.StopRegistry{}, nil)
	deps.DB = store
	deps.Media = media
	deps.StateFunc = func() *scheduler.LiveState {
		return &scheduler.LiveState{Cfg: &fakeScanCfg{searchCfg: subflux.SearchConfig{ScanInterval: testScanInterval}}}
	}
	return deps, store, log
}

func TestRun_retries_a_refused_scan_after_15_minutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		media := &scriptedMedia{}
		media.set(true, true)
		deps, store, log := retryRig(media)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { scheduler.Run(ctx, deps); close(done) }()

		time.Sleep(scheduler.StartupDelay)
		synctest.Wait()
		if store.count() != 1 || len(log.Entries()) != 1 {
			t.Fatalf("first cycle: %d reconciles, %d scans; want 1 and 1", store.count(), len(log.Entries()))
		}

		time.Sleep(scheduler.MediaRetryInterval - time.Second)
		synctest.Wait()
		if r, _ := media.counts(); r != 0 {
			t.Fatalf("the retry ran before %v", scheduler.MediaRetryInterval)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if r, _ := media.counts(); r != 1 || store.count() != 1 || len(log.Entries()) != 1 {
			t.Fatalf("refused retry: %d retry preflights, %d reconciles, %d scans; want 1, 1, 1", r, store.count(), len(log.Entries()))
		}

		media.set(false, false)
		time.Sleep(scheduler.MediaRetryInterval)
		synctest.Wait()
		if store.count() != 2 || len(log.Entries()) != 2 {
			t.Fatalf("passing retry: %d reconciles, %d scans; want 2 and 2", store.count(), len(log.Entries()))
		}

		time.Sleep(testScanInterval - time.Second)
		synctest.Wait()
		if store.count() != 2 {
			t.Fatal("after a clean scan the next cycle came before scan_interval")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if r, _ := media.counts(); store.count() != 3 || r != 2 {
			t.Errorf("after a clean scan: %d reconciles, %d retry preflights; want 3 and 2 (no retry preflight)", store.count(), r)
		}
		cancel()
		<-done
	})
}

func TestRun_a_scan_reporting_an_unwritable_folder_retries_even_when_the_roots_pass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		media := &scriptedMedia{}
		media.set(false, true)
		deps, store, log := retryRig(media)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { scheduler.Run(ctx, deps); close(done) }()

		time.Sleep(scheduler.StartupDelay)
		synctest.Wait()
		media.set(false, false)
		time.Sleep(scheduler.MediaRetryInterval)
		synctest.Wait()
		if r, s := media.counts(); r != 1 || s != 2 || store.count() != 2 || len(log.Entries()) != 2 {
			t.Fatalf("retry: %d retry preflights, %d scan preflights, %d reconciles, %d scans; want 1, 2, 2, 2",
				r, s, store.count(), len(log.Entries()))
		}

		time.Sleep(scheduler.MediaRetryInterval)
		synctest.Wait()
		if store.count() != 2 {
			t.Fatal("a clean retry was followed by another 15-minute retry")
		}
		time.Sleep(testScanInterval - scheduler.MediaRetryInterval)
		synctest.Wait()
		if store.count() != 3 {
			t.Errorf("reconciles = %d, want 3 at scan_interval after the clean retry", store.count())
		}
		cancel()
		<-done
	})
}
