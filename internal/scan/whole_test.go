package scan_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/status"
)

// backoffs is design 5.1's whole-pass retry wait: one minute doubling to 30.
var backoffs = []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute}

func TestWholePassFailsOnSourceOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 5)
		r.start(t)
		advance(30 * time.Second)
		if got := r.listed(t); len(got) != 5 {
			t.Fatalf("the boot pass listed %v, want 5 roots", got)
		}
		r.scanWork(t)
		before := r.stored(t)
		r.db.tookFinishes()
		r.ad.took("Items")

		r.ad.setItems(func(_ context.Context, y func(mediasource.Root, []mediasource.Item) error, _ mediasource.Unread) error {
			roots, _, _, _ := r.lib.Snapshot()
			for _, lr := range roots[:2] {
				if err := y(lr.Root, lr.Items); err != nil {
					return err
				}
			}
			return unreachable("connection reset")
		})
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		failed := time.Now()

		if f := r.db.tookFinishes(); len(f) != 1 || f[0].complete {
			t.Errorf("the failed walk finished %+v, want one FinishPass(false)", f)
		}
		if got := r.listed(t); len(got) != 5 {
			t.Errorf("after the outage %v are listed, want all 5", got)
		}
		if got := r.stored(t); !sameBytes(got, before) {
			t.Errorf("the stored cursor moved from %x to %x", before, got)
		}
		if n := r.scanWork(t); n != 0 {
			t.Errorf("the failed pass queued %d Scan work, want none", n)
		}
		if n := len(r.ad.took("Items")); n != 1 {
			t.Fatalf("%d walks ran for the request, want 1", n)
		}
		for _, d := range backoffs {
			advance(d)
		}
		if got := gaps(failed, r.ad.took("Items")); !slices.Equal(got, backoffs) {
			t.Errorf("retries came after %v, want %v", got, backoffs)
		}
	})
}

func TestWholePassMovesCursorToHead(t *testing.T) {
	t.Run("a complete pass stores its head and the next batch replays nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, movieCaps, daily)
			r.put(1, 2)
			r.storeCursor(t)
			r.put(3, 4)
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
				t.Fatal(err)
			}
			r.start(t)
			head := r.head(t)
			if got := r.stored(t); !sameBytes(got, head) {
				t.Errorf("stored cursor %x after the pass, want its head %x", got, head)
			}
			r.ad.took("Changes")
			advance(daily.Change)
			ch := r.ad.took("Changes")
			if len(ch) != 1 || !sameBytes(ch[0].cursor, head) {
				t.Errorf("the next check read %+v, want one from the head %x", ch, head)
			}
			if roots := r.ad.took("Root"); len(roots) != 0 {
				t.Errorf("the batch replayed %d roots the pass covered", len(roots))
			}
		})
	})
	t.Run("a failed pass keeps a held cursor", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, movieCaps, daily)
			r.put(1, 2)
			held := r.storeCursor(t)
			r.put(3, 3)
			r.ad.Fail("Items", unreachable("down"))
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
				t.Fatal(err)
			}
			r.start(t)
			r.ad.took("Changes")
			advance(daily.Change)
			ch := r.ad.took("Changes")
			if len(ch) != 1 || !sameBytes(ch[0].cursor, held) {
				t.Errorf("the check after the failure read %+v, want one from the held %x", ch, held)
			}
			if roots := r.ad.took("Root"); len(roots) != 1 || roots[0].root != movieRef(3) {
				t.Errorf("the batch read %+v, want movie 3, listed after the held cursor", roots)
			}
		})
	})
	t.Run("a failed pass seeds a source holding none with its head", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, movieCaps, daily)
			r.put(1, 2)
			r.ad.Fail("Items", unreachable("down"))
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
				t.Fatal(err)
			}
			r.start(t)
			r.put(3, 3)
			advance(daily.Change)
			if roots := r.ad.took("Root"); len(roots) != 1 || roots[0].root != movieRef(3) {
				t.Errorf("the batch during the backoff read %+v, want movie 3, listed after the failed pass's head", roots)
			}
			if got := r.listed(t); !slices.Equal(got, []string{"Movie 3"}) {
				t.Errorf("listed %v, want the batch's movie 3", got)
			}
		})
	})
}

func TestFailedRequestedPassRetriesAfterBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 2)
		r.start(t)
		advance(30 * time.Second)
		advance(time.Hour)
		r.ad.took("Items")

		r.ad.Fail("Items", unreachable("503 Service Unavailable"))
		j, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		failed := time.Now()
		if got := r.job(t, j.ID); got.State != status.Done || got.Outcome != status.Failed {
			t.Errorf("the request's job is %v %v, want Done Failed", got.State, got.Outcome)
		}
		want := []time.Duration{0, time.Minute, 2 * time.Minute, 4 * time.Minute}
		advance(time.Minute)
		advance(2 * time.Minute)
		advance(4 * time.Minute)
		r.ad.Fail("Items", nil)
		advance(8 * time.Minute)
		want = append(want, 8*time.Minute)
		advance(20 * time.Hour)
		if got := gaps(failed, r.ad.took("Items")); !slices.Equal(got, want) {
			t.Errorf("walks came at gaps %v, want %v and none after the complete retry", got, want)
		}
	})
}

func TestFailedWholePassStorageFaultRetries(t *testing.T) {
	t.Run("the fault retries after a minute and raises nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, movieCaps, daily)
			r.put(1, 2)
			r.start(t)
			advance(30 * time.Second)
			advance(time.Hour)
			r.ad.took("Items")

			r.db.fault(nil, errors.New("bolt: commit failed"))
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			failed := time.Now()
			if a, ok := r.alert(sourceAlert); ok {
				t.Errorf("the commit fault raised %+v", a)
			}
			r.db.fault(nil, nil)
			advance(daily.Change)
			if a, ok := r.alert(sourceAlert); ok {
				t.Errorf("a tick while the retry waits raised %+v", a)
			}
			advance(time.Minute - daily.Change)
			advance(20 * time.Hour)
			if got := gaps(failed, r.ad.took("Items")); !slices.Equal(got, []time.Duration{0, time.Minute}) {
				t.Errorf("walks came at gaps %v, want the faulted one and one retry a minute later", got)
			}
			if a, ok := r.alert(sourceAlert); ok {
				t.Errorf("the retry raised %+v", a)
			}
		})
	})
	t.Run("a storage fault after a source failure keeps its alert", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := booted(t, movieCaps, daily, 1)
			r.ad.Fail("Items", unreachable("down"))
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			r.ad.Fail("Items", nil)
			r.db.fault(nil, errors.New("bolt: commit failed"))
			advance(time.Minute)
			if _, ok := r.alert(sourceAlert); !ok {
				t.Fatal("the retry's commit fault cleared the source failure's alert")
			}
			r.db.fault(nil, nil)
			advance(2 * time.Minute)
			if a, ok := r.alert(sourceAlert); ok {
				t.Errorf("a complete retry kept %+v", a)
			}
		})
	})
}

func TestNoCursorFailingWholePassBacksOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 1)
		r.ad.Fail("Items", unreachable("down"))
		boot := time.Now()
		r.start(t)
		advance(30 * time.Second)
		for _, d := range backoffs {
			advance(d)
		}
		want := append([]time.Duration{30 * time.Second}, backoffs...)
		if got := gaps(boot, r.ad.took("Items")); !slices.Equal(got, want) {
			t.Errorf("walks came at gaps %v, want %v", got, want)
		}
	})
}

func TestExcludedNeverObservedUnmarked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		caps := movieCaps
		caps.Tags = true
		r := newRig(t, caps, daily, excluding("no-subflux"))
		r.lib.SetTags(map[mediasource.TagRef]string{"7": "no-subflux"})
		r.put(1, 1)
		hidden := movieRoot(2)
		hidden.Items[0].Tags = []mediasource.TagRef{"7"}
		r.lib.Put(hidden)
		r.ad.Fail("Tags", unreachable("down"))
		r.start(t)
		advance(30 * time.Second)
		if got := r.listed(t); len(got) != 0 {
			t.Fatalf("a pass whose tags failed listed %v, want nothing", got)
		}
		r.ad.Fail("Tags", nil)
		advance(time.Minute)
		rows, _, err := r.db.Roots(t.Context(), "", 10)
		if err != nil || len(rows) != 2 {
			t.Fatalf("Roots = %d rows, %v; want both movies after the retry", len(rows), err)
		}
		for _, row := range rows {
			items, _, err := r.db.Items(t.Context(), row.Key, "", 10)
			if err != nil || len(items) != 1 {
				t.Fatalf("Items(%s) = %d, %v", row.Root.Title, len(items), err)
			}
			if want := row.Root.Title == "Movie 2"; items[0].Item.Excluded != want {
				t.Errorf("%s stored Excluded %v, want %v", row.Root.Title, items[0].Item.Excluded, want)
			}
		}
	})
}

func TestItemsLaterViolationRetiresNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 2)
		r.start(t)
		advance(30 * time.Second)
		r.db.tookFinishes()
		r.ad.setItems(func(_ context.Context, y func(mediasource.Root, []mediasource.Item) error, _ mediasource.Unread) error {
			a := movieRoot(1)
			if err := y(a.Root, a.Items); err != nil {
				return err
			}
			return y(a.Root, a.Items)
		})
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if f := r.db.tookFinishes(); len(f) != 1 || f[0].complete {
			t.Errorf("the refused walk finished %+v, want one FinishPass(false)", f)
		}
		if got := r.listed(t); !slices.Equal(got, []string{"Movie 1", "Movie 2"}) {
			t.Errorf("after the refused walk %v are listed, want both", got)
		}
	})
}

func TestCancelledScheduledPassDoesNotRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 1)
		started := r.ad.blockWalk()
		r.start(t)
		advance(30 * time.Second)
		walkStarted(t, started)
		live := r.liveJobs(status.Scan)
		if len(live) != 1 || live[0].Owner != status.SystemOwner || live[0].State != status.Running {
			t.Fatalf("the scheduled pass holds %+v, want one running SystemOwner job", live)
		}
		if err := r.jobs.Cancel(live[0].ID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		cancelled := time.Now()
		if got := r.job(t, live[0].ID); got.State != status.Done || got.Outcome != status.Cancelled {
			t.Errorf("the job is %v %v, want Done Cancelled", got.State, got.Outcome)
		}
		r.ad.setItems(nil)
		r.ad.took("Items")
		advance(daily.Whole - time.Second)
		if n := len(r.ad.took("Items")); n != 0 {
			t.Errorf("%d walks ran before the next scheduled time", n)
		}
		advance(time.Second)
		if got := gaps(cancelled, r.ad.took("Items")); !slices.Equal(got, []time.Duration{daily.Whole}) {
			t.Errorf("the next walk came after %v, want one a whole interval after the cancel", got)
		}
	})
}

func TestCancelledManualPassKeepsSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 1)
		r.start(t)
		advance(30 * time.Second)
		completed := time.Now()
		advance(time.Hour)
		started := r.ad.blockWalk()
		j, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		walkStarted(t, started)
		for _, live := range r.liveJobs(status.Scan) {
			if live.Owner == status.SystemOwner {
				t.Errorf("a requested pass carries SystemOwner job %s", live.ID)
			}
		}
		if err := r.jobs.Cancel(j.ID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := r.job(t, j.ID); got.State != status.Done || got.Outcome != status.Cancelled {
			t.Errorf("the job is %v %v, want Done Cancelled", got.State, got.Outcome)
		}
		r.ad.setItems(nil)
		r.ad.took("Items")
		advance(daily.Whole - time.Hour - time.Second)
		if n := len(r.ad.took("Items")); n != 0 {
			t.Errorf("%d walks ran before the kept schedule", n)
		}
		advance(time.Second)
		if got := gaps(completed, r.ad.took("Items")); !slices.Equal(got, []time.Duration{daily.Whole}) {
			t.Errorf("the next walk came %v after the boot pass, want one whole interval", got)
		}
	})
}

func TestPassesNeverOverlap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 3)
		r.ad.setItems(func(ctx context.Context, y func(mediasource.Root, []mediasource.Item) error, u mediasource.Unread) error {
			time.Sleep(10 * time.Minute)
			return r.ad.Fake.Items(ctx, y, u)
		})
		r.start(t)
		advance(30 * time.Second)
		var reqs []status.JobID
		for owner := range int64(3) {
			j, err := r.scanner.Start(t.Context(), scan.Request{Owner: owner + 1, Source: src})
			if err != nil {
				t.Fatal(err)
			}
			reqs = append(reqs, j.ID)
		}
		r.put(4, 4)
		advance(time.Minute)
		if n := len(r.ad.took("Items")); n != 1 {
			t.Errorf("%d walks started during the first, want only the first", n)
		}
		advance(30 * time.Minute)
		if n := len(r.ad.took("Items")); n != 1 {
			t.Errorf("%d walks ran after the first, want one carrying every request", n)
		}
		for _, id := range reqs {
			if got := r.job(t, id); got.State != status.Done || got.Outcome != status.Succeeded {
				t.Errorf("request %s is %v %v, want Done Succeeded", id, got.State, got.Outcome)
			}
		}
		if p := r.ad.peak(); p != 1 {
			t.Errorf("%d walks and root reads ran at once, want 1", p)
		}
		if got := r.listed(t); !slices.Equal(got, []string{"Movie 1", "Movie 2", "Movie 3", "Movie 4"}) {
			t.Errorf("listed %v, want all four", got)
		}
	})
}

// moviesOnly serves movies with no Changes, so only whole passes and the
// retry set read the library.
var moviesOnly = mediasource.Capabilities{Kinds: []media.Kind{media.Movie}}

func TestUnreadableRootDoesNotStopWholePassWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, moviesOnly, daily)
		r.put(1, 21)
		r.start(t)
		advance(30 * time.Second)
		if got := r.listed(t); len(got) != 21 {
			t.Fatalf("the boot pass listed %d roots, want 21", len(got))
		}
		r.scanWork(t)
		r.db.tookFinishes()

		p := movieRef(7)
		r.ad.FailRoot(p, unreachable("the read of movie 7 timed out"))
		bidi := movieRoot(13)
		bidi.Items[0].VideoPath = "/movies/13\u200f.mkv"
		r.lib.Put(bidi)
		r.lib.Remove(movieRef(21))
		advance(daily.Whole)

		f := r.db.tookFinishes()
		if len(f) != 1 || !f[0].complete {
			t.Fatalf("the scheduled pass finished %+v, want one complete FinishPass", f)
		}
		if want := []mediasource.NativeRef{p, movieRef(13)}; !sameRefs(f[0].keep, want) {
			t.Errorf("the pass kept %v, want %v", f[0].keep, want)
		}
		listed := r.listed(t)
		if slices.Contains(listed, "Movie 21") {
			t.Errorf("the root the walk no longer lists is still listed: %v", listed)
		}
		for _, kept := range []string{"Movie 7", "Movie 13"} {
			if !slices.Contains(listed, kept) {
				t.Errorf("%s, unread, was retired: %v", kept, listed)
			}
		}
		if n := r.scanWork(t); n != 18 {
			t.Errorf("the pass queued %d Scan work, want one per instance of the 18 read roots", n)
		}
		a, ok := r.alert(status.SourceAlert(string(src)))
		if !ok {
			t.Fatal("no source alert names the unread roots")
		}
		if !strings.Contains(a.Text, "root 13") {
			t.Errorf("the alert reads %q, want the first unread root in ref order, 13", a.Text)
		}
		for range 6 {
			advance(daily.Change)
			if b, _ := r.alert(status.SourceAlert(string(src))); !strings.Contains(b.Text, "root 13") {
				t.Fatalf("the alert moved to %q while both roots stay unread, want it on root 13", b.Text)
			}
		}
	})
}

func TestThreeConsecutiveUnreadFailsPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, moviesOnly, daily)
		r.put(1, 5)
		r.start(t)
		advance(30 * time.Second)
		r.scanWork(t)
		r.db.tookFinishes()

		for n := 2; n <= 4; n++ {
			r.ad.FailRoot(movieRef(n), unreachable("the read failed"))
		}
		r.lib.Remove(movieRef(5))
		advance(daily.Whole)

		if f := r.db.tookFinishes(); len(f) != 1 || f[0].complete || f[0].keep != nil {
			t.Errorf("the pass finished %+v, want one FinishPass(false) keeping nothing", f)
		}
		if got := r.listed(t); len(got) != 5 {
			t.Errorf("after the failed pass %v are listed, want all 5", got)
		}
		if n := r.scanWork(t); n != 0 {
			t.Errorf("the failed pass queued %d Scan work, want none", n)
		}
	})
}

func TestUnreadRootsUnderFailedProbeFailPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, moviesOnly, daily)
		r.put(1, 3)
		r.start(t)
		advance(30 * time.Second)
		r.scanWork(t)
		r.db.tookFinishes()

		r.ad.FailRoot(movieRef(2), unreachable("the read failed"))
		r.lib.Remove(movieRef(3))
		r.ad.setItems(func(ctx context.Context, y func(mediasource.Root, []mediasource.Item) error, u mediasource.Unread) error {
			err := r.ad.Fake.Items(ctx, y, u)
			r.ad.Fail("RootFolders", unreachable("connection refused"))
			return err
		})
		advance(daily.Whole)
		failed := time.Now()

		if f := r.db.tookFinishes(); len(f) != 1 || f[0].complete {
			t.Errorf("the pass finished %+v, want one FinishPass(false)", f)
		}
		if got := r.listed(t); len(got) != 3 {
			t.Errorf("after the failed probe %v are listed, want all 3", got)
		}
		if n := r.scanWork(t); n != 0 {
			t.Errorf("the failed pass queued %d Scan work, want none", n)
		}
		r.ad.setItems(nil)
		r.ad.Fail("RootFolders", nil)
		r.ad.took("Items")
		advance(time.Minute)
		if got := gaps(failed, r.ad.took("Items")); !slices.Equal(got, []time.Duration{time.Minute}) {
			t.Errorf("the next walk came %v after the failed pass, want its outage backoff of one minute", got)
		}
	})
}

func TestKeptRootRecoversThroughRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, moviesOnly, daily)
		r.put(1, 3)
		p := movieRef(2)
		r.ad.FailRoot(p, unreachable("the read of movie 2 timed out"))
		r.start(t)
		advance(30 * time.Second)
		if _, ok := r.alert(status.SourceAlert(string(src))); !ok {
			t.Fatal("the kept root raised no source alert")
		}
		r.ad.took("Root")

		r.ad.FailRoot(p, nil)
		advance(time.Minute)
		roots := r.ad.took("Root")
		if len(roots) != 1 {
			t.Fatalf("%d one-root passes ran at the retry, want 1", len(roots))
		}
		if a, ok := r.alert(status.SourceAlert(string(src))); ok {
			t.Errorf("the alert %q stays after the kept root read again", a.Text)
		}
		if got := r.listed(t); len(got) != 3 {
			t.Errorf("after the retry %v are listed, want all 3", got)
		}
	})
}

func sameRefs(got, want []mediasource.NativeRef) bool {
	return slices.Equal(slices.SortedFunc(slices.Values(got), cmpRef), slices.SortedFunc(slices.Values(want), cmpRef))
}

func cmpRef(a, b mediasource.NativeRef) int { return strings.Compare(string(a.ID), string(b.ID)) }

// systemScans answers id's SystemOwner Scan jobs started at or after since.
func (r *rig) systemScans(id media.SourceID, since time.Time) []status.Job {
	snap, _ := r.jobs.Snapshot()
	var out []status.Job
	for _, j := range snap.Jobs {
		if j.Kind == status.Scan && j.Owner == status.SystemOwner && j.Source == id && !j.Started.Before(since) {
			out = append(out, j)
		}
	}
	return out
}

func TestPassNowRunsWholePassPerSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 1)
		var otherLib sourcetest.Library
		otherLib.Put(movieRoot(2))
		a, b := r.ad, newAdapter(&otherLib, movieCaps)
		r.over(setOf(t, map[media.SourceID]*adapter{src: a, other: b}))
		r.start(t)
		advance(30 * time.Second)
		advance(time.Hour)
		a.took("Items")
		b.took("Items")

		idle := time.Now()
		r.scanner.PassNow()
		r.scanner.PassNow()
		synctest.Wait()
		for id, ad := range map[media.SourceID]*adapter{src: a, other: b} {
			if n := len(ad.took("Items")); n != 1 {
				t.Errorf("%s walked %d times after two PassNow calls, want 1", id, n)
			}
			js := r.systemScans(id, idle)
			if len(js) != 1 || js[0].Outcome != status.Succeeded {
				t.Errorf("%s's SystemOwner Scan jobs since PassNow are %+v, want one Succeeded", id, js)
			}
		}

		release := make(chan struct{})
		walking := gated(b, release)
		r.scanner.PassNow()
		synctest.Wait()
		<-walking
		advance(time.Second)
		called := time.Now()
		r.scanner.PassNow()
		r.scanner.PassNow()
		synctest.Wait()
		close(release)
		synctest.Wait()
		if n := len(b.took("Items")); n != 2 {
			t.Errorf("other walked %d times, want the gated pass and one more at once", n)
		}
		advance(2 * daily.Change)
		if n := len(b.took("Items")); n != 0 {
			t.Errorf("other walked %d more times after the pass PassNow asked for", n)
		}
		if js := r.systemScans(other, called); len(js) != 1 {
			t.Errorf("other's SystemOwner Scan jobs started after the calls are %+v, want one", js)
		}
		select {
		case <-walking:
		default:
			t.Error("no second walk started after the gated one")
		}
	})
}
