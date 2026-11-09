package scan_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/status"
)

// scanSeries is what one gather reads from a Scanner's families.
type scanSeries struct {
	lastComplete map[media.SourceID]float64
	interval     float64
	families     int
}

// gatherer registers s's collectors in a registry prefixed as obs's is and
// answers a reader of its two families.
func gatherer(t *testing.T, s *scan.Scanner) func() scanSeries {
	t.Helper()
	reg := measure.NewRegistry("subflux")
	reg.MustRegister(s.Collectors()...)
	return func() scanSeries {
		t.Helper()
		fams, err := reg.Gather(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out := scanSeries{lastComplete: map[media.SourceID]float64{}}
		for _, f := range fams {
			switch f.Name {
			case "subflux_scan_last_complete_timestamp_seconds":
				out.families++
				for _, s := range f.Samples {
					out.lastComplete[media.SourceID(s.Labels[0].Value)] = s.Value
				}
			case "subflux_scan_whole_interval_seconds":
				out.families++
				out.interval = f.Samples[0].Value
			}
		}
		if out.families != 2 {
			t.Fatalf("gathered %d of the two scan families", out.families)
		}
		return out
	}
}

func unix(at time.Time) float64 { return float64(at.UnixMilli()) / 1e3 }

func TestLastCompleteMovesOnCompletePassOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 1)
		read := gatherer(t, r.scanner)
		r.start(t)
		advance(30 * time.Second)
		booted := unix(time.Now())
		if got := read().lastComplete[src]; got != booted {
			t.Fatalf("after the boot pass the series reads %v, want its end %v", got, booted)
		}
		advance(time.Hour)

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
		if got := r.job(t, j.ID); got.Outcome != status.Cancelled {
			t.Fatalf("the blocked pass ended %v, want Cancelled", got.Outcome)
		}
		if got := read().lastComplete[src]; got != booted {
			t.Errorf("a cancelled pass moved the series to %v, want %v", got, booted)
		}

		r.ad.setItems(func(context.Context, func(mediasource.Root, []mediasource.Item) error, mediasource.Unread) error {
			return unreachable("connection reset")
		})
		if _, err := r.scanner.Start(t.Context(), scan.Request{Owner: 1, Source: src}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := read().lastComplete[src]; got != booted {
			t.Errorf("a failed pass moved the series to %v, want %v", got, booted)
		}

		r.ad.setItems(nil)
		advance(time.Minute)
		if got, want := read().lastComplete[src], unix(time.Now()); got != want {
			t.Errorf("after the retry completed the series reads %v, want its end %v", got, want)
		}
	})
}

func TestLastCompleteSeededAtLoopStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 1)
		read := gatherer(t, r.scanner)
		if got := read().lastComplete; len(got) != 0 {
			t.Errorf("before Run the series are %v, want none", got)
		}
		r.start(t)
		if got, want := read().lastComplete[src], unix(time.Now()); got != want {
			t.Errorf("before any pass the series reads %v, want the loop's start %v", got, want)
		}
		advance(30 * time.Second)
		advance(time.Hour)

		b := newAdapter(r.lib, movieCaps)
		started := b.blockWalk()
		r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: r.ad, other: b}), []media.SourceID{other}, nil)
		synctest.Wait()
		walkStarted(t, started)
		if got, want := read().lastComplete[other], unix(time.Now()); got != want {
			t.Errorf("the added source's series reads %v before its first pass completes, want its loop's start %v", got, want)
		}
	})
}

func TestRemovedSourceSeriesDeleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, movieCaps, daily)
		r.put(1, 1)
		b := newAdapter(r.lib, movieCaps)
		r.over(setOf(t, map[media.SourceID]*adapter{src: r.ad, other: b}))
		read := gatherer(t, r.scanner)
		r.start(t)
		advance(30 * time.Second)
		booted := unix(time.Now())
		advance(time.Hour)

		r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: r.ad}), nil, nil)
		got := read().lastComplete
		if _, ok := got[other]; ok {
			t.Errorf("the removed source still has a series: %v", got)
		}
		if got[src] != booted {
			t.Errorf("the kept source's series reads %v after the removal, want %v", got[src], booted)
		}

		advance(time.Hour)
		a2 := newAdapter(r.lib, movieCaps)
		started := a2.blockWalk()
		r.scanner.Reload(setOf(t, map[media.SourceID]*adapter{src: a2}), []media.SourceID{src}, nil)
		synctest.Wait()
		walkStarted(t, started)
		if got, want := read().lastComplete[src], unix(time.Now()); got != want {
			t.Errorf("the changed source's restarted loop's series reads %v, want its new start %v", got, want)
		}
	})
}

func TestWholeIntervalFollowsSetSchedule(t *testing.T) {
	r := newRig(t, movieCaps, daily)
	read := gatherer(t, r.scanner)
	if got := read().interval; got != daily.Whole.Seconds() {
		t.Errorf("after New the interval reads %v, want %v", got, daily.Whole.Seconds())
	}
	twice := mediasource.Schedule{Change: time.Minute, Whole: 12 * time.Hour}
	r.scanner.SetSchedule(twice)
	if got := read().interval; got != twice.Whole.Seconds() {
		t.Errorf("after SetSchedule the interval reads %v, want %v", got, twice.Whole.Seconds())
	}
	r.scanner.SetSchedule(mediasource.Schedule{Change: time.Minute})
	if got := read().interval; got != twice.Whole.Seconds() {
		t.Errorf("a refused schedule moved the interval to %v, want %v kept", got, twice.Whole.Seconds())
	}
}
