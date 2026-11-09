package scan_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

var (
	sourceAlert  = status.SourceAlert(string(src))
	overlapAlert = status.SourceOverlapAlert(string(src))
)

func TestHungSourceRaisesAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		r.ad.setFolders(func(ctx context.Context) ([]string, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		advance(daily.Change)
		advance(10 * time.Second)
		a, ok := r.alert(sourceAlert)
		if !ok || !strings.Contains(a.Text, "src cannot be reached") {
			t.Fatalf("after a probe that never answered the alert is %+v, %v; want raised as unreachable", a, ok)
		}
		r.ad.setFolders(nil)
		advance(daily.Change)
		if a, ok := r.alert(sourceAlert); ok {
			t.Errorf("a tick whose probe answered kept %+v", a)
		}
	})
}

func TestWholePassFailureKeepsAlertBetweenAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		r.ad.Fail("Items", unreachable("down"))
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		for range 6 {
			if _, ok := r.alert(sourceAlert); !ok {
				t.Fatalf("at %v between attempts the alert cleared", time.Now())
			}
			advance(daily.Change)
		}
		r.ad.Fail("Items", nil)
		advance(4 * time.Minute)
		if a, ok := r.alert(sourceAlert); ok {
			t.Errorf("a complete whole pass kept %+v", a)
		}
	})
}

func TestCancelledPassRaisesNoSourceAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		started := r.ad.blockWalk()
		j, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		walkStarted(t, started)
		if err := r.jobs.Cancel(j.ID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if a, ok := r.alert(sourceAlert); ok {
			t.Errorf("a cancelled pass raised %+v", a)
		}
	})
}

func TestStorageFaultRaisesNoSourceAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 2)
		r.db.fault(nil, errors.New("bolt: commit failed"))
		r.put(1, 2)
		advance(daily.Change)
		if a, ok := r.alert(sourceAlert); ok {
			t.Errorf("a batch failed by a commit fault raised %+v", a)
		}
		r.db.fault(nil, nil)
		r.ad.Fail("RootFolders", unreachable("down"))
		advance(daily.Change)
		if _, ok := r.alert(sourceAlert); !ok {
			t.Fatal("a failed probe raised nothing")
		}
		r.ad.Fail("RootFolders", nil)
		r.db.fault(nil, errors.New("bolt: commit failed"))
		r.put(1, 1)
		advance(daily.Change)
		if _, ok := r.alert(sourceAlert); !ok {
			t.Error("a tick ending on a commit fault cleared the alert")
		}
		r.db.fault(nil, nil)
		advance(daily.Change)
		if a, ok := r.alert(sourceAlert); ok {
			t.Errorf("a clean tick kept %+v", a)
		}
	})
}

func TestExpiredRaisesNoSourceAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		r.ad.Fail("Changes", &mediasource.SourceError{Cause: mediasource.Expired, Err: errors.New("history pruned")})
		advance(daily.Change)
		if a, ok := r.alert(sourceAlert); ok {
			t.Errorf("an expired cursor raised %+v", a)
		}
		r.ad.Fail("Changes", nil)
		r.ad.took("Items")
		advance(daily.Change)
		if n := len(r.ad.took("Items")); n != 1 {
			t.Errorf("%d whole passes followed the expired cursor, want 1", n)
		}
	})
}

func TestRetrySetKeepsAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 2)
		r.ad.FailRoot(movieRef(1), unreachable("episode read failed"))
		r.put(1, 1)
		advance(daily.Change)
		advance(daily.Change)
		a, ok := r.alert(sourceAlert)
		if !ok || !strings.Contains(a.Text, "episode read failed") {
			t.Fatalf("with a root in the retry set the alert is %+v, %v; want it raised naming the read", a, ok)
		}
		r.ad.FailRoot(movieRef(1), nil)
		advance(daily.Change)
		if a, ok := r.alert(sourceAlert); ok {
			t.Errorf("the root's completed retry kept %+v", a)
		}
	})
}

func TestAlertWords(t *testing.T) {
	for _, tc := range []struct {
		cause mediasource.Cause
		words string
	}{
		{mediasource.Unauthorized, "src refused the API key; failing request: RootFolders: 401"},
		{mediasource.Unreachable, "src cannot be reached; failing request: RootFolders: 401"},
		{mediasource.NotFound, "src does not answer as its API at this URL (a 404, or a web page): check the URL's base path; failing request: RootFolders: 401"},
		{mediasource.Protocol, "src answered something subflux cannot read; failing request: RootFolders: 401"},
	} {
		t.Run(tc.cause.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := booted(t, movieCaps, daily, 1)
				r.ad.Fail("RootFolders", &mediasource.SourceError{Cause: tc.cause, Err: errors.New("401")})
				advance(daily.Change)
				if a, ok := r.alert(sourceAlert); !ok || a.Text != tc.words {
					t.Errorf("alert %q, %v; want %q", a.Text, ok, tc.words)
				}
			})
		})
	}
	t.Run("a path under a mapping's to", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mapped := func(c *rigConfig) { c.cfg.PathMappings = []mediasource.PathMapping{{From: "/arr", To: "/movies"}} }
			r := booted(t, movieCaps, daily, 0, mapped)
			r.put(1, 1)
			advance(daily.Change)
			want := "src reports /movies/1.mkv under /movies, which a mapping already serves: map that folder too or remove the overlap"
			if a, ok := r.alert(sourceAlert); !ok || a.Text != want {
				t.Errorf("alert %q, %v; want %q", a.Text, ok, want)
			}
		})
	})
	for _, reason := range []string{"SkyHook (skyhook.sonarr.tv) did not answer", "2 episodes are listed otherwise"} {
		t.Run(reason, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				j := &failJoin{fail: map[mediasource.NativeID]bool{"1": true}, reason: reason}
				caps := mediasource.Capabilities{Kinds: []media.Kind{media.Episode}, Changes: true}
				r := booted(t, caps, daily, 0, joinedBy(j))
				r.lib.Put(dvdShow(1))
				advance(daily.Change)
				want := "src lists Show 1 in another episode order, and its aired order cannot be read now: " + reason
				if a, ok := r.alert(sourceAlert); !ok || a.Text != want {
					t.Errorf("alert %q, %v; want %q", a.Text, ok, want)
				}
			})
		})
	}
}

func TestFinishPassFaultKeepsOverlapAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.observeOther(t, movieRoot(1))
		r.put(1, 1)
		r.start(t)
		advance(30 * time.Second)
		a, ok := r.alert(overlapAlert)
		want := "src reports videos or episodes that other already owns; nothing is attached for them. Map the two sources to different folders, or remove one."
		if !ok || a.Text != want {
			t.Fatalf("overlap alert %q, %v; want %q", a.Text, ok, want)
		}
		r.db.failFinish(errors.New("bolt: commit failed"))
		r.put(1, 1)
		advance(daily.Change)
		if _, ok := r.alert(overlapAlert); !ok {
			t.Error("a FinishPass fault cleared the overlap alert")
		}
	})
}

func TestOverlapAlertSurvivesCleanBatchElsewhere(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.observeOther(t, movieRoot(1))
		r.put(1, 2)
		r.start(t)
		advance(30 * time.Second)
		if _, ok := r.alert(overlapAlert); !ok {
			t.Fatal("no overlap alert after the boot pass")
		}
		r.put(2, 2)
		advance(daily.Change)
		if _, ok := r.alert(overlapAlert); !ok {
			t.Error("a clean batch on movie 2 cleared the overlap on movie 1")
		}
	})
}

func TestOverlapClearsOnOwnerRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.observeOther(t, movieRoot(1))
		r.put(1, 1)
		r.start(t)
		advance(30 * time.Second)
		if _, ok := r.alert(overlapAlert); !ok {
			t.Fatal("no overlap alert after the boot pass")
		}
		if err := r.db.Activate(t.Context(), statedb.Activation{Sources: []media.SourceID{src}, Languages: r.policy}); err != nil {
			t.Fatal(err)
		}
		r.put(1, 1)
		advance(daily.Change)
		if a, ok := r.alert(overlapAlert); ok {
			t.Errorf("with the owner removed the overlap alert is %+v", a)
		}
	})
}

func TestRemovedSourceRaisesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := booted(t, movieCaps, daily, 1)
		if err := r.db.Activate(t.Context(), statedb.Activation{Sources: []media.SourceID{other}, Languages: r.policy}); err != nil {
			t.Fatal(err)
		}
		r.put(1, 1)
		advance(daily.Change)
		if ch := r.ad.took("Changes"); len(ch) != 1 {
			t.Fatalf("%d change checks ran after the removal, want the batch's one", len(ch))
		}
		if snap, _ := r.jobs.Snapshot(); len(snap.Alerts) != 0 {
			t.Errorf("a pass ended by the removal raised %+v", snap.Alerts)
		}
	})
}
