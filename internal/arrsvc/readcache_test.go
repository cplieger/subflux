package arrsvc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// stubFetch is a controllable upstream pass: it records each call's begin
// instant, optionally blocks a call until released (or the pass context
// dies), and answers via respond.
type stubFetch struct {
	respond func(n int) (any, error)
	gates   map[int]chan struct{}
	begins  []time.Time
	mu      sync.Mutex
}

func newStubFetch(respond func(n int) (any, error)) *stubFetch {
	return &stubFetch{respond: respond, gates: make(map[int]chan struct{})}
}

// callIndexFetch answers each call with its zero-based call index.
func callIndexFetch() *stubFetch {
	return newStubFetch(func(n int) (any, error) { return n, nil })
}

// gate makes call n block until the returned channel is closed.
func (s *stubFetch) gate(n int) chan struct{} {
	ch := make(chan struct{})
	s.mu.Lock()
	s.gates[n] = ch
	s.mu.Unlock()
	return ch
}

func (s *stubFetch) fn(ctx context.Context) (any, error) {
	s.mu.Lock()
	n := len(s.begins)
	s.begins = append(s.begins, time.Now())
	release := s.gates[n]
	s.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.respond(n)
}

func (s *stubFetch) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.begins)
}

func (s *stubFetch) begin(i int) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.begins[i]
}

type readOutcome struct {
	val        any
	err        error
	answeredAt time.Time
}

func newTestTable(ctx context.Context) *readTable {
	return newReadTable(NewReadGate(func() context.Context { return ctx }, nil))
}

// goMarkedRead starts one marked reader whose recovery deadline arms now (its
// arrival). The buffered channel receives the outcome.
func goMarkedRead(ctx context.Context, tbl *readTable, key string, fetch fetchFn) <-chan readOutcome {
	return goMarkedReadCtx(WithRecovery(ctx), tbl, key, fetch)
}

// goMarkedReadCtx starts one marked reader on an already-armed recovery
// context (shared-deadline fixtures).
func goMarkedReadCtx(rctx context.Context, tbl *readTable, key string, fetch fetchFn) <-chan readOutcome {
	ch := make(chan readOutcome, 1)
	go func() {
		rec, _ := recoveryFrom(rctx)
		v, err := tbl.waveRead(rctx, rec, key, fetch)
		ch <- readOutcome{val: v, err: err, answeredAt: time.Now()}
	}()
	return ch
}

func goPlainRead(ctx context.Context, tbl *readTable, key string, fetch fetchFn) <-chan readOutcome {
	ch := make(chan readOutcome, 1)
	go func() {
		v, err := tbl.plainRead(ctx, key, fetch)
		ch <- readOutcome{val: v, err: err, answeredAt: time.Now()}
	}()
	return ch
}

// holdPermits occupies n of the gate's permits with blocked waves on
// throwaway keys. Closing a returned channel releases that wave's pass.
func holdPermits(t *testing.T, ctx context.Context, tbl *readTable, n int) []chan struct{} {
	t.Helper()
	releases := make([]chan struct{}, n)
	for i := range n {
		stub := callIndexFetch()
		releases[i] = stub.gate(0)
		goMarkedRead(ctx, tbl, "hold/"+string(rune('a'+i)), stub.fn)
	}
	synctest.Wait()
	return releases
}

func mustValue(t *testing.T, out readOutcome) any {
	t.Helper()
	if out.err != nil {
		t.Fatalf("read failed: %v", out.err)
	}
	return out.val
}

func assertPending(t *testing.T, ch <-chan readOutcome, msg string) {
	t.Helper()
	synctest.Wait()
	select {
	case out := <-ch:
		t.Fatalf("%s: answered early with (%v, %v)", msg, out.val, out.err)
	default:
	}
}

func TestPlainRead_coalescesConcurrentReaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := callIndexFetch()
		release := stub.gate(0)

		leader := goPlainRead(t.Context(), tbl, "k", stub.fn)
		synctest.Wait()
		joiners := make([]<-chan readOutcome, 4)
		for i := range joiners {
			joiners[i] = goPlainRead(t.Context(), tbl, "k", stub.fn)
		}
		synctest.Wait()
		close(release)

		if got := mustValue(t, <-leader); got != 0 {
			t.Errorf("leader value = %v, want 0", got)
		}
		for i, ch := range joiners {
			if got := mustValue(t, <-ch); got != 0 {
				t.Errorf("joiner %d value = %v, want 0", i, got)
			}
		}
		if stub.calls() != 1 {
			t.Errorf("upstream calls = %d, want 1 (the wrapper's singleflight)", stub.calls())
		}
	})
}

func TestPlainRead_ttlExpiryRefetches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := callIndexFetch()

		mustValue(t, <-goPlainRead(t.Context(), tbl, "k", stub.fn))
		if got := mustValue(t, <-goPlainRead(t.Context(), tbl, "k", stub.fn)); got != 0 {
			t.Errorf("within TTL: value = %v, want the cached 0", got)
		}
		if stub.calls() != 1 {
			t.Fatalf("within TTL: upstream calls = %d, want 1", stub.calls())
		}

		time.Sleep(arrCacheTTL + time.Millisecond)
		if got := mustValue(t, <-goPlainRead(t.Context(), tbl, "k", stub.fn)); got != 1 {
			t.Errorf("past TTL: value = %v, want the refetched 1", got)
		}
		if stub.calls() != 2 {
			t.Errorf("past TTL: upstream calls = %d, want 2", stub.calls())
		}
	})
}

func TestPlainRead_errorsNotCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		errBoom := errors.New("boom")
		stub := newStubFetch(func(n int) (any, error) {
			if n == 0 {
				return nil, errBoom
			}
			return "fresh", nil
		})

		if out := <-goPlainRead(t.Context(), tbl, "k", stub.fn); !errors.Is(out.err, errBoom) {
			t.Fatalf("first read error = %v, want %v", out.err, errBoom)
		}
		if _, ok := tbl.cache.Get("k"); ok {
			t.Fatal("an error was cached")
		}
		if got := mustValue(t, <-goPlainRead(t.Context(), tbl, "k", stub.fn)); got != "fresh" {
			t.Errorf("second read value = %v, want fresh", got)
		}
		if stub.calls() != 2 {
			t.Errorf("upstream calls = %d, want 2 (error not served from cache)", stub.calls())
		}
	})
}

func TestPlainRead_leaderContextDeathStillServesJoiner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := callIndexFetch()
		release := stub.gate(0)

		leaderCtx, cancelLeader := context.WithCancel(t.Context())
		leader := goPlainRead(leaderCtx, tbl, "k", stub.fn)
		synctest.Wait()
		joiner := goPlainRead(t.Context(), tbl, "k", stub.fn)
		synctest.Wait()

		cancelLeader()
		if out := <-leader; !errors.Is(out.err, context.Canceled) {
			t.Fatalf("leader error = %v, want context.Canceled", out.err)
		}
		assertPending(t, joiner, "joiner")

		close(release)
		if got := mustValue(t, <-joiner); got != 0 {
			t.Errorf("joiner value = %v, want 0 (the detached fetch's result)", got)
		}
		if stub.calls() != 1 {
			t.Errorf("upstream calls = %d, want 1", stub.calls())
		}
	})
}

func TestWaveRead_neverServedPreArrivalData(t *testing.T) {
	t.Run("completed pre-arrival wave, fresh cache", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tbl := newTestTable(t.Context())
			stub := callIndexFetch()
			start := time.Now()

			mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn)) // W0 begins and lands at T

			time.Sleep(100 * time.Millisecond)
			out := <-goMarkedRead(t.Context(), tbl, "k", stub.fn)
			if got := mustValue(t, out); got != 1 {
				t.Errorf("value = %v, want 1 (a fresh pass, never W0's)", got)
			}
			if wantAt := start.Add(waveFloor); !stub.begin(1).Equal(wantAt) {
				t.Errorf("follow-up read-begin = %v, want %v (W0 start + floor)", stub.begin(1), wantAt)
			}
		})
	})
	t.Run("in-flight pre-arrival wave lands after the follow-up", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tbl := newTestTable(t.Context())
			stub := callIndexFetch()
			releaseW0 := stub.gate(0)

			w0 := goMarkedRead(t.Context(), tbl, "k", stub.fn) // read begins at T, lands late
			synctest.Wait()

			time.Sleep(time.Second)
			follower := goMarkedRead(t.Context(), tbl, "k", stub.fn) // arrives T+1, read begun → arm 3
			if got := mustValue(t, <-follower); got != 1 {
				t.Errorf("follower value = %v, want 1 (the post-arrival pass)", got)
			}

			time.Sleep(2 * time.Second) // T+3: W0 finally lands
			close(releaseW0)
			mustValue(t, <-w0)
			e, ok := tbl.cache.Get("k")
			if !ok || e.payload != 1 {
				t.Errorf("cache payload = %v (ok=%v), want the follow-up's 1: the slow W0 write must not clobber it", e.payload, ok)
			}
		})
	})
}

func TestReads_markedAndPlainNeverShareAFlight(t *testing.T) {
	t.Run("marked never joins a plain flight", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tbl := newTestTable(t.Context())
			stub := callIndexFetch()
			release := stub.gate(0)

			plain := goPlainRead(t.Context(), tbl, "k", stub.fn)
			synctest.Wait()
			marked := goMarkedRead(t.Context(), tbl, "k", stub.fn)
			if got := mustValue(t, <-marked); got != 1 {
				t.Errorf("marked value = %v, want 1 (its own wave pass)", got)
			}
			close(release)
			mustValue(t, <-plain)
			if stub.calls() != 2 {
				t.Errorf("upstream calls = %d, want 2", stub.calls())
			}
		})
	})
	t.Run("plain never joins a wave", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tbl := newTestTable(t.Context())
			releases := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
			stub := callIndexFetch()

			marked := goMarkedRead(t.Context(), tbl, "k", stub.fn) // queued for a permit, pre-read
			synctest.Wait()
			plain := goPlainRead(t.Context(), tbl, "k", stub.fn)
			if got := mustValue(t, <-plain); got != 0 {
				t.Errorf("plain value = %v, want 0 (its own flight, not the wave)", got)
			}

			for _, r := range releases {
				close(r)
			}
			mustValue(t, <-marked)
			if stub.calls() != 2 {
				t.Errorf("upstream calls = %d, want 2", stub.calls())
			}
		})
	})
}

func TestWaveRead_heldPermitJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		releases := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
		stub := callIndexFetch()

		a := goMarkedRead(t.Context(), tbl, "k", stub.fn) // creates the wave, permit withheld
		synctest.Wait()
		b := goMarkedRead(t.Context(), tbl, "k", stub.fn) // joins pre-start
		synctest.Wait()

		close(releases[0])
		va, vb := mustValue(t, <-a), mustValue(t, <-b)
		if va != vb {
			t.Errorf("A got %v, B got %v; one pass must serve both", va, vb)
		}
		if stub.calls() != 1 {
			t.Errorf("upstream calls = %d, want 1", stub.calls())
		}
		close(releases[1])
	})
}

// TestWaveRead_preStartPartialDetachKeepsTheWaveForCoWaiters pins the
// pre-start half of the CONTEXT clause: a waiter's cancellation removes it
// from the wave and never discards a wave that still has co-waiters. Under
// held permits, A creates the wave and B joins pre-start; A cancels and gets
// its own cancellation, B stays pending, and the released permit serves B
// from the ONE surviving pass.
func TestWaveRead_preStartPartialDetachKeepsTheWaveForCoWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		releases := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
		stub := callIndexFetch()

		ctxA, cancelA := context.WithCancel(t.Context())
		a := goMarkedRead(ctxA, tbl, "k", stub.fn) // creates the wave, permit withheld
		synctest.Wait()
		b := goMarkedRead(t.Context(), tbl, "k", stub.fn) // joins pre-start
		synctest.Wait()

		cancelA()
		outA := <-a
		if !errors.Is(outA.err, context.Canceled) {
			t.Errorf("A's outcome = (%v, %v), want its own cancellation", outA.val, outA.err)
		}
		assertPending(t, b, "B after A's pre-start cancel")

		close(releases[0])
		if got := mustValue(t, <-b); got != 0 {
			t.Errorf("B value = %v, want 0 from the surviving wave", got)
		}
		if stub.calls() != 1 {
			t.Errorf("upstream calls = %d, want exactly 1 (the wave survived A's detach)", stub.calls())
		}
		close(releases[1])
	})
}

func TestWaveRead_secondTabAfterReadBeginCostsSecondPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := callIndexFetch()
		releaseW0 := stub.gate(0)
		start := time.Now()

		tab1 := goMarkedRead(t.Context(), tbl, "k", stub.fn) // read begins at T
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)
		tab2 := goMarkedRead(t.Context(), tbl, "k", stub.fn) // 0.1 s later, read already begun

		close(releaseW0)
		if got := mustValue(t, <-tab1); got != 0 {
			t.Errorf("tab1 value = %v, want 0", got)
		}
		if got := mustValue(t, <-tab2); got != 1 {
			t.Errorf("tab2 value = %v, want 1 (its own follow-up pass)", got)
		}
		if stub.calls() != 2 {
			t.Errorf("upstream calls = %d, want the stated 2 passes", stub.calls())
		}
		if wantAt := start.Add(waveFloor); !stub.begin(1).Equal(wantAt) {
			t.Errorf("follow-up read-begin = %v, want %v", stub.begin(1), wantAt)
		}
	})
}

func TestWaveRead_primedFollowUpBurstSharesOnePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := newStubFetch(func(n int) (any, error) {
			if n == 0 {
				return "v0", nil
			}
			return "v1", nil // the upstream mutation after W0
		})
		start := time.Now()

		mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn)) // W0 completes at T

		time.Sleep(500 * time.Millisecond)
		b := goMarkedRead(t.Context(), tbl, "k", stub.fn)
		time.Sleep(500 * time.Millisecond)
		c := goMarkedRead(t.Context(), tbl, "k", stub.fn)

		if got := mustValue(t, <-b); got != "v1" {
			t.Errorf("B value = %v, want the mutated v1", got)
		}
		if got := mustValue(t, <-c); got != "v1" {
			t.Errorf("C value = %v, want the mutated v1", got)
		}
		if stub.calls() != 2 {
			t.Errorf("upstream calls = %d, want 2 (W0 + ONE shared follow-up)", stub.calls())
		}
		if wantAt := start.Add(waveFloor); !stub.begin(1).Equal(wantAt) {
			t.Errorf("follow-up read-begin = %v, want %v", stub.begin(1), wantAt)
		}
	})
}

func TestWaveRead_arrivalInsideFloorIsAnsweredAfterTheFollowUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := callIndexFetch()
		start := time.Now()

		mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn)) // W0 at T

		time.Sleep(1900 * time.Millisecond)
		reader := goMarkedRead(t.Context(), tbl, "k", stub.fn) // 1.9 s into the floor
		time.Sleep(50 * time.Millisecond)                      // T+1.95: before the follow-up
		assertPending(t, reader, "reader 1.9s into the floor")

		out := <-reader
		mustValue(t, out)
		if wantAt := start.Add(waveFloor); !out.answeredAt.Equal(wantAt) {
			t.Errorf("answered at %v, want %v (after the follow-up, not before)", out.answeredAt, wantAt)
		}
	})
}

func TestWaveRead_floorBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		arrival     time.Duration // after W0's read-begin
		wantBeginAt time.Duration // second pass read-begin, after W0's
	}{
		{"floor minus 1ms joins the follow-up", waveFloor - time.Millisecond, waveFloor},
		{"floor plus 1ms starts a wave now", waveFloor + time.Millisecond, waveFloor + time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tbl := newTestTable(t.Context())
				stub := callIndexFetch()
				start := time.Now()

				mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn)) // W0 at T

				time.Sleep(tc.arrival)
				mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn))
				if want := start.Add(tc.wantBeginAt); !stub.begin(1).Equal(want) {
					t.Errorf("second read-begin = %v, want %v", stub.begin(1), want)
				}
			})
		})
	}
}

func TestWaveRead_overlappingArrivalsCostAtMostTwoReadBegins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		// The value of each pass is its own read-begin instant, so every
		// reader's freshness is checkable against its arrival.
		stub := newStubFetch(func(int) (any, error) { return time.Now(), nil })

		arrivalA := time.Now()
		a := goMarkedRead(t.Context(), tbl, "k", stub.fn)
		synctest.Wait()
		time.Sleep(500 * time.Millisecond)
		arrivalB := time.Now()
		b := goMarkedRead(t.Context(), tbl, "k", stub.fn)
		synctest.Wait()
		time.Sleep(500 * time.Millisecond)
		arrivalC := time.Now()
		c := goMarkedRead(t.Context(), tbl, "k", stub.fn)

		for _, r := range []struct {
			ch      <-chan readOutcome
			arrival time.Time
			name    string
		}{{a, arrivalA, "A"}, {b, arrivalB, "B"}, {c, arrivalC, "C"}} {
			served := mustValue(t, <-r.ch).(time.Time)
			if served.Before(r.arrival) {
				t.Errorf("%s served a value fetched at %v, before its arrival %v", r.name, served, r.arrival)
			}
		}
		if stub.calls() > 2 {
			t.Errorf("read-begins = %d, want <= 2 (initial wave + one shared follow-up)", stub.calls())
		}
	})
}

func TestWaveRead_floorMeasuresReadBeginNotSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		releases := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
		stub := callIndexFetch()
		start := time.Now()

		r1 := goMarkedRead(t.Context(), tbl, "k", stub.fn) // scheduled at T, queued
		synctest.Wait()

		time.Sleep(3 * time.Second) // admission delayed to T+3
		close(releases[0])
		mustValue(t, <-r1)
		if want := start.Add(3 * time.Second); !stub.begin(0).Equal(want) {
			t.Fatalf("delayed read-begin = %v, want %v", stub.begin(0), want)
		}

		time.Sleep(time.Second) // T+4: one second after the actual read-begin
		mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn))
		// The floor measures the T+3 read-begin, not the T schedule: the next
		// pass may begin at T+5, never at T+4.
		if want := start.Add(5 * time.Second); !stub.begin(1).Equal(want) {
			t.Errorf("second read-begin = %v, want %v", stub.begin(1), want)
		}
		close(releases[1])
	})
}

func TestCommit_slowPlainFetchNeverClobbersAWave(t *testing.T) {
	t.Run("plain lands last", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tbl := newTestTable(t.Context())
			plainStub := newStubFetch(func(int) (any, error) { return "plain", nil })
			releasePlain := plainStub.gate(0)
			waveStub := newStubFetch(func(int) (any, error) { return "wave", nil })

			plain := goPlainRead(t.Context(), tbl, "k", plainStub.fn) // read begins at T
			synctest.Wait()
			time.Sleep(500 * time.Millisecond)
			mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", waveStub.fn)) // begins T+0.5, writes

			time.Sleep(500 * time.Millisecond)
			close(releasePlain) // plain lands last, with the older read-begin
			mustValue(t, <-plain)

			e, ok := tbl.cache.Get("k")
			if !ok || e.payload != "wave" {
				t.Errorf("cache payload = %v (ok=%v), want the wave's snapshot to survive", e.payload, ok)
			}
		})
	})
	t.Run("plain lands first", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tbl := newTestTable(t.Context())
			plainStub := newStubFetch(func(int) (any, error) { return "plain", nil })
			waveStub := newStubFetch(func(int) (any, error) { return "wave", nil })

			mustValue(t, <-goPlainRead(t.Context(), tbl, "k", plainStub.fn)) // lands at T
			time.Sleep(500 * time.Millisecond)
			mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", waveStub.fn)) // newer read-begin wins

			e, ok := tbl.cache.Get("k")
			if !ok || e.payload != "wave" {
				t.Errorf("cache payload = %v (ok=%v), want the wave's snapshot", e.payload, ok)
			}
		})
	})
}

func TestWriteThrough_winsAsNewestWriteAndResetsFloor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := newStubFetch(func(int) (any, error) { return "wave", nil })
		start := time.Now()

		mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn)) // W0 at T

		time.Sleep(500 * time.Millisecond)
		tbl.writeThrough("k", readEntry{payload: "scan", readBegin: time.Now()}) // T+0.5

		e, ok := tbl.cache.Get("k")
		if !ok || e.payload != "scan" {
			t.Fatalf("cache payload = %v (ok=%v), want the scan write-through to win", e.payload, ok)
		}

		time.Sleep(500 * time.Millisecond) // T+1
		mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn))
		// The write-through reset the floor clock to T+0.5, so the next pass
		// begins at T+2.5, not T+2.
		if want := start.Add(500*time.Millisecond + waveFloor); !stub.begin(1).Equal(want) {
			t.Errorf("post-write-through read-begin = %v, want %v", stub.begin(1), want)
		}

		// A write-through with an older read-begin than the entry's is refused
		// (write ordering binds EVERY writer).
		tbl.writeThrough("k", readEntry{payload: "stale-scan", readBegin: start})
		if e, _ := tbl.cache.Get("k"); e.payload == "stale-scan" {
			t.Error("a stale write-through clobbered a newer entry")
		}
	})
}

func TestWaveRead_shutdownCancelsAdmittedPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifetime, shutdown := context.WithCancel(t.Context())
		tbl := newReadTable(NewReadGate(func() context.Context { return lifetime }, nil))
		stub := callIndexFetch()
		stub.gate(0) // never released: the pass ends only via its context

		reader := goMarkedRead(t.Context(), tbl, "k", stub.fn)
		synctest.Wait()

		shutdown()
		out := <-reader
		if !errors.Is(out.err, ErrRecoveryFailed) {
			t.Errorf("error = %v, want ErrRecoveryFailed", out.err)
		}
		if !errors.Is(out.err, context.Canceled) {
			t.Errorf("error = %v, want the shutdown cancellation in the chain", out.err)
		}
	})
}

func TestWaveRead_lastWaiterCancelAfterReadBeginDoesNotCancelThePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := newStubFetch(func(int) (any, error) { return "landed", nil })
		release := stub.gate(0)

		readerCtx, cancelReader := context.WithCancel(t.Context())
		reader := goMarkedReadCtx(WithRecovery(readerCtx), tbl, "k", stub.fn)
		synctest.Wait() // read has begun; the fetch is blocked

		cancelReader()
		if out := <-reader; !errors.Is(out.err, context.Canceled) {
			t.Fatalf("reader error = %v, want its own cancellation", out.err)
		}

		close(release)
		synctest.Wait()
		e, ok := tbl.cache.Get("k")
		if !ok || e.payload != "landed" {
			t.Errorf("cache payload = %v (ok=%v), want the pass to complete and land", e.payload, ok)
		}
	})
}

func TestWave_zeroWaitersBeforeReadBeginDiscardsUnexecuted(t *testing.T) {
	t.Run("scheduled follow-up: timer cancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tbl := newTestTable(t.Context())
			stub := callIndexFetch()

			mustValue(t, <-goMarkedRead(t.Context(), tbl, "k", stub.fn)) // W0 primes the floor

			readerCtx, cancelReader := context.WithCancel(t.Context())
			time.Sleep(500 * time.Millisecond)
			reader := goMarkedReadCtx(WithRecovery(readerCtx), tbl, "k", stub.fn) // follow-up at T+2
			synctest.Wait()

			cancelReader()
			<-reader
			time.Sleep(3 * time.Second) // well past the follow-up's schedule
			if stub.calls() != 1 {
				t.Errorf("upstream calls = %d, want 1: the waiterless follow-up must be discarded", stub.calls())
			}
		})
	})
	t.Run("queued acquire cancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			tbl := newTestTable(t.Context())
			releases := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
			stub := callIndexFetch()

			readerCtx, cancelReader := context.WithCancel(t.Context())
			reader := goMarkedReadCtx(WithRecovery(readerCtx), tbl, "k", stub.fn)
			synctest.Wait()

			cancelReader()
			<-reader
			for _, r := range releases {
				close(r)
			}
			synctest.Wait()
			if stub.calls() != 0 {
				t.Errorf("upstream calls = %d, want 0: the discarded wave must never execute", stub.calls())
			}
		})
	})
	t.Run("discard around the permit grant advances the FIFO", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// The discard signal and the permit grant race; whichever arm the
			// runner takes, the permit must not leak and the next queued wave
			// must be served. Iterate to exercise both select outcomes.
			for range 20 {
				lifetime, done := context.WithCancel(t.Context())
				tbl := newReadTable(NewReadGate(func() context.Context { return lifetime }, nil))
				releases := holdPermits(t, lifetime, tbl, maxConcurrentWaves)
				discarded := callIndexFetch()
				next := callIndexFetch()

				readerCtx, cancelReader := context.WithCancel(lifetime)
				reader := goMarkedReadCtx(WithRecovery(readerCtx), tbl, "k", discarded.fn)
				synctest.Wait()
				follower := goMarkedRead(lifetime, tbl, "m", next.fn)
				synctest.Wait()

				cancelReader()
				<-reader           // the waiter has detached; the wave is discarded
				close(releases[0]) // races the grant against the closed discard signal

				if out := <-follower; out.err != nil {
					t.Fatalf("follower error = %v; the FIFO did not advance past the discard", out.err)
				}
				if discarded.calls() != 0 {
					t.Errorf("discarded wave's upstream calls = %d, want 0", discarded.calls())
				}
				close(releases[1])
				done()
				synctest.Wait()
			}
		})
	})
}

func TestWaveRead_distinctKeysBoundedByCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		const keys = 4
		stubs := make([]*stubFetch, keys)
		releases := make([]chan struct{}, keys)
		results := make([]<-chan readOutcome, keys)
		for i := range keys {
			stubs[i] = callIndexFetch()
			releases[i] = stubs[i].gate(0)
			results[i] = goMarkedRead(t.Context(), tbl, "k/"+string(rune('0'+i)), stubs[i].fn)
		}
		synctest.Wait()

		if got := started(stubs); got != maxConcurrentWaves {
			t.Fatalf("concurrent read-begins = %d, want the ceiling %d", got, maxConcurrentWaves)
		}
		for _, r := range releases {
			close(r)
		}
		for i, ch := range results {
			if out := <-ch; out.err != nil {
				t.Errorf("reader %d error = %v", i, out.err)
			}
		}
	})
}

func started(stubs []*stubFetch) int {
	n := 0
	for _, s := range stubs {
		n += s.calls()
	}
	return n
}

func TestWaveRead_admissionBudgetRefusesTyped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		releases := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
		stub := callIndexFetch()
		scheduled := time.Now()

		reader := goMarkedRead(t.Context(), tbl, "k", stub.fn)
		out := <-reader
		if !errors.Is(out.err, ErrRecoveryRefused) {
			t.Fatalf("error = %v, want ErrRecoveryRefused", out.err)
		}
		if errors.Is(out.err, ErrRecoveryFailed) {
			t.Error("refusal must not read as an execution failure")
		}
		if want := scheduled.Add(admissionBudget); !out.answeredAt.Equal(want) {
			t.Errorf("refused at %v, want %v (admission budget past the scheduled instant)", out.answeredAt, want)
		}
		if stub.calls() != 0 {
			t.Errorf("upstream calls = %d, want 0", stub.calls())
		}
		for _, r := range releases {
			close(r)
		}
	})
}

func TestWaveRead_admittedPassSettlesWithinExecutionBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		stub := callIndexFetch()
		stub.gate(0) // never released: only the execution budget ends the pass
		start := time.Now()

		out := <-goMarkedRead(t.Context(), tbl, "k", stub.fn)
		if !errors.Is(out.err, ErrRecoveryFailed) {
			t.Fatalf("error = %v, want ErrRecoveryFailed", out.err)
		}
		if !errors.Is(out.err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want the execution deadline in the chain", out.err)
		}
		if want := start.Add(executionBudget); !out.answeredAt.Equal(want) {
			t.Errorf("settled at %v, want %v", out.answeredAt, want)
		}
	})
}

func TestWaveRead_oneDeadlinePerMarkedRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		rctx := WithRecovery(t.Context()) // the request arrives: deadline arms at T
		arrival := time.Now()

		// First wrapper read of the request: settles fast.
		listStub := callIndexFetch()
		mustValue(t, <-goMarkedReadCtx(rctx, tbl, "list", listStub.fn))

		// Occupy both permits so the second read's admission is delayed
		// almost to its budget, and never release its pass: the wave alone
		// would settle at read-begin + 20 s, past the request's 25 s.
		releases := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
		time.Sleep(time.Second)
		tagStub := callIndexFetch()
		tagStub.gate(0)
		second := goMarkedReadCtx(rctx, tbl, "tags", tagStub.fn) // scheduled T+1
		synctest.Wait()

		time.Sleep(5900 * time.Millisecond) // T+6.9: admit within the T+7 budget
		close(releases[0])
		synctest.Wait()
		if tagStub.calls() != 1 {
			t.Fatalf("second read's pass did not begin after admission")
		}

		out := <-second
		if !errors.Is(out.err, ErrRecoveryRefused) {
			t.Fatalf("error = %v, want ErrRecoveryRefused", out.err)
		}
		// ONE deadline, armed at request arrival: refusal lands at T+25, not
		// 25 s after this read began.
		if want := arrival.Add(requestDeadline); !out.answeredAt.Equal(want) {
			t.Errorf("refused at %v, want %v (one deadline per marked request)", out.answeredAt, want)
		}
		close(releases[1])
		time.Sleep(executionBudget) // let the abandoned pass expire before the bubble ends
	})
}

func TestWaveRead_expiredDeadlineRefusesBeforeAdmitting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		rctx := WithRecovery(t.Context())
		time.Sleep(requestDeadline + time.Millisecond)

		stub := callIndexFetch()
		out := <-goMarkedReadCtx(rctx, tbl, "k", stub.fn)
		if !errors.Is(out.err, ErrRecoveryRefused) {
			t.Fatalf("error = %v, want ErrRecoveryRefused", out.err)
		}
		if stub.calls() != 0 {
			t.Errorf("upstream calls = %d, want 0", stub.calls())
		}
	})
}

func TestWaveRead_fanOutFailureAndRetriesShareOneReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tbl := newTestTable(t.Context())
		releases := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
		stub := callIndexFetch()
		stub.gate(0) // the first pass only ever ends via its execution budget

		const waiters = 3
		firstRound := make([]<-chan readOutcome, waiters)
		for i := range waiters {
			firstRound[i] = goMarkedRead(t.Context(), tbl, "k", stub.fn)
		}
		synctest.Wait()
		close(releases[0]) // admit the shared wave; its fetch stalls

		for i, ch := range firstRound {
			out := <-ch
			if !errors.Is(out.err, ErrRecoveryFailed) {
				t.Errorf("waiter %d error = %v, want ErrRecoveryFailed fanned out", i, out.err)
			}
		}
		if stub.calls() != 1 {
			t.Fatalf("read-begins = %d, want exactly 1 for the fan-out", stub.calls())
		}

		// Each retry's read joins whatever wave is then live for the key. The
		// fan-out's own retries arrive together, pre-read-begin (permits held
		// again), so they share ONE replacement pass. Both original blockers'
		// passes expired with the fan-out (same execution budget), so the
		// permits are free again here.
		retryBlocks := holdPermits(t, t.Context(), tbl, maxConcurrentWaves)
		retries := make([]<-chan readOutcome, waiters)
		for i := range waiters {
			retries[i] = goMarkedRead(t.Context(), tbl, "k", stub.fn)
		}
		synctest.Wait()
		close(retryBlocks[0])
		var vals [waiters]any
		for i, ch := range retries {
			vals[i] = mustValue(t, <-ch)
		}
		if vals[0] != vals[1] || vals[1] != vals[2] {
			t.Errorf("retry values %v differ; they must share one replacement wave", vals)
		}
		if stub.calls() != 2 {
			t.Errorf("read-begins = %d, want 2 (the failed pass + one replacement)", stub.calls())
		}
		close(retryBlocks[1])
	})
}

func TestReadTable_reloadOrphansOldWaveWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := NewReadGate(func() context.Context { return t.Context() }, nil)
		oldTable := newReadTable(gate)
		newTable := newReadTable(gate) // the reload publishes a fresh instance

		oldStub := newStubFetch(func(int) (any, error) { return "old", nil })
		releaseOld := oldStub.gate(0)
		oldRead := goMarkedRead(t.Context(), oldTable, keySeriesList, oldStub.fn)
		synctest.Wait() // the old wave is mid-flight when the swap happens

		newStub := newStubFetch(func(int) (any, error) { return "new", nil })
		mustValue(t, <-goPlainRead(t.Context(), newTable, keySeriesList, newStub.fn))

		close(releaseOld)
		mustValue(t, <-oldRead)

		e, ok := newTable.cache.Get(keySeriesList)
		if !ok || e.payload != "new" {
			t.Errorf("post-reload cache payload = %v (ok=%v), want new: the old wave's write must not land", e.payload, ok)
		}
	})
}
