package mediarefresh_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
	"github.com/cplieger/subflux/internal/statedb"
)

var firstShow = show{native: "73", tvdb: 100, episodes: []int{1}}

// lastCall is calls' last element; it fails t when there is none.
func lastCall(t *testing.T, calls []call) call {
	t.Helper()
	if len(calls) == 0 {
		t.Fatal("no rescan was sent")
	}
	return calls[len(calls)-1]
}

// warnsWith are the WARN lines holding every part.
func warnsWith(l *logs, parts ...string) int {
	n := 0
	for _, line := range l.warns() {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(line, p)
		}
		if all {
			n++
		}
	}
	return n
}

func TestOutageLongerThanCapStillRefreshes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		r.list(srcA, firstShow)
		after(settle)
		up := time.Now().Add(2 * time.Hour)
		r.src(srcA).script(downUntil(up))
		r.deliver(r.ep(srcA, 100, 1))
		after(2*time.Hour + 11*time.Minute)
		calls := r.src(srcA).sent(firstShow.ref())
		if last := lastCall(t, calls); last.err != nil || last.at.Before(up) {
			t.Errorf("the last rescan was at %v with %v, want a success after the source came back at %v", last.at, last.err, up)
		}
		if n := r.src(srcA).succeeded(firstShow.ref()); n != 1 {
			t.Errorf("%d rescans succeeded over %d calls, want 1 after the 2-hour outage", n, len(calls))
		}
	})
}

func TestRecoveryAfterSeventhAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		r.list(srcA, firstShow)
		after(settle)
		// 30 s doubling to 10 minutes: 7 failed attempts end by 35 minutes.
		up := time.Now().Add(40 * time.Minute)
		r.src(srcA).script(downUntil(up))
		r.deliver(r.ep(srcA, 100, 1))
		after(time.Hour)
		calls := r.src(srcA).sent(firstShow.ref())
		if failed := len(calls) - r.src(srcA).succeeded(firstShow.ref()); failed < 7 {
			t.Errorf("%d rescans failed before recovery, want the outage to outlast 7", failed)
		}
		if n := r.src(srcA).succeeded(firstShow.ref()); n != 1 || lastCall(t, calls).err != nil {
			t.Errorf("%d rescans succeeded, the last ending %v; want one success after recovery", n, lastCall(t, calls).err)
		}
	})
}

func TestOneItemFailingWhilePingAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		shows := make([]show, 10)
		for i := range shows {
			shows[i] = show{native: strconv.Itoa(i + 1), tvdb: 101 + i, episodes: []int{1}}
		}
		r.list(srcA, shows...)
		after(settle)
		r.src(srcA).script(func(_ context.Context, ref mediasource.NativeRef, n int) error {
			if ref.ID == "1" && n <= 2 || ref.ID == "4" {
				return errDown
			}
			return nil
		}, nil)
		recs := map[int]statedb.FileRecord{}
		for _, sh := range shows {
			recs[sh.tvdb] = r.deliver(r.ep(srcA, sh.tvdb, 1))
		}
		after(2 * time.Hour)
		src := r.src(srcA)
		if got, ok := len(src.sent(shows[0].ref())), src.succeeded(shows[0].ref()); got != 3 || ok != 1 {
			t.Errorf("root 1: %d calls, %d succeeded; want its third call to succeed", got, ok)
		}
		if got := len(src.sent(shows[3].ref())); got != 6 || src.succeeded(shows[3].ref()) != 0 {
			t.Errorf("root 4: %d calls; want its own 6 attempts", got)
		}
		if n := warnsWith(r.logs, "skipped", "root=4"); n != 1 {
			t.Errorf("%d WARNs name root 4 skipped, want 1:\n%s", n, strings.Join(r.logs.warns(), ""))
		}
		for _, sh := range append(shows[1:3:3], shows[4:]...) {
			if got := len(src.sent(sh.ref())); got != 1 || src.succeeded(sh.ref()) != 1 {
				t.Errorf("root %s: %d calls, want one successful rescan", sh.native, got)
			}
		}
		r.retime(recs[102])
		after(settle)
		if got := len(src.sent(shows[1].ref())); got != 2 {
			t.Errorf("root 2 after a later write: %d calls, want 2, the cursor past the batch", got)
		}
		if got := len(src.sent(shows[3].ref())); got != 6 {
			t.Errorf("root 4 after a later write elsewhere: %d calls, want its 6", got)
		}
	})
}

func TestSinceFailureKeepsCursor(t *testing.T) {
	shows := []show{{native: "1", tvdb: 101, episodes: []int{1}}, {native: "2", tvdb: 102, episodes: []int{1}}, {native: "3", tvdb: 103, episodes: []int{1}}}
	t.Run("one failure", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, srcA)
			r.list(srcA, shows...)
			after(settle)
			r.feed.failSince(1)
			recs := map[int]statedb.FileRecord{}
			for _, sh := range shows {
				recs[sh.tvdb] = r.deliver(r.ep(srcA, sh.tvdb, 1))
			}
			after(settle)
			if got := r.src(srcA).refs(); len(got) != 0 || len(r.logs.warns()) != 1 {
				t.Fatalf("after the failed read: rescans %v, WARNs %q; want none and one", got, r.logs.warns())
			}
			after(retryFirst)
			for _, sh := range shows {
				if got := len(r.src(srcA).sent(sh.ref())); got != 1 {
					t.Errorf("root %s after the retry: %d rescans, want 1", sh.native, got)
				}
			}
			r.retime(recs[101])
			after(settle)
			if got := len(r.src(srcA).sent(shows[0].ref())); got != 2 {
				t.Errorf("root 1 after a later write: %d rescans, want 2", got)
			}
		})
	})
	t.Run("retries double to 10 minutes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, srcA)
			r.list(srcA, shows[0])
			after(settle)
			r.feed.failSince(8)
			first := len(r.feed.sinceTimes())
			r.deliver(r.ep(srcA, 101, 1))
			after(time.Hour)
			at := r.feed.sinceTimes()[first:]
			want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute, 10 * time.Minute}
			if len(at) < len(want)+1 {
				t.Fatalf("%d reads after the delivery, want at least %d", len(at), len(want)+1)
			}
			for i, w := range want {
				if got := at[i+1].Sub(at[i]); got != w {
					t.Errorf("retry %d came %v after the failure, want %v", i+1, got, w)
				}
			}
			if got := len(r.src(srcA).sent(shows[0].ref())); got != 1 {
				t.Errorf("%d rescans after the feed recovered, want 1", got)
			}
		})
	})
}

func TestRootTargetsFailureKeepsCursor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		shows := []show{{native: "21", tvdb: 201, episodes: []int{1}}, {native: "22", tvdb: 202, episodes: []int{1}}, {native: "23", tvdb: 203, episodes: []int{1}}}
		r.list(srcA, shows...)
		after(settle)
		r.feed.failTargets(r.ep(srcA, 201, 1).key.Root(), 1)
		// Five transient failures while ping answers: a sixth counted attempt,
		// had the read failure counted, would skip the root instead.
		r.src(srcA).script(func(_ context.Context, ref mediasource.NativeRef, n int) error {
			if ref.ID == "21" && n <= 5 {
				return errDown
			}
			return nil
		}, nil)
		recs := map[int]statedb.FileRecord{}
		for _, sh := range shows {
			recs[sh.tvdb] = r.deliver(r.ep(srcA, sh.tvdb, 1))
		}
		after(settle)
		if got := r.src(srcA).refs(); len(got) != 0 || warnsWith(r.logs, "targets of root") != 1 {
			t.Fatalf("after the failed read: rescans %v, WARNs %q; want none and one naming the read", got, r.logs.warns())
		}
		after(time.Hour)
		src := r.src(srcA)
		if got, ok := len(src.sent(shows[0].ref())), src.succeeded(shows[0].ref()); got != 6 || ok != 1 {
			t.Errorf("root 21: %d calls, %d succeeded; want the sixth to succeed", got, ok)
		}
		for _, sh := range shows[1:] {
			if got := len(src.sent(sh.ref())); got != 1 {
				t.Errorf("root %s: %d rescans, want 1", sh.native, got)
			}
		}
		r.retime(recs[202])
		after(settle)
		if got := len(src.sent(shows[1].ref())); got != 2 {
			t.Errorf("root 22 after a later write: %d rescans, want 2", got)
		}
	})
}

// sonarrDir is the recorded Sonarr v5 the sonarr adapter's tests serve.
const sonarrDir = "../mediasource/sonarr/testdata/v5-api-v5"

func TestOutcomeUnknownIsNotResent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		srv := arrtest.Load(t, sonarrDir)
		var mu sync.Mutex
		posts := map[int]int{}
		srv.Handle("/api/v5/command", func(w http.ResponseWriter, req *http.Request) {
			var cmd struct {
				Name     string `json:"name"`
				SeriesID int    `json:"seriesId"`
			}
			if err := json.NewDecoder(req.Body).Decode(&cmd); err != nil || cmd.Name != "RescanSeries" {
				t.Errorf("command %+v, %v; want RescanSeries", cmd, err)
			}
			mu.Lock()
			posts[cmd.SeriesID]++
			unattributed := cmd.SeriesID == 2188 && posts[cmd.SeriesID] == 1
			mu.Unlock()
			if unattributed {
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"id":7}`)
				return
			}
			srv.Write(w, http.StatusCreated, []byte(`{"id":7}`))
		})
		cfg := arrtest.Config(t, "Sonarr", "http://sonarr.test", "")
		cfg.ID = srcA
		set, err := mediasource.NewSet([]mediasource.AdapterKind{sonarr.Kind}, []mediasource.Config{cfg}, mediasource.Deps{HTTP: srv.Client(), Logger: slog.New(slog.DiscardHandler)}, sourcetest.OwnPlaces{})
		if err != nil {
			t.Fatal(err)
		}
		r.r.ReloadSources(set)
		// Firefly, Shoah and Monogatari, as the recording numbers them.
		shows := []show{{native: "2188", tvdb: 78874, episodes: []int{1}}, {native: "254", tvdb: 254, episodes: []int{1}}, {native: "2189", tvdb: 2189, episodes: []int{1}}}
		r.list(srcA, shows...)
		after(settle)
		recs := map[int]statedb.FileRecord{}
		for _, sh := range shows {
			recs[sh.tvdb] = r.deliver(r.ep(srcA, sh.tvdb, 1))
		}
		after(30 * time.Minute)
		mu.Lock()
		got := map[int]int{2188: posts[2188], 254: posts[254], 2189: posts[2189]}
		mu.Unlock()
		if got[2188] != 1 || got[254] != 1 || got[2189] != 1 {
			t.Errorf("POSTs per series = %v, want one each", got)
		}
		if n := warnsWith(r.logs, "not sent again", "root=2188"); n != 1 {
			t.Errorf("%d WARNs name root 2188 as not sent again, want 1:\n%s", n, strings.Join(r.logs.warns(), ""))
		}
		r.retime(recs[254])
		after(settle)
		mu.Lock()
		defer mu.Unlock()
		if posts[254] != 2 || posts[2188] != 1 {
			t.Errorf("after a later write to Shoah: POSTs %v, want Shoah's second and no other", posts)
		}
	})
}

func TestRescanTimeoutIsTransient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		r.list(srcA, firstShow)
		after(settle)
		r.src(srcA).script(func(ctx context.Context, _ mediasource.NativeRef, n int) error {
			if n == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}, nil)
		r.deliver(r.ep(srcA, 100, 1))
		after(settle + 30*time.Second + retryFirst)
		calls := r.src(srcA).sent(firstShow.ref())
		if len(calls) != 2 || calls[0].took != 30*time.Second || calls[1].err != nil {
			t.Fatalf("calls = %+v, want a 30 s timeout then a success", calls)
		}
		if p := r.src(srcA).pinged(); p != 1 {
			t.Errorf("%d pings, want 1 after the timeout", p)
		}
	})
}

func TestUnauthorizedAdvances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		r.list(srcA, firstShow)
		after(settle)
		r.src(srcA).script(func(context.Context, mediasource.NativeRef, int) error {
			return &mediasource.SourceError{Cause: mediasource.Unauthorized}
		}, nil)
		rec := r.deliver(r.ep(srcA, 100, 1))
		after(time.Hour)
		if got := len(r.src(srcA).sent(firstShow.ref())); got != 1 || r.src(srcA).pinged() != 0 {
			t.Errorf("%d rescans and %d pings over an hour, want one rescan and no ping", got, r.src(srcA).pinged())
		}
		if n := warnsWith(r.logs, "rescan failed", "root=73", "unauthorized"); n != 1 {
			t.Errorf("%d WARNs name the failed rescan, want 1:\n%s", n, strings.Join(r.logs.warns(), ""))
		}
		r.retime(rec)
		after(settle)
		if got := len(r.src(srcA).sent(firstShow.ref())); got != 2 {
			t.Errorf("after a later write: %d rescans, want 2", got)
		}
	})
}

func TestFailedRescanRaisesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		other := show{native: "74", tvdb: 101, episodes: []int{1}}
		r.list(srcA, firstShow, other)
		after(settle)
		r.src(srcA).script(func(_ context.Context, ref mediasource.NativeRef, _ int) error {
			if ref.ID == "73" {
				return &mediasource.SourceError{Cause: mediasource.NotFound}
			}
			return nil
		}, nil)
		r.deliver(r.ep(srcA, 100, 1))
		r.deliver(r.ep(srcA, 101, 1))
		after(settle)
		if n := warnsWith(r.logs, "rescan failed", "source=sonarr-a", "root=73"); n != 1 {
			t.Errorf("%d WARNs name the failed rescan's source and root, want 1", n)
		}
		if got := r.src(srcA).succeeded(other.ref()); got != 1 {
			t.Errorf("the next root: %d rescans, want 1 after the failure", got)
		}
	})
}

func TestDeliveryTimingDeleteReconcileRescan(t *testing.T) {
	a, b := show{native: "73", tvdb: 100, episodes: []int{1}}, show{native: "91", tvdb: 100, episodes: []int{2}}
	for _, c := range []struct {
		name string
		// write lands any setup and returns the write under test.
		write func(r *rig) func()
	}{
		{"delivery", func(r *rig) func() { return func() { r.deliver(r.ep(srcA, 100, 1)) } }},
		{"timing save", func(r *rig) func() {
			rec := r.deliver(r.ep(srcA, 100, 1))
			return func() { r.retime(rec) }
		}},
		{"delete", func(r *rig) func() {
			rec := r.deliver(r.ep(srcA, 100, 1))
			return func() { r.remove(rec) }
		}},
		{"reconcile", func(r *rig) func() {
			rec := r.deliver(r.ep(srcB, 100, 2))
			return func() { r.remove(rec) }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t, srcA, srcB)
				r.list(srcA, a)
				r.list(srcB, b)
				after(settle)
				write := c.write(r)
				after(settle)
				beforeA, beforeB := len(r.src(srcA).sent(a.ref())), len(r.src(srcB).sent(b.ref()))
				write()
				after(settle)
				if got := len(r.src(srcA).sent(a.ref())) - beforeA; got != 1 {
					t.Errorf("sonarr-a rescanned its root %d times, want 1", got)
				}
				if got := len(r.src(srcB).sent(b.ref())) - beforeB; got != 1 {
					t.Errorf("sonarr-b rescanned its root %d times, want 1", got)
				}
			})
		})
	}
}

func TestSharedRootEachOwnSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA, srcB, srcC)
		a, b := show{native: "73", tvdb: 100, episodes: []int{1}}, show{native: "91", tvdb: 100, episodes: []int{2}}
		r.list(srcA, a)
		r.list(srcB, b)
		r.list(srcC, show{native: "55", tvdb: 300, episodes: []int{1}})
		after(settle)
		r.deliver(r.ep(srcA, 100, 1))
		after(settle)
		if got := r.src(srcA).refs(); len(got) != 1 || got[0] != a.ref() {
			t.Errorf("sonarr-a rescanned %v, want only its own %v", got, a.ref())
		}
		if got := r.src(srcB).refs(); len(got) != 1 || got[0] != b.ref() {
			t.Errorf("sonarr-b rescanned %v, want only its own %v", got, b.ref())
		}
		if got := r.src(srcC).refs(); len(got) != 0 {
			t.Errorf("sonarr-c, owning nothing under the root, rescanned %v", got)
		}
	})
}

func TestMovedVideoRefreshedAtNewPath(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		r.list(srcA, firstShow)
		after(settle)
		r.deliver(r.ep(srcA, 100, 1))
		after(settle)
		r.listAt(srcA, func(show, int) string { return "/tv/moved/Show/E01.mkv" }, firstShow)
		after(settle)
		if got := len(r.src(srcA).sent(firstShow.ref())); got != 2 {
			t.Errorf("%d rescans, want one for the delivery and one for the move", got)
		}
	})
}

func TestFiveDeliveriesOneRescan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		sh := show{native: "73", tvdb: 100, episodes: []int{1, 2, 3, 4, 5}}
		r.list(srcA, sh)
		after(settle)
		for n := 1; n <= 5; n++ {
			r.deliver(r.ep(srcA, 100, n))
			if n < 5 {
				time.Sleep(time.Second - time.Millisecond)
			}
		}
		after(settle)
		if got := len(r.src(srcA).sent(sh.ref())); got != 1 {
			t.Errorf("five deliveries within 5 s: %d rescans, want 1", got)
		}
	})
}

func TestWriteDuringBatchIsRescanned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, srcA)
		later := show{native: "74", tvdb: 101, episodes: []int{1}}
		r.list(srcA, firstShow, later)
		after(settle)
		r.src(srcA).script(downUntil(time.Now().Add(2 * time.Minute)))
		r.deliver(r.ep(srcA, 100, 1))
		after(settle + 10*time.Second)
		if got := len(r.src(srcA).sent(firstShow.ref())); got != 1 {
			t.Fatalf("%d rescans of the first root, want 1 failed one holding its batch in backoff", got)
		}
		r.deliver(r.ep(srcA, 101, 1))
		after(time.Hour)
		if n := r.src(srcA).succeeded(later.ref()); n != 1 {
			t.Errorf("a write landing during an earlier batch's backoff: %d rescans of its root succeeded, want 1", n)
		}
		if got := len(r.src(srcA).sent(later.ref())); got != 1 {
			t.Errorf("%d rescans of the later root, want 1 after the source came back", got)
		}
	})
}
