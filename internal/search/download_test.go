package search_test

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// handleOf is the handle of got's first candidate.
func handleOf(t *testing.T, got search.Results) search.Handle {
	t.Helper()
	if len(got.Candidates) == 0 {
		t.Fatalf("no candidate in %+v", got)
	}
	return got.Candidates[0].Handle
}

// settled is job id once it is Done, the bubble's clock moving a second
// at a time for at most a minute.
func (r *rig) settled(id status.JobID) status.Job {
	r.t.Helper()
	var j status.Job
	for range 60 {
		synctest.Wait()
		var (
			ok  bool
			err error
		)
		if j, ok, err = r.st.Get(r.t.Context(), id); err != nil || !ok {
			r.t.Fatalf("Get(%s) = %v, %v", id, ok, err)
		}
		if j.State == status.Done {
			return j
		}
		time.Sleep(time.Second)
	}
	return j
}

// downloaded starts e's download of h as variant and returns the settled
// job.
func (r *rig) downloaded(e *search.Engine, h search.Handle, v language.Variant) status.Job {
	r.t.Helper()
	j, err := e.Download(r.t.Context(), owner, h, v, nil)
	if err != nil {
		r.t.Fatalf("Download: %v", err)
	}
	return r.settled(j.ID)
}

// manual seeds episode 1 with one provider whose candidate a fetches srt,
// and searches it.
func (r *rig) manual() (*scripted, statedb.InstanceID, *search.Engine, search.Handle) {
	r.t.Helper()
	p := r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"))).serve("a", nil, srtBytes)
	inst := r.seed(show, nil, ep1)[0]
	e := r.engine()
	return p, inst, e, handleOf(r.t, r.candidates(e, inst, "en"))
}

func TestDownloadTakesHandleOnly(t *testing.T) {
	m, ok := reflect.TypeFor[*search.Engine]().MethodByName("Download")
	if !ok {
		t.Fatal("no Download")
	}
	if reflect.TypeFor[search.Handle]() == reflect.TypeFor[string]() {
		t.Error("a Handle is a plain string")
	}
	for i := range m.Type.NumIn() {
		in := m.Type.In(i)
		for in.Kind() == reflect.Pointer {
			in = in.Elem()
		}
		if in == reflect.TypeFor[url.URL]() {
			t.Errorf("Download's argument %d is a URL", i)
		}
		if in.Kind() == reflect.Struct {
			for f := range in.Fields() {
				if name := f.Name; name == "URL" || name == "Url" {
					t.Errorf("Download's argument %s carries a URL field", in)
				}
			}
		}
	}
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		// Not text: the handle must carry the adapter's bytes untouched.
		const token = "r\xe9f\x00\xff/\u2713?a=1&b=%20"
		p := r.provider("p", "en").answer(nil, cand(1, "en", withRef(token))).serve(token, nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		h := handleOf(t, r.candidates(e, inst, "en"))
		if strings.Contains(string(h), token) {
			t.Errorf("handle %q carries the Ref", h)
		}
		r.script(alignedAs(t, timing.Verified))
		if j := r.downloaded(e, h, language.Standard); j.Outcome != status.Succeeded {
			t.Fatalf("job %+v, want Succeeded", j)
		}
		if got := p.fetches(); !slices.Equal(got, []string{token}) {
			t.Errorf("fetched %q, want exactly the adapter's own Ref %q", got, token)
		}
		rec := r.record(inst)
		if !rec.Locked || !rec.Provenance.Manual || rec.Ordinal == 0 {
			t.Errorf("record %+v, want a locked, numbered, manual delivery", rec)
		}
	})
}

func TestHandleSingleUse(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, _, e, h := r.manual()
		r.script(alignedAs(t, timing.Verified))
		if _, err := e.Download(t.Context(), owner+1, h, language.Standard, nil); !errors.Is(err, search.ErrCandidateUnknown) {
			t.Errorf("another owner's download = %v, want ErrCandidateUnknown", err)
		}
		r.downloaded(e, h, language.Standard)
		if _, err := e.Download(t.Context(), owner, h, language.Standard, nil); !errors.Is(err, search.ErrCandidateUnknown) {
			t.Errorf("a second download = %v, want ErrCandidateUnknown", err)
		}
		if _, err := e.Download(t.Context(), owner, "AAAAAAAAAAAAAAAAAAAAAA", language.Standard, nil); !errors.Is(err, search.ErrCandidateUnknown) {
			t.Errorf("an unknown handle = %v, want ErrCandidateUnknown", err)
		}
	})
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, _, e, h := r.manual()
		time.Sleep(search.HandleTTL)
		if _, err := e.Download(t.Context(), owner, h, language.Standard, nil); !errors.Is(err, search.ErrCandidateUnknown) {
			t.Errorf("an expired handle = %v, want ErrCandidateUnknown", err)
		}
	})
}

func TestHandleRestoredOnUnrecorded(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"))).serve("a", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		got, err := e.Candidates(t.Context(), status.SystemOwner, inst, mustTag("en"))
		if err != nil {
			t.Fatal(err)
		}
		h := handleOf(t, got)
		key := &status.JobKey{Key: "k"}
		if _, err := e.Download(t.Context(), status.SystemOwner, h, language.Standard, key); !errors.Is(err, status.ErrJobUnrecorded) {
			t.Fatalf("Download = %v, want ErrJobUnrecorded", err)
		}
		r.script(alignedAs(t, timing.Verified))
		j, err := e.Download(t.Context(), status.SystemOwner, h, language.Standard, nil)
		if err != nil {
			t.Fatalf("the handle after an unrecorded job = %v, want it given back", err)
		}
		if j = r.settled(j.ID); j.Outcome != status.Succeeded {
			t.Errorf("job %+v, want Succeeded", j)
		}
	})
}

func TestVariantUnserved(t *testing.T) {
	inRig(t, "default: [{tag: en, variants: [standard, hi]}]", func(t *testing.T, r *rig) {
		hiForced := func(c *provider.Candidate) { c.Flags = language.Flags{HI: true, Forced: true} }
		r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"), hiForced)).serve("a", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		h := handleOf(t, r.candidates(e, inst, "en"))
		for _, v := range []language.Variant{language.Standard, language.Forced} {
			if _, err := e.Download(t.Context(), owner, h, v, nil); !errors.Is(err, search.ErrVariantUnserved) {
				t.Errorf("variant %d = %v, want ErrVariantUnserved", v, err)
			}
		}
		r.script(alignedAs(t, timing.Verified))
		if j := r.downloaded(e, h, language.HI); j.Outcome != status.Succeeded {
			t.Errorf("the handle given back downloads HI to %+v, want Succeeded", j)
		}
	})
}

func TestDownloadKeyedReplay(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p, _, e, h := r.manual()
		r.script(alignedAs(t, timing.Verified))
		key := &status.JobKey{Key: "k", Fingerprint: [32]byte{1}}
		var (
			wg   sync.WaitGroup
			ids  [2]status.JobID
			errs [2]error
		)
		for i := range 2 {
			wg.Go(func() {
				j, err := e.Download(t.Context(), owner, h, language.Standard, key)
				ids[i], errs[i] = j.ID, err
			})
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil || ids[0] != ids[1] || ids[0] == "" {
			t.Fatalf("concurrent same-key downloads = %v %v, %v; want one job", ids, errs[0], errs[1])
		}
		r.settled(ids[0])
		swapped := r.engine()
		if j, err := swapped.Download(t.Context(), owner, h, language.Standard, key); err != nil || j.ID != ids[0] {
			t.Errorf("the resend on the next engine = %v, %v; want job %s", j.ID, err, ids[0])
		}
		if got := p.fetches(); len(got) != 1 {
			t.Errorf("fetched %q, want the handle taken once", got)
		}
		for name, k := range map[string]*status.JobKey{"another key": {Key: "other"}, "no key": nil} {
			if _, err := e.Download(t.Context(), owner, h, language.Standard, k); !errors.Is(err, search.ErrCandidateUnknown) {
				t.Errorf("%s = %v, want ErrCandidateUnknown", name, err)
			}
		}
	})
}

func TestDownloadCausesClosed(t *testing.T) {
	cases := []struct {
		arrange func(r *rig, p *scripted)
		name    string
		want    string
		outcome status.Outcome
	}{
		{name: "delivered", outcome: status.Succeeded},
		{name: "different cut", want: "different_cut", outcome: status.Failed, arrange: func(r *rig, _ *scripted) { r.script(refusedCut) }},
		{name: "unreadable", want: "format_unsupported", outcome: status.Failed, arrange: func(r *rig, _ *scripted) { r.script(unreadable) }},
		{name: "fetch gone", want: "provider_failed", outcome: status.Failed, arrange: func(_ *rig, p *scripted) {
			p.serve("a", provider.Fail(provider.NotFound, "gone"), nil)
		}},
		{name: "fetch refused by the funnel", want: "provider_failed", outcome: status.Failed, arrange: func(_ *rig, p *scripted) {
			p.serve("a", &provider.Failure{Kind: provider.RateLimited, Msg: "slow", RetryAfter: time.Hour}, nil)
		}},
		{name: "root unavailable", want: "media_root_unavailable", outcome: status.Failed, arrange: func(r *rig, p *scripted) {
			p.onFetch = func(context.Context) error {
				if err := os.Rename(r.dir, r.dir+".gone"); err != nil {
					return err
				}
				r.rootsChangedFromHook()
				return nil
			}
		}},
		{name: "outside every root", want: "outside_root", outcome: status.Failed, arrange: func(r *rig, p *scripted) {
			p.onFetch = func(context.Context) error {
				other := r.t.TempDir()
				if err := os.WriteFile(filepath.Join(other, ".root"), nil, 0o644); err != nil {
					return err
				}
				r.roots = []string{other}
				r.rootsChangedFromHook()
				return nil
			}
		}},
		{name: "different episode", want: "different_episode", outcome: status.Failed, arrange: func(r *rig, p *scripted) {
			p.serve("a", nil, zipOf(r.t, "Show.S01E01.srt"))
			r.script(refusedCut)
		}},
		{name: "the store refusing the delivery", want: "write_failed", outcome: status.Failed, arrange: func(r *rig, p *scripted) {
			p.onFetch = func(context.Context) error { return r.db.Close() }
		}},
		{name: "drained", want: "settings_changed", outcome: status.Interrupted, arrange: func(_ *rig, p *scripted) {
			p.onFetch = func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}
		}},
	}
	var reached []string
	for _, c := range cases {
		if c.want != "" && !slices.Contains(reached, c.want) {
			reached = append(reached, c.want)
		}
	}
	slices.Sort(reached)
	if want := slices.Sorted(slices.Values(search.DownloadCauses)); !slices.Equal(reached, want) {
		t.Errorf("the cases reach %q, want every one of DownloadCauses %q", reached, want)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				p, _, e, h := r.manual()
				r.script(alignedAs(t, timing.Verified))
				if c.arrange != nil {
					c.arrange(r, p)
				}
				if c.name == "fetch refused by the funnel" {
					first := r.downloaded(e, h, language.Standard)
					if first.Cause != "provider_failed" {
						t.Fatalf("the rate-limited fetch ended %+v", first)
					}
					h = handleOf(t, r.candidates(e, r.seededInst(), "en"))
				}
				var j status.Job
				if c.name == "drained" {
					stop := r.drain(e)
					started, err := e.Download(t.Context(), owner, h, language.Standard, nil)
					if err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					_ = stop()
					j = r.settled(started.ID)
				} else {
					j = r.downloaded(e, h, language.Standard)
				}
				if j.State != status.Done || j.Outcome != c.outcome || j.Cause != c.want {
					t.Errorf("job %+v, want %d with %q", j, c.outcome, c.want)
				}
				if j.Cause != "" && !slices.Contains(search.DownloadCauses, j.Cause) {
					t.Errorf("cause %q outside DownloadCauses", j.Cause)
				}
			})
		})
	}
}

// rootsChangedFromHook has the writer apply the rig's roots from a
// goroutine of the bubble while the test may sit in synctest.Wait: the
// sleep returns once the writer's probe has settled.
func (r *rig) rootsChangedFromHook() {
	r.writer.RootsChanged()
	time.Sleep(time.Second)
}

// seededInst is the rig's one seeded instance.
func (r *rig) seededInst() statedb.InstanceID {
	r.t.Helper()
	row, found, err := r.db.ItemRow(r.t.Context(), keyOf(r.t, show, ep1))
	if err != nil || !found {
		r.t.Fatalf("ItemRow = %v, %v", found, err)
	}
	return row.Inst
}

func TestDownloadDuringDrainNeverCallsGo(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"))).serve("a", nil, srtBytes)
		inst := r.seed(show, nil, ep1)[0]
		for range 20 {
			e := r.engine()
			hs := r.candidates(e, inst, "en")
			stop := r.drain(e)
			var (
				wg   sync.WaitGroup
				jobs []status.JobID
				mu   sync.Mutex
			)
			wg.Go(func() { _ = stop() })
			wg.Go(func() {
				j, err := e.Download(t.Context(), owner, handleOf(t, hs), language.Standard, nil)
				if errors.Is(err, search.ErrCandidateUnknown) {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				jobs = append(jobs, j.ID)
				mu.Unlock()
			})
			wg.Wait()
			for _, id := range jobs {
				if j := r.settled(id); j.State != status.Done {
					t.Errorf("job %+v left running", j)
				}
			}
		}
		e := r.engine()
		h := handleOf(t, r.candidates(e, inst, "en"))
		if err := r.drain(e)(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := e.Download(t.Context(), owner, h, language.Standard, nil); !errors.Is(err, search.ErrCandidateUnknown) {
			t.Errorf("a handle of a drained engine = %v, want ErrCandidateUnknown: its handles went", err)
		}
		draining := r.engine()
		h = handleOf(t, r.candidates(draining, inst, "en"))
		search.BeginDrain(draining)
		j, err := draining.Download(t.Context(), owner, h, language.Standard, nil)
		if err != nil {
			t.Fatal(err)
		}
		if j.State != status.Done || j.Outcome != status.Interrupted || j.Cause != "settings_changed" {
			t.Errorf("a download finding the engine draining answered %+v, want Interrupted, settings_changed", j)
		}
	})
}

func TestManualDownloadDrainRecordsFailed(t *testing.T) {
	for _, failing := range []bool{false, true} {
		name := "recorded"
		if failing {
			name = "the write failing"
		}
		t.Run(name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				p, _, e, h := r.manual()
				p.onFetch = func(ctx context.Context) error {
					if failing {
						if err := r.db.Close(); err != nil {
							t.Error(err)
						}
					}
					<-ctx.Done()
					return ctx.Err()
				}
				stop := r.drain(e)
				j, err := e.Download(t.Context(), owner, h, language.Standard, nil)
				if err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if err := stop(); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if j = r.settled(j.ID); j.Outcome != status.Interrupted || j.Cause != "settings_changed" {
					t.Errorf("job %+v, want Interrupted, settings_changed", j)
				}
				const unrecorded = "search: a drained download's attempt was not recorded"
				if failing {
					if n := r.atLevel(slog.LevelError, unrecorded); n != 1 {
						t.Errorf("%d ERROR lines, want one for the lost row", n)
					}
					return
				}
				if got := spellings(r.history(keyOf(t, show, ep1))); !slices.Equal(got, []string{"fetch_failed"}) {
					t.Errorf("rows %q, want one fetch_failed", got)
				}
			})
		})
	}
}

func TestManualDownloadOutlivesRequest(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		_, inst, e, h := r.manual()
		r.script(alignedAs(t, timing.Verified))
		defer r.drain(e)()
		ctx, cancel := context.WithCancel(t.Context())
		j, err := e.Download(ctx, owner, h, language.Standard, nil)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if j = r.settled(j.ID); j.Outcome != status.Succeeded {
			t.Errorf("job %+v, want Succeeded after its request ended", j)
		}
		if subs := r.subtitles(inst); len(subs) != 1 {
			t.Errorf("subtitles %q, want the delivery", subs)
		}
	})
}

func TestManualDownloadOutsideRunLimit(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"))).serve("a", nil, srtBytes)
		ids := r.instances(search.RunsInFlight + 1)
		g := newGatedRuns()
		e := r.engine()
		search.WrapRun(e, g.wrap(always))
		stop := r.drain(e)
		defer func() {
			close(g.release)
			_ = stop()
		}()
		r.enqueue(statedb.Scan, ids[1:]...)
		synctest.Wait()
		h := handleOf(t, r.candidates(e, ids[0], "en"))
		r.script(alignedAs(t, timing.Verified))
		if j := r.downloaded(e, h, language.Standard); j.Outcome != status.Succeeded {
			t.Errorf("job %+v beside %d blocked scans, want Succeeded", j, search.RunsInFlight)
		}
	})
}

func TestManualPickRunsPlan(t *testing.T) {
	cases := []struct {
		edit func(*provider.Candidate)
		name string
		want timing.Decision
	}{
		{name: "the same release", edit: sameRelease, want: timing.Decision{Action: timing.Skip, Skip: timing.SameRelease}},
		{name: "another release", edit: otherFit, want: timing.Decision{Action: timing.ActionReference}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				r.timing = timing.Config{Sync: true}
				r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"), c.edit)).serve("a", nil, srtBytes)
				inst := r.seed(show, nil, ep1)[0]
				e := r.engine()
				h := handleOf(t, r.candidates(e, inst, "en"))
				rec := r.script(alignedAs(t, timing.Verified))
				r.downloaded(e, h, language.Standard)
				if got := rec.Decisions(); len(got) != 1 || got[0] != c.want {
					t.Errorf("aligned with %+v, want %+v", got, c.want)
				}
			})
		})
	}
}

func TestManualDownloadWaitsForItemSlot(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		r.provider("p", "en").answer(nil, cand(1, "en", withRef("a"))).serve("a", nil, srtBytes)
		ids := r.instances(search.ItemRunsInFlight + 1)
		g := newGatedRuns()
		e := r.engine()
		search.WrapRun(e, g.wrap(always))
		stop := r.drain(e)
		defer func() { _ = stop() }()
		r.enqueue(statedb.Item, ids[1:]...)
		synctest.Wait()
		h := handleOf(t, r.candidates(e, ids[0], "en"))
		r.script(alignedAs(t, timing.Verified))
		j, err := e.Download(t.Context(), owner, h, language.Standard, nil)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got, _, _ := r.st.Get(t.Context(), j.ID); got.State != status.Queued || got.Phase != "queued" {
			t.Errorf("job %+v beside %d Item runs, want it queued for a slot", got, search.ItemRunsInFlight)
		}
		close(g.release)
		if got := r.settled(j.ID); got.Outcome != status.Succeeded {
			t.Errorf("job %+v once a slot freed, want Succeeded", got)
		}
	})
}
