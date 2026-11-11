package search_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
	"github.com/cplieger/subflux/internal/timing"
)

// hookPersister is the in-memory persister with an insertion hook and a
// switch answering every insertion ErrJobCapacity.
type hookPersister struct {
	*statustest.MemPersister
	onInsert func()
	full     bool
}

func (p *hookPersister) InsertJob(ctx context.Context, j status.Job, l status.JobLimits) (status.Job, bool, error) {
	if p.onInsert != nil {
		p.onInsert()
	}
	if p.full {
		return status.Job{}, false, status.ErrJobCapacity
	}
	return p.MemPersister.InsertJob(ctx, j, l)
}

// hooked has the rig's engines record jobs through a hookPersister.
func (r *rig) hooked() *hookPersister {
	r.t.Helper()
	p := &hookPersister{MemPersister: statustest.NewMemPersister()}
	st, err := status.New(r.t.Context(), p)
	if err != nil {
		r.t.Fatal(err)
	}
	r.st = st
	return p
}

// retire ends sonarr's whole pass with nothing seen, which retires every
// show instance.
func (r *rig) retire() {
	r.t.Helper()
	p, err := r.db.BeginPass(r.t.Context(), "sonarr", statedb.PassScope{})
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.db.FinishPass(r.t.Context(), "sonarr", p, true, mediasource.Cursor{}, nil); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) searchNow(e *search.Engine, inst statedb.InstanceID, idem *status.JobKey) status.Job {
	r.t.Helper()
	j, err := e.SearchNow(r.t.Context(), owner, inst, idem)
	if err != nil {
		r.t.Fatalf("SearchNow: %v", err)
	}
	return j
}

// codeOf is the code search.Codes answers for err, "" for none.
func codeOf(err error) string {
	for _, c := range search.Codes {
		if errors.Is(err, c.Err) {
			return c.Code
		}
	}
	return ""
}

func TestCodesCoverFailureTable(t *testing.T) {
	inRig(t, "default: [{tag: en, variants: [standard]}]", func(t *testing.T, r *rig) {
		p := r.hooked()
		r.provider("p", "en").answer(nil, cand(1, "en", withRef("a")), cand(1, "en", withRef("b")), cand(1, "en", withRef("c")))
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		ctx := t.Context()
		handles := r.candidates(e, inst, "en")
		key := &status.JobKey{Key: "k", Fingerprint: [32]byte{1}}
		if _, err := e.Download(ctx, owner, handles.Candidates[0].Handle, language.Standard, key); err != nil {
			t.Fatal(err)
		}
		reused := &status.JobKey{Key: "k", Fingerprint: [32]byte{2}}
		arms := []struct {
			run  func() error
			name string
		}{
			{name: "Candidates of an unknown instance", run: func() error {
				_, err := e.Candidates(ctx, owner, inst+100, mustTag("en"))
				return err
			}},
			{name: "Candidates of an unconfigured target", run: func() error {
				_, err := e.Candidates(ctx, owner, inst, mustTag("de"))
				return err
			}},
			{name: "Download of an unknown handle", run: func() error {
				_, err := e.Download(ctx, owner, "AAAAAAAAAAAAAAAAAAAAAA", language.Standard, nil)
				return err
			}},
			{name: "Download of an unserved variant", run: func() error {
				_, err := e.Download(ctx, owner, handles.Candidates[1].Handle, language.HI, nil)
				return err
			}},
			{name: "Download with a reused key", run: func() error {
				_, err := e.Download(ctx, owner, handles.Candidates[1].Handle, language.Standard, reused)
				return err
			}},
			{name: "Download by the system owner with a key", run: func() error {
				got, err := e.Candidates(ctx, status.SystemOwner, inst, mustTag("en"))
				if err != nil {
					return err
				}
				_, err = e.Download(ctx, status.SystemOwner, got.Candidates[0].Handle, language.Standard, &status.JobKey{Key: "s"})
				return err
			}},
			{name: "Download past the job records' capacity", run: func() error {
				p.full = true
				defer func() { p.full = false }()
				_, err := e.Download(ctx, owner, handles.Candidates[2].Handle, language.Standard, nil)
				return err
			}},
			{name: "SearchNow of an unknown instance", run: func() error {
				_, err := e.SearchNow(ctx, owner, inst+100, nil)
				return err
			}},
			{name: "SearchNow with a reused key", run: func() error {
				_, err := e.SearchNow(ctx, owner, inst, reused)
				return err
			}},
			{name: "Candidates past the handle capacity", run: func() error {
				search.FillHandles(e)
				_, err := e.Candidates(ctx, owner+5, inst, mustTag("en"))
				return err
			}},
		}
		covered := map[string]bool{}
		for _, arm := range arms {
			err := arm.run()
			code := codeOf(err)
			if err == nil || code == "" {
				t.Errorf("%s = %v, which no entry of Codes answers", arm.name, err)
			}
			covered[code] = true
		}
		for _, c := range search.Codes {
			if !covered[c.Code] {
				t.Errorf("Codes holds %s, which no arm returns", c.Code)
			}
		}
		if len(search.Codes) != 8 {
			t.Errorf("Codes holds %d entries, want search's five and status's three start codes", len(search.Codes))
		}
	})
}

func TestSearchNowSettles(t *testing.T) {
	t.Run("delivered", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			_, inst := r.deliverable()
			e := r.engine()
			defer r.drain(e)()
			j := r.searchNow(e, inst, nil)
			if j.Kind != status.ItemScan {
				t.Errorf("job kind %v, want ItemScan", j.Kind)
			}
			if got := r.settled(j.ID); got.Outcome != status.Succeeded {
				t.Errorf("job %+v, want Succeeded", got)
			}
			if subs := r.subtitles(inst); len(subs) != 1 {
				t.Errorf("subtitles %q, want the delivery", subs)
			}
		})
	})
	t.Run("skipped", func(t *testing.T) {
		inRig(t, "default: [{tag: en}, {tag: fr}]", func(t *testing.T, r *rig) {
			r.provider("p", "en", "fr").answer(nil, cand(1, "en", withRef("a"))).serve("a", nil, srtBytes)
			inst := r.seed(show, nil, ep1)[0]
			r.script(alignedAs(t, timing.Verified))
			e := r.engine()
			defer r.drain(e)()
			j := r.searchNow(e, inst, nil)
			if got := r.settled(j.ID); got.Outcome != status.Partial || got.Cause != "no_candidate" {
				t.Errorf("job %+v, want Partial, no_candidate for the French pair", got)
			}
		})
	})
	t.Run("a run error", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			inst := r.seed(show, nil, ep1)[0]
			e := r.engine()
			search.WrapRun(e, func(search.RunFunc) search.RunFunc {
				return func(context.Context, statedb.Work) error { return errStoreDown }
			})
			defer r.drain(e)()
			j := r.searchNow(e, inst, nil)
			if got := r.settled(j.ID); got.Outcome != status.Failed {
				t.Errorf("job %+v, want Failed", got)
			}
		})
	})
}

func TestSearchNowDuringRunningScanSettles(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, inst := r.deliverable()
		g := newGatedRuns()
		e := r.engine()
		first := true
		search.WrapRun(e, func(run search.RunFunc) search.RunFunc {
			return func(ctx context.Context, w statedb.Work) error {
				if first {
					first = false
					return g.wrap(always)(run)(ctx, w)
				}
				return run(ctx, w)
			}
		})
		defer r.drain(e)()
		r.enqueue(statedb.Scan, inst)
		synctest.Wait()
		j := r.searchNow(e, inst, nil)
		synctest.Wait()
		if got, _, _ := r.st.Get(t.Context(), j.ID); got.State == status.Done {
			t.Fatalf("job %+v settled by the scan that started before it", got)
		}
		start := time.Now()
		close(g.release)
		got := r.settled(j.ID)
		if got.Outcome != status.Succeeded || time.Since(start) >= time.Minute {
			t.Errorf("job %+v after %v, want Succeeded by the next run within a minute", got, time.Since(start))
		}
		if rows := r.due(); len(rows) != 0 {
			t.Errorf("work %+v left, want no other enqueue", rows)
		}
	})
}

func TestSearchNowCancelRecordsFailed(t *testing.T) {
	t.Run("recorded", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p, inst := r.deliverable()
			p.onFetch = func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}
			e := r.engine()
			defer r.drain(e)()
			j := r.searchNow(e, inst, nil)
			time.Sleep(time.Minute)
			synctest.Wait()
			if err := r.st.Cancel(j.ID); err != nil {
				t.Fatal(err)
			}
			if got := r.settled(j.ID); got.Outcome != status.Cancelled {
				t.Errorf("job %+v, want Cancelled", got)
			}
			if got := spellings(r.history(keyOf(t, show, ep1))); !slices.Equal(got, []string{"fetch_failed"}) {
				t.Errorf("rows %q, want one fetch_failed", got)
			}
			if rows := r.due(); len(rows) != 0 {
				t.Errorf("work %+v, want the cancelled run's row acknowledged", rows)
			}
		})
	})
	t.Run("the write failing", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p, inst := r.deliverable()
			p.onFetch = func(ctx context.Context) error {
				if err := r.db.Close(); err != nil {
					t.Error(err)
				}
				<-ctx.Done()
				return ctx.Err()
			}
			e := r.engine()
			stop := r.drain(e)
			j := r.searchNow(e, inst, nil)
			time.Sleep(time.Minute)
			synctest.Wait()
			if err := r.st.Cancel(j.ID); err != nil {
				t.Fatal(err)
			}
			if got := r.settled(j.ID); got.Outcome != status.Cancelled {
				t.Errorf("job %+v, want Cancelled", got)
			}
			_ = stop()
			r.reopen()
			p.onFetch = nil
			r.script(alignedAs(t, timing.Verified))
			defer r.drain(r.engine())()
			time.Sleep(time.Minute)
			synctest.Wait()
			if subs := r.subtitles(inst); len(subs) != 1 {
				t.Errorf("subtitles %q, want the unacknowledged row replayed", subs)
			}
		})
	})
}

func TestSearchNowCancel(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p, inst := r.deliverable()
		release := make(chan struct{})
		var once sync.Once
		p.onFetch = func(ctx context.Context) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		e := r.engine()
		defer r.drain(e)()
		a, b := r.searchNow(e, inst, nil), r.searchNow(e, inst, nil)
		time.Sleep(time.Minute)
		synctest.Wait()
		if err := r.st.Cancel(a.ID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got, _, _ := r.st.Get(t.Context(), a.ID); got.Outcome != status.Cancelled {
			t.Errorf("the cancelled job %+v, want Cancelled", got)
		}
		once.Do(func() { close(release) })
		if got := r.settled(b.ID); got.Outcome != status.Succeeded {
			t.Errorf("the other job %+v, want Succeeded by the run it kept going", got)
		}
		if subs := r.subtitles(inst); len(subs) != 1 {
			t.Errorf("subtitles %q, want the delivery", subs)
		}
	})
}

func TestSearchNowCancelledAtBegin(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p, inst := r.deliverable()
		e := r.engine()
		search.WrapBegin(e, func(inner search.BeginFunc) search.BeginFunc {
			return func(ctx context.Context, job status.Job, stop func()) (status.Job, bool, error) {
				j, replayed, err := inner(ctx, job, stop)
				if err == nil {
					if cerr := r.st.Cancel(j.ID); cerr != nil {
						t.Errorf("Cancel: %v", cerr)
					}
				}
				return j, replayed, err
			}
		})
		j := r.searchNow(e, inst, nil)
		if j.State != status.Done || j.Outcome != status.Cancelled {
			t.Errorf("job %+v, want Cancelled by the Cancel before SearchNow registered it", j)
		}
		if rows := r.due(); len(rows) != 0 {
			t.Errorf("work %+v, want none enqueued for a cancelled job", rows)
		}
		defer r.drain(e)()
		time.Sleep(time.Minute)
		synctest.Wait()
		if p.calls() != 0 {
			t.Errorf("the provider was asked %d times, want no run for a cancelled job", p.calls())
		}
	})
}

func TestSearchNowRetiredInstanceSettles(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.hooked()
		_, inst := r.deliverable()
		e := r.engine()
		defer r.drain(e)()
		p.onInsert = func() { r.retire() }
		j := r.searchNow(e, inst, nil)
		if got := r.settled(j.ID); got.Outcome != status.Failed || got.Cause != "instance_unknown" {
			t.Errorf("job %+v, want Failed, instance_unknown", got)
		}
		p.onInsert = nil
		if _, err := e.SearchNow(t.Context(), owner, inst, nil); !errors.Is(err, search.ErrInstanceUnknown) {
			t.Errorf("SearchNow of the retired instance = %v, want ErrInstanceUnknown", err)
		}
	})
}

func TestSearchNowKeyedReplayAfterRetire(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, inst := r.deliverable()
		key := &status.JobKey{Key: "k", Fingerprint: [32]byte{1}}
		e := r.engine()
		stop := r.drain(e)
		j := r.searchNow(e, inst, key)
		r.settled(j.ID)
		r.retire()
		if got := r.searchNow(e, inst, key); got.ID != j.ID {
			t.Errorf("the resend after a retirement started %s, want %s", got.ID, j.ID)
		}
		_ = stop()
		r.reopen()
		if got := r.searchNow(r.engine(), inst, key); got.ID != j.ID {
			t.Errorf("the resend after a restart started %s, want %s", got.ID, j.ID)
		}
	})
}

func TestSearchNowOnDrainedEngine(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, inst := r.deliverable()
		e := r.engine()
		if err := r.drain(e)(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		j := r.searchNow(e, inst, nil)
		if j.State != status.Done || j.Outcome != status.Interrupted || j.Cause != "settings_changed" {
			t.Errorf("job %+v, want Interrupted, settings_changed at once", j)
		}
		if rows := r.due(); len(rows) != 1 || rows[0].Cause != statedb.Item {
			t.Errorf("work %+v, want the Item row enqueued for the next engine", rows)
		}
	})
}

func TestDrainSettlesWaitingSearchNow(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, inst := r.deliverable()
		e := r.engine()
		j := r.searchNow(e, inst, nil)
		if err := r.drain(e)(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if got := r.settled(j.ID); got.Outcome != status.Interrupted || got.Cause != "settings_changed" {
			t.Errorf("job %+v, want Interrupted, settings_changed", got)
		}
		if rows := r.due(); len(rows) != 1 {
			t.Errorf("work %+v, want the Item row kept", rows)
		}
	})
}

func TestEngineSwapKeepsCooldown(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(&provider.Failure{Kind: provider.RateLimited, Msg: "slow", RetryAfter: time.Hour})
		inst := r.seed(show, nil, ep1)[0]
		old := r.engine()
		r.candidates(old, inst, "en")
		if err := r.drain(old)(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		got := r.candidates(r.engine(), inst, "en")
		if a := answerOf(t, got, "p"); a.Answer != search.Paused || p.calls() != 1 {
			t.Errorf("the next engine answered %+v after %d requests, want the old engine's pause kept", a, p.calls())
		}
	})
}
