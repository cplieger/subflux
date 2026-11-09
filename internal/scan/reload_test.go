package scan_test

import (
	"context"
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

// over replaces the rig's scanner, before start, with one over set.
func (r *rig) over(set *mediasource.Set) {
	r.set = set
	r.scanner = scan.New(scan.Deps{Store: r.db, Status: r.status, Logger: quiet(), Schedule: r.sched}, set)
}

// gated makes a's walk wait on release before it reads, and answers a
// channel that receives once per walk started.
func gated(a *adapter, release <-chan struct{}) <-chan struct{} {
	walking := make(chan struct{}, 4)
	a.setItems(func(ctx context.Context, y yieldFunc, u mediasource.Unread) error {
		walking <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return &mediasource.SourceError{Cause: mediasource.Unreachable, Err: ctx.Err()}
		}
		return a.Fake.Items(ctx, y, u)
	})
	return walking
}

func TestReloadKeepsUnchangedLoop(t *testing.T) {
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

		release := make(chan struct{})
		walking := gated(b, release)
		running, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: other})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		<-walking
		a2, b2 := newAdapter(r.lib, movieCaps), newAdapter(&otherLib, movieCaps)
		r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: a2, other: b2}), []media.SourceID{src}, nil)
		synctest.Wait()
		if n := len(a2.took("Items")); n != 1 {
			t.Errorf("the changed source's new Set walked %d times after the Reload, want one pass at once", n)
		}
		if got := r.job(t, running.ID); got.State != status.Running {
			t.Errorf("the unchanged source's running pass is %v %v after the Reload, want untouched", got.State, got.Outcome)
		}
		close(release)
		synctest.Wait()
		ended := time.Now()
		if got := r.job(t, running.ID); got.Outcome != status.Succeeded {
			t.Errorf("the unchanged source's pass ended %v, want Succeeded on its Set", got.Outcome)
		}
		advance(daily.Whole - time.Second)
		if n := len(b2.took("Items")); n != 0 {
			t.Errorf("the unchanged source walked %d times before its kept schedule", n)
		}
		advance(time.Second)
		if got := gaps(ended, b2.took("Items")); len(got) != 1 || got[0] != daily.Whole {
			t.Errorf("the unchanged source's next pass came at %v on the new Set, want one a whole interval after its pass", got)
		}
		if n := len(b.took("Items")); n != 1 {
			t.Errorf("the old Set walked the unchanged source %d times, want only the pass in flight", n)
		}
	})
}

func TestRemovedSourceLoopEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 1)
		var otherLib sourcetest.Library
		otherLib.Put(movieRoot(2))
		b := newAdapter(&otherLib, movieCaps)
		r.over(setOf(t, map[media.SourceID]*adapter{src: r.ad, other: b}))
		r.start(t)
		advance(30 * time.Second)
		two := movieRef(2)
		queued, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: other, Root: &two})
		if err != nil {
			t.Fatal(err)
		}
		b.took("RootFolders")
		r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: r.ad}), nil, nil)
		if got := r.job(t, queued.ID); got.State != status.Done || got.Outcome != status.Interrupted {
			t.Errorf("the removed source's queued request is %v %v, want Done Interrupted", got.State, got.Outcome)
		}
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: other}); causeOf(err) != mediasource.UnknownSource {
			t.Errorf("Start for the removed source = %v, want UnknownSource", err)
		}
		advance(daily.Whole)
		if n := len(b.took("RootFolders")); n != 0 {
			t.Errorf("the removed source was probed %d times after the Reload", n)
		}
		if n := len(r.ad.took("Items")); n != 2 {
			t.Errorf("the kept source walked %d times, want its boot pass and its daily one", n)
		}
	})
}

func TestScheduleHotChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 2)
		passed := time.Now()
		head := r.stored(t)
		one := movieRef(1)
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Root: &one}); err != nil {
			t.Fatal(err)
		}
		advance(5 * time.Second)
		r.scanner.SetSchedule(mediasource.Schedule{Change: 10 * time.Second, Whole: time.Hour})
		advance(5 * time.Second)
		ch := r.ad.took("Changes")
		if len(ch) != 1 || !sameBytes(ch[0].cursor, head) {
			t.Errorf("10 s after the boot tick the checks were %+v, want one from the kept cursor %x", ch, head)
		}
		if got := reads(r.ad.took("Root"), one); len(got) != 1 {
			t.Errorf("the queued refetch was read %d times at the re-armed tick, want once", len(got))
		}
		advance(time.Hour - 10*time.Second)
		if got := gaps(passed, r.ad.took("Items")); len(got) != 1 || got[0] != time.Hour {
			t.Errorf("the next whole pass came after %v, want one an hour after the boot pass", got)
		}
		r.ad.took("Changes")
		r.scanner.SetSchedule(mediasource.Schedule{})
		advance(10 * time.Second)
		if n := len(r.ad.took("Changes")); n != 1 {
			t.Errorf("after a refused zero schedule %d checks ran in 10 s, want the kept 10 s interval's one", n)
		}
	})
}

func TestSecretOnlyEditKeepsSchedule(t *testing.T) {
	t.Run("a rekeyed source keeps its loop and reads through the new Set", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := booted(t, movieCaps, daily, 2)
			passed := time.Now()
			head := r.stored(t)
			advance(time.Hour)
			one := movieRef(1)
			if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src, Root: &one}); err != nil {
				t.Fatal(err)
			}
			rekeyed := newAdapter(r.lib, movieCaps)
			r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: rekeyed}), nil, []media.SourceID{src})
			synctest.Wait()
			if n := len(rekeyed.took("Items")); n != 0 {
				t.Errorf("a key-only edit ran %d whole passes at once", n)
			}
			advance(daily.Change)
			ch := rekeyed.took("Changes")
			if len(ch) != 1 || !sameBytes(ch[0].cursor, head) {
				t.Errorf("the next tick checked %+v through the new Set, want one from the kept cursor %x", ch, head)
			}
			if got := reads(rekeyed.took("Root"), one); len(got) != 1 {
				t.Errorf("the kept queue's refetch was read %d times through the new Set, want once", len(got))
			}
			advance(daily.Whole - time.Hour - daily.Change)
			if got := gaps(passed, rekeyed.took("Items")); len(got) != 1 || got[0] != daily.Whole {
				t.Errorf("the next whole pass came at %v, want the kept schedule's one", got)
			}
		})
	})
	t.Run("a changed source runs a whole pass at once", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := booted(t, movieCaps, daily, 2)
			advance(time.Hour)
			moved := newAdapter(r.lib, movieCaps)
			r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: moved}), []media.SourceID{src}, nil)
			synctest.Wait()
			if n := len(moved.took("Items")); n != 1 {
				t.Errorf("a url edit ran %d whole passes at once, want 1", n)
			}
		})
	})
}

func TestSecretOnlyEditRetriesFailedPassAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		r.ad.Fail("Items", &mediasource.SourceError{Cause: mediasource.Unauthorized})
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		for _, d := range backoffs[:4] {
			advance(d)
		}
		if a, ok := r.alert(sourceAlert); !ok || !strings.Contains(a.Text, "refused the API key") {
			t.Fatalf("after the refused passes the alert is %+v, %v", a, ok)
		}
		fixed := newAdapter(r.lib, movieCaps)
		r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: fixed}), nil, []media.SourceID{src})
		advance(daily.Change)
		if n := len(fixed.took("Items")); n != 1 {
			t.Errorf("the 16-minute backoff held the rekeyed source's retry: %d passes at the next wake, want 1", n)
		}
		if a, ok := r.alert(sourceAlert); ok {
			t.Errorf("the complete pass on the new key kept %+v", a)
		}
	})
}
