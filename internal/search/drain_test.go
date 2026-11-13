package search_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

// drain runs e's Drain in the bubble; stop ends it and returns its error.
func (r *rig) drain(e *search.Engine) (stop func() error) { return drainUnder(r.t.Context(), e) }

// drainUnder is drain under ctx, such as one carrying a served provider's
// dial.
func drainUnder(ctx context.Context, e *search.Engine) (stop func() error) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- e.Drain(ctx) }()
	var once sync.Once
	var err error
	return func() error {
		once.Do(func() {
			cancel()
			err = <-done
		})
		return err
	}
}

func (r *rig) enqueue(cause statedb.WorkCause, ids ...statedb.InstanceID) {
	r.t.Helper()
	if err := r.db.EnqueueWork(r.t.Context(), ids, cause); err != nil {
		r.t.Fatal(err)
	}
}

// due is every work row due now.
func (r *rig) due() []statedb.Work {
	r.t.Helper()
	rows, err := r.db.NextWork(r.t.Context(), 1000)
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

// rootGone moves the rig's media root away, so it is unavailable.
func (r *rig) rootGone() {
	r.t.Helper()
	if err := os.Rename(r.dir, r.dir+".gone"); err != nil {
		r.t.Fatal(err)
	}
	r.rootsChanged()
}

func (r *rig) rootBack() {
	r.t.Helper()
	if err := os.Rename(r.dir+".gone", r.dir); err != nil {
		r.t.Fatal(err)
	}
	r.rootsChanged()
}

// deliverable seeds episode 1 with a provider whose one candidate aligns
// verified.
func (r *rig) deliverable() (*scripted, statedb.InstanceID) {
	r.t.Helper()
	p := r.provider("p", "en").answer(nil, cand(1, "en")).serve("ref-en-01", nil, srtBytes)
	inst := r.seed(show, nil, ep1)[0]
	r.script(alignedAs(r.t, timing.Verified))
	return p, inst
}

// atLevel is how many records r's engines logged at level with msg.
func (r *rig) atLevel(level slog.Level, msg string) int {
	n := 0
	for _, rec := range r.logs.Records() {
		if rec.Level == level && rec.Message == msg {
			n++
		}
	}
	return n
}

func TestDeferredWorkSurvivesRestart(t *testing.T) {
	deferredReplays(t, (*rig).reopen)
}

func TestDeferredWorkSurvivesSwap(t *testing.T) {
	deferredReplays(t, func(*rig) {})
}

// deferredReplays defers a run on an unavailable root, ends its engine,
// restarts as given, and checks the next engine runs it once its
// not-before passes, never before.
func deferredReplays(t *testing.T, restart func(*rig)) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p, inst := r.deliverable()
		r.rootGone()
		stop := r.drain(r.engine())
		r.enqueue(statedb.Scan, inst)
		synctest.Wait()
		if n := r.atLevel(slog.LevelInfo, "search: run deferred"); n != 1 {
			t.Fatalf("%d deferrals logged, want 1", n)
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Drain = %v, want context.Canceled", err)
		}
		r.rootBack()
		restart(r)
		stop = r.drain(r.engine())
		defer stop()
		time.Sleep(4 * time.Minute)
		synctest.Wait()
		if p.calls() != 0 {
			t.Errorf("the deferred row ran %d times before its not-before", p.calls())
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if subs := r.subtitles(inst); len(subs) != 1 {
			t.Errorf("subtitles %q after the not-before, want the delivery", subs)
		}
	})
}

func TestDeferCapAcrossRestart(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, inst := r.deliverable()
		r.rootGone()
		stop := r.drain(r.engine())
		r.enqueue(statedb.Scan, inst)
		time.Sleep(12 * time.Hour)
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Drain = %v", err)
		}
		r.reopen()
		stop = r.drain(r.engine())
		time.Sleep(11*time.Hour + 50*time.Minute)
		synctest.Wait()
		if n := r.atLevel(slog.LevelInfo, "search: run given up, deferred since its first deferral"); n != 0 {
			t.Fatalf("given up %d times before 24 hours", n)
		}
		time.Sleep(20 * time.Minute)
		synctest.Wait()
		if n := r.atLevel(slog.LevelInfo, "search: run given up, deferred since its first deferral"); n != 1 {
			t.Errorf("given up %d times, want once at INFO", n)
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Drain = %v", err)
		}
		time.Sleep(10 * time.Minute)
		if rows := r.due(); len(rows) != 0 {
			t.Errorf("work %+v left, want the row acknowledged", rows)
		}
	})
}

// failing wraps an engine's runs: each run's start is recorded, and a run
// starting within the first d of start fails with err instead of running.
type failing struct {
	start  time.Time
	err    error
	starts []time.Duration
	d      time.Duration
	mu     sync.Mutex
}

func (f *failing) wrap(run search.RunFunc) search.RunFunc {
	return func(ctx context.Context, w statedb.Work) error {
		f.mu.Lock()
		at := time.Since(f.start)
		f.starts = append(f.starts, at)
		f.mu.Unlock()
		if at < f.d {
			return f.err
		}
		return run(ctx, w)
	}
}

func (f *failing) runs() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.starts)
}

var errStoreDown = errors.New("statedb: commit failed: disk unavailable")

func TestRunStoreErrorBacksOff(t *testing.T) {
	short := func(t *testing.T, d time.Duration, want []time.Duration) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			_, inst := r.deliverable()
			f := &failing{start: time.Now(), d: d, err: errStoreDown}
			e := r.engine()
			search.WrapRun(e, f.wrap)
			defer r.drain(e)()
			r.enqueue(statedb.Scan, inst)
			time.Sleep(time.Minute)
			synctest.Wait()
			if got := f.runs(); !slices.Equal(got, want) {
				t.Errorf("runs started at %v, want %v", got, want)
			}
			if subs := r.subtitles(inst); len(subs) != 1 {
				t.Errorf("subtitles %q, want the delivery once the store recovered", subs)
			}
		})
	}
	t.Run("500 ms", func(t *testing.T) { short(t, 500*time.Millisecond, []time.Duration{0, time.Second}) })
	t.Run("4 s", func(t *testing.T) {
		short(t, 4*time.Second, []time.Duration{0, time.Second, 3 * time.Second, 7 * time.Second})
	})
	long := func(t *testing.T, restartAt time.Duration) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			_, inst := r.deliverable()
			f := &failing{start: time.Now(), d: 10 * time.Minute, err: errStoreDown}
			e := r.engine()
			search.WrapRun(e, f.wrap)
			stop := r.drain(e)
			r.enqueue(statedb.Scan, inst)
			if restartAt > 0 {
				time.Sleep(restartAt)
				if err := stop(); !errors.Is(err, context.Canceled) {
					t.Fatalf("Drain = %v", err)
				}
				r.reopen()
				e = r.engine()
				search.WrapRun(e, f.wrap)
				stop = r.drain(e)
			}
			defer stop()
			time.Sleep(10*time.Minute - restartAt - time.Second)
			synctest.Wait()
			if rows := r.due(); len(rows) != 1 {
				t.Fatalf("work %+v, want the row never acknowledged", rows)
			}
			runs := f.runs()
			for i := 1; i < len(runs); i++ {
				if gap := runs[i] - runs[i-1]; gap > search.StoreRetryMax {
					t.Errorf("runs %v: a wait of %v, past the cap", runs, gap)
				}
			}
			if restartAt > 0 {
				i := slices.IndexFunc(runs, func(at time.Duration) bool { return at >= restartAt })
				if i < 0 || i+2 >= len(runs) || runs[i] != restartAt || runs[i+1]-runs[i] != time.Second || runs[i+2]-runs[i+1] != 2*time.Second {
					t.Errorf("runs %v, want the restarted engine to run at once, then wait 1 s and 2 s", runs)
				}
			} else if n := len(runs); n < 3 || runs[n-1]-runs[n-2] != search.StoreRetryMax {
				t.Errorf("runs %v, want the waits capped at a minute", runs)
			}
			engines := 1
			if restartAt > 0 {
				engines = 2
			}
			if got := r.atLevel(slog.LevelError, "search: run failed"); got != engines {
				t.Errorf("%d ERROR lines, want one per engine's streak", got)
			}
			if got, want := r.atLevel(slog.LevelWarn, "search: run failed again"), len(runs)-engines; got != want {
				t.Errorf("%d WARN lines, want %d", got, want)
			}
			time.Sleep(2 * time.Minute)
			synctest.Wait()
			if subs := r.subtitles(inst); len(subs) != 1 {
				t.Errorf("subtitles %q, want the delivery once the store recovered", subs)
			}
		})
	}
	t.Run("10 minutes", func(t *testing.T) { long(t, 0) })
	t.Run("10 minutes with a restart", func(t *testing.T) { long(t, 5*time.Minute) })
}

// scriptedRuns answers each run with the next error, nil running it.
type scriptedRuns struct {
	errs []error
	n    int
	mu   sync.Mutex
}

func (s *scriptedRuns) wrap(run search.RunFunc) search.RunFunc {
	return func(ctx context.Context, w statedb.Work) error {
		s.mu.Lock()
		var err error
		if s.n < len(s.errs) {
			err = s.errs[s.n]
		}
		s.n++
		s.mu.Unlock()
		if err != nil {
			return err
		}
		return run(ctx, w)
	}
}

func (s *scriptedRuns) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func TestCorruptRowAcknowledgedAfterThree(t *testing.T) {
	const acked = "search: work acknowledged, its instance's stored record does not decode"
	corrupt := fmt.Errorf("%w: instance record", statedb.ErrCorrupt)
	t.Run("at the third", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			inst := r.seed(show, nil, ep1)[0]
			s := &scriptedRuns{errs: []error{corrupt, corrupt, corrupt, corrupt}}
			e := r.engine()
			search.WrapRun(e, s.wrap)
			defer r.drain(e)()
			r.enqueue(statedb.Scan, inst)
			time.Sleep(2 * time.Second)
			synctest.Wait()
			if rows := r.due(); s.count() != 2 || len(rows) != 1 {
				t.Fatalf("%d runs, work %+v; want two failures and the row kept", s.count(), rows)
			}
			time.Sleep(2 * time.Second)
			synctest.Wait()
			if rows := r.due(); s.count() != 3 || len(rows) != 0 {
				t.Errorf("%d runs, work %+v; want the row acknowledged at the third", s.count(), rows)
			}
			if r.atLevel(slog.LevelError, acked) != 1 || !r.logs.AttrContains(acked, "instance", fmt.Sprint(inst)) {
				t.Errorf("logs %v, want one ERROR naming the instance", r.logs.Records())
			}
		})
	})
	t.Run("a success restarts the count", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			inst := r.seed(show, nil, ep1)[0]
			s := &scriptedRuns{errs: []error{corrupt, corrupt, nil, corrupt, corrupt, corrupt}}
			e := r.engine()
			search.WrapRun(e, s.wrap)
			defer r.drain(e)()
			r.enqueue(statedb.Scan, inst)
			time.Sleep(4 * time.Second)
			synctest.Wait()
			if s.count() != 3 || len(r.due()) != 0 {
				t.Fatalf("%d runs, want the third to succeed and acknowledge", s.count())
			}
			r.enqueue(statedb.Scan, inst)
			time.Sleep(2 * time.Second)
			synctest.Wait()
			if rows := r.due(); s.count() != 5 || len(rows) != 1 {
				t.Fatalf("%d runs, work %+v; want two more failures and the row kept", s.count(), rows)
			}
			time.Sleep(2 * time.Second)
			synctest.Wait()
			if rows := r.due(); s.count() != 6 || len(rows) != 0 || r.atLevel(slog.LevelError, acked) != 1 {
				t.Errorf("%d runs, work %+v; want the row acknowledged at the third consecutive failure", s.count(), rows)
			}
		})
	})
	t.Run("no other store error acknowledges", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			inst := r.seed(show, nil, ep1)[0]
			s := &scriptedRuns{errs: []error{corrupt, corrupt, errStoreDown, corrupt, corrupt}}
			e := r.engine()
			search.WrapRun(e, s.wrap)
			defer r.drain(e)()
			r.enqueue(statedb.Scan, inst)
			time.Sleep(40 * time.Second)
			synctest.Wait()
			if rows := r.due(); s.count() != 6 || len(rows) != 0 {
				t.Errorf("%d runs, work %+v; want five failures kept, the run after them acknowledged", s.count(), rows)
			}
			if r.atLevel(slog.LevelError, acked) != 0 {
				t.Error("a broken streak acknowledged the row")
			}
		})
	})
}

func TestDrainedFetchRecordsFailed(t *testing.T) {
	t.Run("recorded", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p, inst := r.deliverable()
			p.onFetch = func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}
			stop := r.drain(r.engine())
			r.enqueue(statedb.Scan, inst)
			time.Sleep(time.Minute)
			synctest.Wait()
			if err := stop(); !errors.Is(err, context.Canceled) {
				t.Fatalf("Drain = %v", err)
			}
			if got := spellings(r.history(keyOf(t, show, ep1))); !slices.Equal(got, []string{"fetch_failed"}) {
				t.Errorf("rows %q, want one fetch_failed", got)
			}
			if rows := r.due(); len(rows) != 1 {
				t.Errorf("work %+v, want the row kept for the next engine", rows)
			}
		})
	})
	t.Run("a failing write replays", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p, inst := r.deliverable()
			p.onFetch = func(ctx context.Context) error {
				if err := r.db.Close(); err != nil {
					t.Error(err)
				}
				<-ctx.Done()
				return ctx.Err()
			}
			stop := r.drain(r.engine())
			r.enqueue(statedb.Scan, inst)
			time.Sleep(time.Minute)
			synctest.Wait()
			if err := stop(); !errors.Is(err, context.Canceled) {
				t.Fatalf("Drain = %v", err)
			}
			r.reopen()
			p.onFetch = nil
			r.script(alignedAs(t, timing.Verified))
			defer r.drain(r.engine())()
			time.Sleep(time.Minute)
			synctest.Wait()
			if subs := r.subtitles(inst); len(subs) != 1 {
				t.Errorf("subtitles %q, want the next engine to replay the row and deliver", subs)
			}
		})
	})
}

// gatedRuns blocks every run until release is closed, counting runs and
// the most in flight at once for one instance.
type gatedRuns struct {
	release chan struct{}
	active  map[statedb.InstanceID]int
	started []statedb.Work
	most    int
	mu      sync.Mutex
}

func newGatedRuns() *gatedRuns {
	return &gatedRuns{release: make(chan struct{}), active: map[statedb.InstanceID]int{}}
}

func (g *gatedRuns) wrap(block func(statedb.Work) bool) func(search.RunFunc) search.RunFunc {
	return func(search.RunFunc) search.RunFunc {
		return func(ctx context.Context, w statedb.Work) error {
			g.mu.Lock()
			g.started = append(g.started, w)
			g.active[w.Inst]++
			g.most = max(g.most, g.active[w.Inst])
			g.mu.Unlock()
			if block(w) {
				<-g.release
			}
			g.mu.Lock()
			g.active[w.Inst]--
			g.mu.Unlock()
			return nil
		}
	}
}

func (g *gatedRuns) runs() []statedb.Work {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.started)
}

func always(statedb.Work) bool { return true }

// instances seeds n episodes of show, each its own instance.
func (r *rig) instances(n int) []statedb.InstanceID {
	r.t.Helper()
	items := make([]mediasource.Item, 0, n)
	for i := 1; i <= n; i++ {
		items = append(items, episode(i))
	}
	return r.seed(show, nil, items...)
}

// TestStaleReadNeverRestartsEndedRun has a run end, its row acknowledged,
// while a poll's read still lists that row: the poll must not run it again.
func TestStaleReadNeverRestartsEndedRun(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		ids := r.instances(2)
		first, other := ids[0], ids[1]
		g := newGatedRuns()
		e := r.engine()
		search.WrapRun(e, g.wrap(func(w statedb.Work) bool { return w.Inst == first }))
		var armed atomic.Bool
		search.WrapNext(e, func(next search.NextFunc) search.NextFunc {
			return func(ctx context.Context, limit int) ([]statedb.Work, error) {
				rows, err := next(ctx, limit)
				if armed.CompareAndSwap(true, false) {
					close(g.release)
					// In the bubble this returns once the released run has
					// acknowledged its row and left.
					time.Sleep(time.Millisecond)
				}
				return rows, err
			}
		})
		stop := r.drain(e)
		defer stop()
		r.enqueue(statedb.Scan, first)
		synctest.Wait()
		armed.Store(true)
		r.enqueue(statedb.Scan, other)
		time.Sleep(time.Second)
		synctest.Wait()
		var runs []statedb.InstanceID
		for _, w := range g.runs() {
			runs = append(runs, w.Inst)
		}
		if slices.Sort(runs); !slices.Equal(runs, []statedb.InstanceID{first, other}) {
			t.Errorf("runs of %v, want each instance run once", runs)
		}
		if rows := r.due(); len(rows) != 0 {
			t.Errorf("work %+v left, want both rows acknowledged", rows)
		}
	})
}

func TestReenqueueDuringRunPolledAgain(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		inst := r.seed(show, nil, ep1)[0]
		g := newGatedRuns()
		first := true
		e := r.engine()
		search.WrapRun(e, g.wrap(func(statedb.Work) bool {
			b := first
			first = false
			return b
		}))
		defer r.drain(e)()
		r.enqueue(statedb.Scan, inst)
		synctest.Wait()
		r.enqueue(statedb.Scan, inst)
		synctest.Wait()
		if n := len(g.runs()); n != 1 {
			t.Fatalf("%d runs while the first is in flight, want 1", n)
		}
		close(g.release)
		synctest.Wait()
		if runs := g.runs(); len(runs) != 2 || runs[1].Gen != 2 {
			t.Errorf("runs %+v, want the re-enqueued row started at once, no clock passing", runs)
		}
	})
}

func TestItemRowNotBehindFullRunLimit(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		ids := r.instances(search.RunsInFlight + 20 + 1)
		g := newGatedRuns()
		e := r.engine()
		search.WrapRun(e, g.wrap(func(w statedb.Work) bool { return w.Cause == statedb.Scan }))
		stop := r.drain(e)
		defer func() {
			close(g.release)
			_ = stop()
		}()
		r.enqueue(statedb.Scan, ids[:len(ids)-1]...)
		synctest.Wait()
		if n := len(g.runs()); n != search.RunsInFlight {
			t.Fatalf("%d scans started, want %d", n, search.RunsInFlight)
		}
		r.enqueue(statedb.Item, ids[len(ids)-1])
		synctest.Wait()
		runs := g.runs()
		if len(runs) != search.RunsInFlight+1 || runs[len(runs)-1].Inst != ids[len(ids)-1] {
			t.Errorf("runs %+v, want the Item row started beside the four blocked scans", runs)
		}
	})
}

func TestDrainerNeverBlocksOnFullGroup(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		ids := r.instances(search.ItemRunsInFlight + 3)
		g := newGatedRuns()
		e := r.engine()
		search.WrapRun(e, g.wrap(func(w statedb.Work) bool { return w.Cause == statedb.Item }))
		stop := r.drain(e)
		r.enqueue(statedb.Item, ids[:search.ItemRunsInFlight+2]...)
		synctest.Wait()
		r.enqueue(statedb.Scan, ids[len(ids)-1])
		synctest.Wait()
		runs := g.runs()
		if len(runs) != search.ItemRunsInFlight+1 || runs[len(runs)-1].Inst != ids[len(ids)-1] {
			t.Errorf("runs %+v, want the scan started while the Item group is full", runs)
		}
		close(g.release)
		synctest.Wait()
		if n := len(g.runs()); n != len(ids) {
			t.Errorf("%d runs after the release, want every row run", n)
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Errorf("Drain = %v", err)
		}
	})
}

func TestOneRunPerInstance(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		inst := r.seed(show, nil, ep1)[0]
		g := newGatedRuns()
		e := r.engine()
		search.WrapRun(e, g.wrap(always))
		defer r.drain(e)()
		r.enqueue(statedb.Scan, inst)
		for range 3 {
			synctest.Wait()
			r.enqueue(statedb.Item, inst)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		close(g.release)
		synctest.Wait()
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.most != 1 || len(g.started) != 2 {
			t.Errorf("%d runs, at most %d at once; want 2 runs, one at a time", len(g.started), g.most)
		}
	})
}

func TestNextWorkErrorBacksOff(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, inst := r.deliverable()
		start := time.Now()
		var (
			mu    sync.Mutex
			polls []time.Duration
		)
		e := r.engine()
		search.WrapNext(e, func(next search.NextFunc) search.NextFunc {
			return func(ctx context.Context, limit int) ([]statedb.Work, error) {
				mu.Lock()
				at := time.Since(start)
				polls = append(polls, at)
				mu.Unlock()
				if at < 100*time.Second {
					return nil, errStoreDown
				}
				return next(ctx, limit)
			}
		})
		r.enqueue(statedb.Scan, inst)
		defer r.drain(e)()
		time.Sleep(31 * time.Second)
		synctest.Wait()
		mu.Lock()
		got := slices.Clone(polls)
		mu.Unlock()
		want := []time.Duration{0, 0, 2 * time.Second, 6 * time.Second, 14 * time.Second, 30 * time.Second}
		if !slices.Equal(got, want) {
			t.Errorf("polls at %v, want %v: the stored wake, then 1 s doubling", got, want)
		}
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		if n := r.atLevel(slog.LevelWarn, "search: reading the work queue failed"); n != 1 {
			t.Errorf("%d WARN lines, want one for the streak", n)
		}
		if subs := r.subtitles(inst); len(subs) != 1 {
			t.Errorf("subtitles %q, want the row run once the queue reads again", subs)
		}
	})
}

// TestDrainClosesAdapters: the drain closes each provider's adapter once,
// after the search it ended has returned.
func TestDrainClosesAdapters(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p, inst := r.deliverable()
		var entered atomic.Bool
		p.onSearch = func(ctx context.Context) error {
			entered.Store(true)
			<-ctx.Done()
			time.Sleep(time.Second)
			return ctx.Err()
		}
		stop := r.drain(r.engine())
		r.enqueue(statedb.Scan, inst)
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := len(p.closed()); n != 0 || !entered.Load() {
			t.Fatalf("%d closes before the drain ended, a search running %v; want none and one", n, entered.Load())
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Drain = %v", err)
		}
		if c := p.closed(); !slices.Equal(c, []int{0}) {
			t.Errorf("closes with searches running %v, want one close after the search returned", c)
		}
	})
}

// TestDrainClosesUnderOneDeadline: every adapter is closed at once under
// one close bound, so the drain ends one bound after it began however many
// providers it holds, a slow adapter beside a hanging one still closes in
// time, and each overrun is logged.
func TestDrainClosesUnderOneDeadline(t *testing.T) {
	const overran = "search: a provider's goroutines outlived its close"
	hang := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	closes := func(t *testing.T, r *rig) time.Duration {
		t.Helper()
		stop := r.drain(r.engine())
		synctest.Wait()
		start := time.Now()
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Drain = %v", err)
		}
		return time.Since(start)
	}
	t.Run("every adapter hanging", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			r.provider("p", "en").onClose = hang
			r.provider("q", "en").onClose = hang
			r.provider("s", "en").onClose = hang
			if took := closes(t, r); took != search.CloseTimeout {
				t.Errorf("the drain closed three hanging adapters in %v, want one bound of %v", took, search.CloseTimeout)
			}
			if n := r.atLevel(slog.LevelWarn, overran); n != 3 {
				t.Errorf("%d overruns logged, want 3", n)
			}
		})
	})
	t.Run("a slow adapter beside a hanging one", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			r.provider("p", "en").onClose = hang
			r.provider("q", "en").onClose = func(ctx context.Context) error {
				select {
				case <-time.After(time.Second):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if took := closes(t, r); took != search.CloseTimeout {
				t.Errorf("the drain closed a hanging and a 1 s adapter in %v, want one bound of %v", took, search.CloseTimeout)
			}
			if n := r.atLevel(slog.LevelWarn, overran); n != 1 {
				t.Errorf("%d overruns logged, want 1: the slow adapter's close fits the bound", n)
			}
			if !r.logs.AttrContains(overran, "provider", "p") {
				t.Errorf("logs %v, want the overrun to name the hanging provider p", r.logs.Records())
			}
		})
	})
}

func TestDrainTwiceRefused(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		e := r.engine()
		stop := r.drain(e)
		synctest.Wait()
		if err := e.Drain(t.Context()); err == nil {
			t.Error("a second Drain answered nil")
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Errorf("Drain = %v", err)
		}
	})
}

func TestSwapReleasesBindings(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p, inst := r.deliverable()
		e := r.engine()
		h := handleOf(t, r.candidates(e, inst, "en"))
		if err := r.drain(e)(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Drain = %v", err)
		}
		if _, err := e.Download(t.Context(), owner, h, language.Standard, nil); !errors.Is(err, search.ErrCandidateUnknown) {
			t.Errorf("a drained engine's handle = %v, want ErrCandidateUnknown", err)
		}
		time.Sleep(time.Hour)
		asked := p.calls()
		if err := search.RunFor(t.Context(), e, statedb.Work{Inst: inst, Gen: 1, Cause: statedb.Item}); err != nil {
			t.Fatalf("run: %v", err)
		}
		if p.calls() != asked {
			t.Errorf("a drained engine asked the provider %d more times", p.calls()-asked)
		}
		next := r.engine()
		if got := r.candidates(next, inst, "en"); len(got.Candidates) != 1 {
			t.Errorf("the next engine's search = %+v, want its own binding to answer", got)
		}
	})
}
