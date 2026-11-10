package scan_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/status"
)

// systemJobs answers the live SystemOwner Scan jobs.
func (r *rig) systemJobs() []status.Job {
	var out []status.Job
	for _, j := range r.liveJobs(status.Scan) {
		if j.Owner == status.SystemOwner {
			out = append(out, j)
		}
	}
	return out
}

// oneRunningSystemJob fails the test unless exactly one SystemOwner Scan job
// of src is running, and answers it.
func oneRunningSystemJob(t *testing.T, r *rig) status.Job {
	t.Helper()
	sys := r.systemJobs()
	if len(sys) != 1 || sys[0].State != status.Running || sys[0].Source != src || sys[0].Scope != string(src) {
		t.Fatalf("the walk holds SystemOwner jobs %+v, want one running with Source and Scope %q", sys, src)
	}
	return sys[0]
}

func TestExpiredWholePassCarriesSystemJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		r.ad.Fail("Changes", &mediasource.SourceError{Cause: mediasource.Expired})
		started := r.ad.blockWalk()
		advance(daily.Change)
		r.ad.Fail("Changes", nil)
		advance(daily.Change)
		walkStarted(t, started)
		j := oneRunningSystemJob(t, r)
		if n := len(r.liveJobs(status.Scan)); n != 1 {
			t.Errorf("%d Scan jobs live, want only the SystemOwner one", n)
		}
		if err := r.jobs.Cancel(j.ID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
	})
}

func TestFailedManualPassRetryCarriesSystemJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		r.ad.Fail("Items", unreachable("503"))
		j, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := r.job(t, j.ID); got.State != status.Done || got.Outcome != status.Failed {
			t.Fatalf("the request is %v %v, want Done Failed", got.State, got.Outcome)
		}
		r.ad.Fail("Items", nil)
		started := r.ad.blockWalk()
		advance(time.Minute - time.Second)
		if n := len(r.liveJobs(status.Scan)); n != 0 {
			t.Fatalf("%d Scan jobs before the retry is due", n)
		}
		advance(time.Second)
		walkStarted(t, started)
		sys := oneRunningSystemJob(t, r)
		if n := len(r.liveJobs(status.Scan)); n != 1 {
			t.Errorf("the retry carries %d Scan jobs, want only its SystemOwner one", n)
		}
		advance(10 * time.Minute)
		if got := r.job(t, sys.ID); got.State != status.Running {
			t.Errorf("ticks during the retry left its job %v %v, want it still running", got.State, got.Outcome)
		}
		r.ad.setItems(nil)
		if err := r.jobs.Cancel(sys.ID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
	})
}

func TestWholeRequestDuringPassRunsNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		release := make(chan struct{})
		walking := make(chan struct{}, 1)
		r.ad.setItems(func(ctx context.Context, y yieldFunc, u mediasource.Unread) error {
			walking <- struct{}{}
			<-release
			return r.ad.Fake.Items(ctx, y, u)
		})
		first, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		<-walking
		second, err := r.scanner.Start(t.Context(), scan.Request{Owner: 2, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		release <- struct{}{}
		<-walking
		if got := r.job(t, first.ID); got.Outcome != status.Succeeded {
			t.Errorf("the first request is %v %v, want Succeeded", got.State, got.Outcome)
		}
		if got := r.job(t, second.ID); got.State != status.Running {
			t.Errorf("the request made during the first pass is %v, want carried by the next", got.State)
		}
		release <- struct{}{}
		synctest.Wait()
		if got := r.job(t, second.ID); got.Outcome != status.Succeeded {
			t.Errorf("the second request is %v %v, want Succeeded", got.State, got.Outcome)
		}
	})
}

func TestRefetchIntoRetryEndsFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		one := movieRef(1)
		r.ad.FailRoot(one, unreachable("episode read failed"))
		j, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Root: &one})
		if err != nil {
			t.Fatal(err)
		}
		advance(daily.Change)
		got := r.job(t, j.ID)
		if got.State != status.Done || got.Outcome != status.Failed || got.Cause == "" {
			t.Errorf("the refetch is %v %v %q, want Done Failed with the read's error", got.State, got.Outcome, got.Cause)
		}
		r.ad.FailRoot(one, nil)
		r.ad.took("Root")
		advance(time.Minute)
		if reads := reads(r.ad.took("Root"), one); len(reads) != 1 {
			t.Errorf("the root's retry read it %d times, want once", len(reads))
		}
		if live := r.liveJobs(status.ItemScan); len(live) != 0 {
			t.Errorf("the retry carries jobs %+v, want none", live)
		}
	})
}

func TestRequestPassCancelRules(t *testing.T) {
	t.Run("a requested pass ends with its last requester", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := booted(t, movieCaps, daily, 1)
			started := r.ad.blockWalk()
			// a's poke wakes the loop, and a pass started before b is queued
			// carries a alone (TestWholeRequestDuringPassRunsNext): the probe
			// holds the tick until both wait.
			probing, queued := make(chan struct{}), make(chan struct{})
			r.ad.setFolders(func(ctx context.Context) ([]string, error) {
				close(probing)
				<-queued
				return r.ad.Fake.RootFolders(ctx)
			})
			a, _ := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
			<-probing
			b, _ := r.scanner.Start(t.Context(), scan.Request{Owner: 2, Source: src})
			r.ad.setFolders(nil)
			close(queued)
			synctest.Wait()
			walkStarted(t, started)
			if err := r.jobs.Cancel(a.ID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			if got := r.job(t, b.ID); got.State != status.Running {
				t.Fatalf("cancelling one requester left the other %v %v, want running", got.State, got.Outcome)
			}
			if err := r.jobs.Cancel(b.ID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			if got := r.job(t, b.ID); got.Outcome != status.Cancelled {
				t.Errorf("the last requester is %v, want Cancelled", got.Outcome)
			}
			if f := r.db.tookFinishes(); len(f) != 1 || f[0].complete {
				t.Errorf("the pass finished %+v, want one FinishPass(false)", f)
			}
		})
	})
	t.Run("a scheduled pass outlives its requesters and ends with its SystemOwner job", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, movieCaps, daily)
			r.put(1, 1)
			r.ad.Fail("Items", unreachable("down"))
			r.start(t)
			advance(30 * time.Second)
			advance(10 * time.Second)
			req, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
			if err != nil {
				t.Fatal(err)
			}
			r.ad.Fail("Items", nil)
			started := r.ad.blockWalk()
			advance(50 * time.Second)
			walkStarted(t, started)
			sys := oneRunningSystemJob(t, r)
			if got := r.job(t, req.ID); got.State != status.Running {
				t.Fatalf("the request is %v, want carried by the due pass", got.State)
			}
			if err := r.jobs.Cancel(req.ID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			if got := r.job(t, sys.ID); got.State != status.Running {
				t.Fatalf("a requester's cancel ended the scheduled pass: %v", got.Outcome)
			}
			if err := r.jobs.Cancel(sys.ID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			if got := r.job(t, sys.ID); got.Outcome != status.Cancelled {
				t.Errorf("the SystemOwner job is %v, want Cancelled", got.Outcome)
			}
		})
	})
	t.Run("a cancelled retry waits its backoff again, undoubled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := booted(t, movieCaps, daily, 1)
			r.ad.Fail("Items", unreachable("down"))
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			advance(time.Minute)
			r.ad.Fail("Items", nil)
			started := r.ad.blockWalk()
			advance(2 * time.Minute)
			walkStarted(t, started)
			sys := oneRunningSystemJob(t, r)
			if err := r.jobs.Cancel(sys.ID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			cancelled := time.Now()
			if got := r.job(t, sys.ID); got.Outcome != status.Cancelled {
				t.Errorf("the retry's job is %v, want Cancelled", got.Outcome)
			}
			r.ad.setItems(nil)
			r.ad.took("Items")
			advance(2*time.Minute - time.Second)
			if n := len(r.ad.took("Items")); n != 0 {
				t.Fatalf("%d walks before the current backoff passed again", n)
			}
			advance(time.Second)
			if got := gaps(cancelled, r.ad.took("Items")); len(got) != 1 || got[0] != 2*time.Minute {
				t.Errorf("the next retry came after %v, want the current 2m backoff", got)
			}
		})
	})
}

func TestCutPassEndsJobsInterrupted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		started := r.ad.blockWalk()
		walk, _ := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
		synctest.Wait()
		walkStarted(t, started)
		one := movieRef(1)
		queued, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Root: &one})
		if err != nil {
			t.Fatal(err)
		}
		r.stop()
		for _, id := range []status.JobID{walk.ID, queued.ID} {
			if got := r.job(t, id); got.State != status.Done || got.Outcome != status.Interrupted {
				t.Errorf("job %s is %v %v after the shutdown, want Done Interrupted", id, got.State, got.Outcome)
			}
		}
	})
}

func TestPassProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 3)
		j, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := r.job(t, j.ID); got.Done != 3 || got.Total != 0 {
			t.Errorf("the whole pass reported %d of %d, want 3 of 0", got.Done, got.Total)
		}
		r.put(1, 2)
		advance(daily.Change)
		snap, _ := r.jobs.Snapshot()
		var batch *status.Job
		for i := range snap.Jobs {
			if snap.Jobs[i].Kind == status.Import {
				batch = &snap.Jobs[i]
				break
			}
		}
		if batch == nil || batch.Done != 2 || batch.Total != 2 || batch.Outcome != status.Succeeded || batch.Source != src {
			t.Errorf("the batch's job is %+v, want 2 of 2 Succeeded for %s", batch, src)
		}
	})
}
