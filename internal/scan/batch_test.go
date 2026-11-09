package scan_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// tens ticks every 10 s and passes daily.
var tens = mediasource.Schedule{Change: 10 * time.Second, Whole: 24 * time.Hour}

// booted is a rig whose boot whole pass has completed over movies 1 to n,
// its Scan work drained and its calls forgotten.
func booted(t *testing.T, caps mediasource.Capabilities, sched mediasource.Schedule, n int, opts ...option) *rig {
	t.Helper()
	r := newRig(t, caps, sched, opts...)
	r.put(1, n)
	r.start(t)
	advance(30 * time.Second)
	r.scanWork(t)
	r.db.tookFinishes()
	r.ad.took("Items")
	r.ad.took("Root")
	r.ad.took("Changes")
	r.ad.took("RootFolders")
	return r
}

// reads answers the times ref was read with Root among calls.
func reads(calls []call, ref mediasource.NativeRef) []time.Time {
	var out []time.Time
	for _, c := range calls {
		if c.root == ref {
			out = append(out, c.at)
		}
	}
	return out
}

func since(t0 time.Time, ts []time.Time) []time.Duration {
	out := make([]time.Duration, len(ts))
	for i, at := range ts {
		out[i] = at.Sub(t0)
	}
	return out
}

func TestBackoffHoldsWholePassesOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, tens, 3)
		r.ad.Fail("Items", unreachable("down"))
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		r.ad.FailRoot(movieRef(2), unreachable("episode read failed"))
		r.lib.Put(movieRoot(2))
		advance(tens.Change)
		r.ad.FailRoot(movieRef(2), nil)
		advance(50 * time.Second)
		if n := len(r.ad.took("Items")); n != 2 {
			t.Fatalf("%d walks ran, want the failed request and its retry a minute later", n)
		}
		r.ad.took("Root")

		advance(tens.Change)
		if got := reads(r.ad.took("Root"), movieRef(2)); len(got) != 1 {
			t.Errorf("the due retry root was read %d times in the backoff, want 1", len(got))
		}
		one := movieRef(1)
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Root: &one}); err != nil {
			t.Fatal(err)
		}
		advance(tens.Change)
		if got := reads(r.ad.took("Root"), one); len(got) != 1 {
			t.Errorf("the requested root was read %d times in the backoff, want 1", len(got))
		}
		r.put(9, 9)
		advance(tens.Change)
		if got := reads(r.ad.took("Root"), movieRef(9)); len(got) != 1 {
			t.Errorf("the changed root was read %d times in the backoff, want 1", len(got))
		}
		if n := len(r.ad.took("Items")); n != 0 {
			t.Errorf("%d walks ran inside the 2-minute backoff", n)
		}
	})
}

func TestOutageFreshSourceStillImports(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 2)
		r.ad.Fail("Items", unreachable("down"))
		boot := time.Now()
		r.start(t)
		advance(30 * time.Second)
		r.put(5, 5)
		advance(daily.Change)
		if got := r.listed(t); !slices.Equal(got, []string{"Movie 5"}) {
			t.Errorf("listed %v during the outage, want the batch's movie 5", got)
		}
		advance(30 * time.Second)
		advance(2 * time.Minute)
		if got := gaps(boot, r.ad.took("Items")); !slices.Equal(got, []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute}) {
			t.Errorf("walks came at gaps %v, want the whole-pass backoff", got)
		}
	})
}

func TestPoisonRootLastInBatchDoesNotReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 2)
		x, y := movieRef(1), movieRef(2)
		r.ad.FailRoot(x, unreachable("poison"))
		r.lib.Put(movieRoot(1))
		advance(daily.Change)
		first := time.Now()
		advance(daily.Change)
		r.lib.Put(movieRoot(2))
		r.lib.Put(movieRoot(1))
		for range 3 {
			advance(daily.Change)
		}
		advance(5 * time.Minute)
		calls := r.ad.took("Root")
		if got := since(first, reads(calls, x)); !slices.Equal(got, []time.Duration{0, time.Minute, 3 * time.Minute, 7 * time.Minute}) {
			t.Errorf("X was read at %v after its first failure, want only at its retry times 0, 1m, 3m, 7m", got)
		}
		if got := reads(calls, y); len(got) != 1 {
			t.Errorf("Y was read %d times over the ticks, want once", len(got))
		}
	})
}

func TestDueRetryRootLastDoesNotReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 2)
		x, y := movieRef(1), movieRef(2)
		r.ad.FailRoot(x, unreachable("poison"))
		r.lib.Put(movieRoot(1))
		advance(daily.Change)
		r.ad.took("Root")
		advance(daily.Change)
		r.lib.Put(movieRoot(2))
		advance(daily.Change)
		calls := r.ad.took("Root")
		if len(calls) != 2 || calls[0].root != y || calls[1].root != x {
			t.Fatalf("the batch read %+v, want Y then the due retry root X", calls)
		}
		for range 3 {
			advance(daily.Change)
		}
		if got := reads(r.ad.took("Root"), y); len(got) != 0 {
			t.Errorf("Y was replayed %d times after its batch", len(got))
		}
	})
}

func TestStoredCursorBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 2)
		read := r.stored(t)
		r.put(3, 3)
		advance(daily.Change)
		r.put(4, 4)
		r.ad.FailRoot(movieRef(4), unreachable("poison"))
		advance(daily.Change)
		if got := r.stored(t); !sameBytes(got, read) {
			t.Fatalf("stored cursor %x, want %x, the start of the batch that completed movie 3", got, read)
		}
		r.ad.took("Root")
		r.lib.Put(movieRoot(4))
		advance(daily.Change)
		if roots := r.ad.took("Root"); len(roots) != 0 {
			t.Fatalf("the waiting root was read %+v before its retry", roots)
		}
		if got := r.stored(t); !sameBytes(got, read) {
			t.Errorf("a batch whose every root waits stored %x, want %x kept", got, read)
		}
		r.ad.FailRoot(movieRef(4), nil)
		r.restart(t)
		calls := r.ad.took("Root")
		if got := reads(calls, movieRef(4)); len(got) != 1 {
			t.Errorf("after the restart movie 4 was read %d times, want once from the stored cursor", len(got))
		}
	})
}

// failJoin answers ErrAiredOrderUnavailable for the series in fail, and for
// those in slow once its ctx ends, as seriesorder answers a passed deadline;
// every other series at its own places.
type failJoin struct {
	fail map[mediasource.NativeID]bool
	slow map[mediasource.NativeID]bool
	// reason is the join's reason, "2 episodes are listed otherwise" when
	// empty.
	reason string
	mu     sync.Mutex
}

func (j *failJoin) Join(ctx context.Context, q mediasource.JoinQuery) ([]mediasource.Joined, *mediasource.RootEvidence, error) {
	j.mu.Lock()
	failed, slow, reason := j.fail[q.Root.ID], j.slow[q.Root.ID], cmp.Or(j.reason, "2 episodes are listed otherwise")
	j.mu.Unlock()
	if slow {
		<-ctx.Done()
		return nil, nil, &joinError{reason: "SkyHook (skyhook.sonarr.tv) did not answer: " + ctx.Err().Error()}
	}
	if failed {
		return nil, nil, &joinError{reason: reason}
	}
	return sourcetest.OwnPlaces{}.Join(ctx, q)
}

// joinError is seriesorder's refusal shape: its text the reason alone.
type joinError struct{ reason string }

func (e *joinError) Error() string { return e.reason }
func (*joinError) Unwrap() error   { return mediasource.ErrAiredOrderUnavailable }

// dvdShow is series n in DVD order with one episode.
func dvdShow(n int) sourcetest.LibRoot {
	ref := mediasource.NativeRef{ID: mediasource.NativeID(fmt.Sprint(n)), Kind: media.Episode}
	ids := media.ExternalIDs{TVDB: uint64(70000 + n)}
	return sourcetest.LibRoot{
		Root: mediasource.Root{Native: ref, Title: fmt.Sprintf("Show %d", n), External: ids, Order: mediasource.Order{Type: "dvd", Name: "DVD Order"}},
		Items: []mediasource.Item{{
			Native: mediasource.NativeID(fmt.Sprint(1000 + n)), External: ids, VideoPath: fmt.Sprintf("/tv/%d/S01E01.mkv", n),
			SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1},
		}},
	}
}

func TestBatchAiredOrderUnavailableNotCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		j := &failJoin{fail: map[mediasource.NativeID]bool{}}
		caps := mediasource.Capabilities{Kinds: []media.Kind{media.Episode}, Changes: true}
		r := newRig(t, caps, daily, joinedBy(j))
		r.start(t)
		advance(30 * time.Second)
		r.ad.took("RootFolders")
		j.mu.Lock()
		for n := 1; n <= 4; n++ {
			j.fail[mediasource.NativeID(fmt.Sprint(n))] = true
		}
		j.mu.Unlock()
		for n := 1; n <= 5; n++ {
			r.lib.Put(dvdShow(n))
		}
		advance(daily.Change)
		if roots := r.ad.took("Root"); len(roots) != 5 {
			t.Errorf("the batch read %d roots, want all five", len(roots))
		}
		if p := r.ad.took("RootFolders"); len(p) != 1 {
			t.Errorf("%d probes in the tick, want only the tick's own", len(p))
		}
		if got := r.listed(t); !slices.Equal(got, []string{"Show 5"}) {
			t.Errorf("listed %v, want the fifth show", got)
		}
		j.mu.Lock()
		clear(j.fail)
		j.mu.Unlock()
		advance(time.Minute)
		if roots := r.ad.took("Root"); len(roots) != 4 {
			t.Errorf("%d roots were retried after a minute, want the four", len(roots))
		}
	})
}

// TestSlowJoinDoesNotHoldBatch: a join SkyHook holds past the root's 60 s
// leaves that root to the retry set, uncounted, and the batch reads the next.
func TestSlowJoinDoesNotHoldBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		j := &failJoin{slow: map[mediasource.NativeID]bool{"1": true}}
		caps := mediasource.Capabilities{Kinds: []media.Kind{media.Episode}, Changes: true}
		r := newRig(t, caps, daily, joinedBy(j))
		r.start(t)
		advance(30 * time.Second)
		r.lib.Put(dvdShow(1))
		r.lib.Put(dvdShow(2))
		advance(daily.Change)
		advance(60 * time.Second)
		if got := r.listed(t); !slices.Equal(got, []string{"Show 2"}) {
			t.Errorf("listed %v a minute into the batch, want the second show read behind the slow one", got)
		}
		j.mu.Lock()
		clear(j.slow)
		j.mu.Unlock()
		r.ad.took("Root")
		advance(time.Minute)
		if roots := r.ad.took("Root"); len(roots) != 1 || roots[0].root.ID != "1" {
			t.Errorf("a minute later the retry read %v, want the slow show alone", roots)
		}
		if got := r.listed(t); !slices.Equal(got, []string{"Show 1", "Show 2"}) {
			t.Errorf("listed %v, want both shows once SkyHook answers", got)
		}
	})
}

func TestLargeBatchRunsNoWholePass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 500
		r := booted(t, movieCaps, daily, 0)
		read := r.head(t)
		r.put(1, n)
		advance(daily.Change)
		f := r.db.tookFinishes()
		if len(f) != n {
			t.Fatalf("%d passes finished, want one per root (%d)", len(f), n)
		}
		for i, fin := range f {
			if !fin.complete || !sameBytes(fin.cursor, read) {
				t.Fatalf("pass %d finished %+v, want complete with the batch's cursor", i, fin)
			}
		}
		if w := r.ad.took("Items"); len(w) != 0 {
			t.Errorf("a large batch ran %d whole passes", len(w))
		}
		if got := r.listed(t); len(got) != n {
			t.Errorf("listed %d roots, want %d", len(got), n)
		}
	})
}

func TestRootRequestWithoutChanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		caps mediasource.Capabilities
	}{
		{"with a cursor", movieCaps},
		{"from a source without Changes", mediasource.Capabilities{Kinds: []media.Kind{media.Movie}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := booted(t, tc.caps, daily, 2)
				one := movieRef(1)
				j, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Root: &one})
				if err != nil {
					t.Fatal(err)
				}
				if j.Kind != status.ItemScan || j.State != status.Queued {
					t.Errorf("Start = %v %v, want a queued ItemScan", j.Kind, j.State)
				}
				advance(daily.Change)
				if got := reads(r.ad.took("Root"), one); len(got) != 1 {
					t.Errorf("the requested root was read %d times, want 1", len(got))
				}
				if got := r.job(t, j.ID); got.State != status.Done || got.Outcome != status.Succeeded {
					t.Errorf("the refetch is %v %v, want Done Succeeded", got.State, got.Outcome)
				}
			})
		})
	}
}

func TestPoisonRootDoesNotWedgeBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		r.ad.FailRoot(movieRef(1), unreachable("poison"))
		r.lib.Put(movieRoot(1))
		for n := 2; n <= 6; n++ {
			advance(daily.Change)
			r.put(n, n)
		}
		advance(daily.Change)
		advance(daily.Change)
		if got := r.listed(t); !slices.Equal(got, []string{"Movie 1", "Movie 2", "Movie 3", "Movie 4", "Movie 5", "Movie 6"}) {
			t.Errorf("listed %v, want every movie the poison root did not hold up", got)
		}
		if got := reads(r.ad.took("Root"), movieRef(1)); len(got) != 3 {
			t.Errorf("the poison root was read %d times in 3 minutes, want 3 (0, 1m, 3m)", len(got))
		}
	})
}

func TestBatchBudgetMovesUnreadRoots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 5)
		for n := 1; n <= 3; n++ {
			r.ad.FailRoot(movieRef(n), unreachable("half down"))
		}
		r.put(1, 5)
		advance(daily.Change)
		calls := r.ad.took("Root")
		if len(calls) != 3 {
			t.Fatalf("the batch read %d roots, want the batch ended at the third failure", len(calls))
		}
		for n := 1; n <= 3; n++ {
			r.ad.FailRoot(movieRef(n), nil)
		}
		advance(daily.Change)
		if roots := r.ad.took("Root"); len(roots) != 0 {
			t.Errorf("the next tick replayed %+v; the cursor should have moved", roots)
		}
		advance(30 * time.Second)
		got := r.ad.took("Root")
		var refs []mediasource.NativeRef
		for _, c := range got {
			refs = append(refs, c.root)
		}
		if want := []mediasource.NativeRef{movieRef(1), movieRef(2), movieRef(3), movieRef(4), movieRef(5)}; !slices.Equal(refs, want) {
			t.Errorf("a minute later the retry read %v, want all five", refs)
		}
	})
}

func TestProbeFailureFailsBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 3)
		read := r.stored(t)
		r.ad.FailRoot(movieRef(2), unreachable("timeout"))
		r.ad.setOnRoot(func(ref mediasource.NativeRef) {
			if ref == movieRef(2) {
				r.ad.Fail("RootFolders", unreachable("down"))
			}
		})
		r.put(1, 3)
		advance(daily.Change)
		if got := r.ad.took("Root"); len(got) != 2 {
			t.Fatalf("the batch read %d roots, want it failed at the second", len(got))
		}
		if got := r.stored(t); !sameBytes(got, read) {
			t.Errorf("stored cursor moved to %x", got)
		}
		r.ad.setOnRoot(nil)
		r.ad.FailRoot(movieRef(2), nil)
		r.ad.Fail("RootFolders", nil)
		advance(daily.Change)
		var refs []mediasource.NativeRef
		for _, c := range r.ad.took("Root") {
			refs = append(refs, c.root)
		}
		if want := []mediasource.NativeRef{movieRef(1), movieRef(2), movieRef(3)}; !slices.Equal(refs, want) {
			t.Errorf("the replay read %v, want the whole batch %v", refs, want)
		}
	})
}

func TestFailedBatchReplaysOneImportRowEach(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 3)
		r.ad.setOnRoot(func(ref mediasource.NativeRef) {
			if ref == movieRef(3) {
				r.ad.FailRoot(ref, unreachable("down"))
				r.ad.Fail("RootFolders", unreachable("down"))
			}
		})
		r.put(1, 3)
		advance(daily.Change)
		r.ad.setOnRoot(nil)
		r.ad.FailRoot(movieRef(3), nil)
		r.ad.Fail("RootFolders", nil)
		advance(daily.Change)
		ws, err := r.db.NextWork(t.Context(), 100)
		if err != nil {
			t.Fatal(err)
		}
		per := map[statedb.InstanceID]int{}
		for _, w := range ws {
			if w.Cause == statedb.Import {
				per[w.Inst]++
			}
		}
		if len(per) != 3 {
			t.Errorf("Import work names %d instances, want the 3 movies", len(per))
		}
		for inst, n := range per {
			if n != 1 {
				t.Errorf("instance %d holds %d Import rows, want 1", inst, n)
			}
		}
	})
}

func TestInactiveSourceEndsPass(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(*testing.T, *rig)
		clear func(*testing.T, *rig)
		// stays is false where the refusal itself drops the source's
		// stored facts (storage's removal).
		stays bool
	}{
		{"a Save removed the source", func(t *testing.T, r *rig) {
			if err := r.db.Activate(t.Context(), statedb.Activation{Languages: r.policy}); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, r *rig) {
			if err := r.db.Activate(t.Context(), statedb.Activation{Sources: []media.SourceID{src}, Languages: r.policy}); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a commit fault", func(_ *testing.T, r *rig) { r.db.fault(nil, errors.New("bolt: commit failed")) }, func(_ *testing.T, r *rig) { r.db.fault(nil, nil) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := booted(t, movieCaps, daily, 2)
				read := r.stored(t)
				tc.fault(t, r)
				r.put(1, 2)
				advance(daily.Change)
				if got := r.stored(t); tc.stays && !sameBytes(got, read) {
					t.Errorf("stored cursor moved to %x", got)
				}
				for _, f := range r.db.tookFinishes() {
					if f.complete {
						t.Errorf("a pass completed under the storage refusal")
					}
				}
				r.ad.took("Root")
				tc.clear(t, r)
				advance(daily.Change)
				if got := r.ad.took("Root"); len(got) != 2 {
					t.Errorf("the replay read %d roots, want both", len(got))
				}
			})
		})
	}
}

func TestNoChangeCheckWritesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 2)
		read := r.stored(t)
		for range 5 {
			advance(daily.Change)
		}
		if ch := r.ad.took("Changes"); len(ch) != 5 {
			t.Errorf("%d change checks ran, want 5", len(ch))
		}
		if f := r.db.tookFinishes(); len(f) != 0 {
			t.Errorf("quiet checks finished %d passes", len(f))
		}
		if got := r.stored(t); !sameBytes(got, read) {
			t.Errorf("stored cursor moved to %x", got)
		}
		if live := r.liveJobs(status.Import); len(live) != 0 {
			t.Errorf("quiet checks hold %d Import jobs", len(live))
		}
		snap, _ := r.jobs.Snapshot()
		for _, j := range snap.Jobs {
			if j.Kind == status.Import {
				t.Errorf("a quiet check recorded Import job %s", j.ID)
			}
		}
	})
}
