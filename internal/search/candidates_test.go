package search_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/statedb"
)

const owner = int64(7)

// credentialed adds an enabled scripted provider whose one setting is the
// secret api_key.
func (r *rig) credentialed(id provider.ProviderID, secret string, tags ...string) *scripted {
	s := r.provider(id, tags...)
	e := &r.entries[len(r.entries)-1]
	e.Schema = formschema.Schema{Fields: []formschema.Field{{Key: "api_key", Kind: formschema.Secret, Required: true}}}
	r.cfgs[len(r.cfgs)-1] = providertest.Config(r.t, *e, "api_key: "+secret)
	return s
}

// candidates is e's manual search of inst for target by owner.
func (r *rig) candidates(e *search.Engine, inst statedb.InstanceID, target string) search.Results {
	r.t.Helper()
	got, err := e.Candidates(r.t.Context(), owner, inst, mustTag(target))
	if err != nil {
		r.t.Fatalf("Candidates: %v", err)
	}
	return got
}

// answerOf is got's answer of provider id; it fails the test when there is
// none.
func answerOf(t *testing.T, got search.Results, id provider.ProviderID) search.ProviderAnswer {
	t.Helper()
	for _, a := range got.Providers {
		if a.Provider == id {
			return a
		}
	}
	t.Fatalf("no answer of %s in %+v", id, got.Providers)
	return search.ProviderAnswer{}
}

func TestCandidatesReportsProviderAnswers(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		paused := r.provider("paused", "en").answer(&provider.Failure{Kind: provider.RateLimited, Msg: "slow down", RetryAfter: time.Hour})
		r.credentialed("disabled", "old-key", "en")
		r.credentialed("failing", "s3cret", "en").answer(&provider.Failure{Kind: provider.Transient, Status: 503, Msg: "upstream said s3cret is busy"})
		machine := func(c *provider.Candidate) { c.MachineTranslated = true }
		r.provider("answers", "en").answer(nil, cand(1, "en", withRef("a")), cand(1, "en", withRef("b"), machine))
		r.provider("skips", "en").answer(&provider.Skipped{Cause: "no IMDb id"})
		unmapped := r.provider("unmapped", "fr")
		slow := r.provider("slow", "en")
		slow.onSearch = func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}
		r.provider("timesout", "en").onSearch = func(context.Context) error {
			return fmt.Errorf("site timed out: %w", context.DeadlineExceeded)
		}
		fp := keyenc.Join("old-key")
		if err := r.db.PutCredentialDisable(t.Context(), statedb.CredentialDisable{At: time.Now(), Provider: "disabled", Fingerprint: fp, Reason: "refused"}); err != nil {
			t.Fatal(err)
		}
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		r.candidates(e, inst, "en")
		pausedAt := time.Now()
		got := r.candidates(e, inst, "en")
		ids := make([]provider.ProviderID, 0, len(got.Providers))
		for _, a := range got.Providers {
			ids = append(ids, a.Provider)
		}
		if want := []provider.ProviderID{"answers", "disabled", "failing", "paused", "skips", "slow", "timesout", "unmapped"}; !slices.Equal(ids, want) {
			t.Errorf("answers of %v, want one per enabled provider %v", ids, want)
		}
		if a := answerOf(t, got, "paused"); a.Answer != search.Paused || !a.Until.After(time.Now()) || a.Until.After(pausedAt.Add(time.Hour)) {
			t.Errorf("paused answered %+v, want Paused until its rate pause ends", a)
		}
		if paused.calls() != 1 {
			t.Errorf("the paused provider was asked %d times, want only before its pause", paused.calls())
		}
		if a := answerOf(t, got, "disabled"); a.Answer != search.Disabled {
			t.Errorf("disabled answered %+v, want Disabled", a)
		}
		if a := answerOf(t, got, "failing"); a.Answer != search.Failed || a.Cause == "" || strings.Contains(a.Cause, "s3cret") {
			t.Errorf("failing answered %+v, want Failed with a redacted cause", a)
		}
		if a := answerOf(t, got, "answers"); a.Answer != search.Answered || a.Count != 2 {
			t.Errorf("answers answered %+v, want Answered, 2", a)
		}
		if a := answerOf(t, got, "skips"); a.Answer != search.NotAsked || a.Cause != "no IMDb id" {
			t.Errorf("skips answered %+v, want NotAsked naming what it lacks", a)
		}
		if a := answerOf(t, got, "unmapped"); a.Answer != search.NotAsked || a.Cause == "" || unmapped.calls() != 0 {
			t.Errorf("unmapped answered %+v after %d requests, want NotAsked and none sent", a, unmapped.calls())
		}
		if a := answerOf(t, got, "slow"); a.Answer != search.Failed || a.Cause != "no answer within 30 seconds" {
			t.Errorf("slow answered %+v, want Failed naming the search deadline", a)
		}
		if a := answerOf(t, got, "timesout"); a.Answer != search.Failed || !strings.Contains(a.Cause, "site timed out") {
			t.Errorf("timesout answered %+v, want Failed with its own timeout's cause", a)
		}
		if len(got.Candidates) != 2 {
			t.Fatalf("candidates %+v, want the two answered", got.Candidates)
		}
		machines := 0
		for _, c := range got.Candidates {
			if c.MachineTranslated {
				machines++
			}
		}
		if machines != 1 {
			t.Errorf("%d candidates marked machine-translated, want the one its provider marked", machines)
		}
	})
}

func TestCandidatesBudgetCoversSlotWait(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		queued := r.provider("queued", "en").answer(nil, cand(1, "en"))
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		hold := r.funnel.Bind("queued", "", 0)
		release := make(chan struct{})
		var held sync.WaitGroup
		for range 2 {
			held.Go(func() {
				err := hold.Call(t.Context(), providercalls.Fetch, providercalls.Automatic, func(ctx context.Context) error {
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				if err != nil {
					t.Errorf("holding fetch: %v", err)
				}
			})
		}
		synctest.Wait()
		start := time.Now()
		got := r.candidates(e, inst, "en")
		close(release)
		held.Wait()
		if a := answerOf(t, got, "queued"); a.Answer != search.Failed || a.Cause != "no answer within 30 seconds" {
			t.Errorf("queued answered %+v, want Failed naming the search deadline", a)
		}
		if waited := time.Since(start); waited != 30*time.Second {
			t.Errorf("the search answered after %v, want the 30-second budget", waited)
		}
		if queued.calls() != 0 {
			t.Errorf("the queued provider was asked %d times, want none past the slot wait", queued.calls())
		}
	})
}

func TestAnswerTextClosed(t *testing.T) {
	for a, text := range map[search.Answer]string{search.Answered: "answered", search.Paused: "paused", search.Disabled: "disabled", search.Failed: "failed", search.NotAsked: "not_asked"} {
		b, err := a.MarshalText()
		if err != nil || string(b) != text {
			t.Errorf("%d marshals %q, %v, want %q", a, b, err, text)
		}
		var back search.Answer
		if err := back.UnmarshalText([]byte(text)); err != nil || back != a {
			t.Errorf("%q unmarshals %d, %v", text, back, err)
		}
	}
	if _, err := search.Answer(5).MarshalText(); err == nil {
		t.Error("Answer 5 marshalled")
	}
	var a search.Answer
	if err := a.UnmarshalText([]byte("Answered")); err == nil {
		t.Error("an unlisted spelling unmarshalled")
	}
}

func TestCandidatesRefusals(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en"))
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		if _, err := e.Candidates(t.Context(), owner, inst+100, mustTag("en")); !errors.Is(err, search.ErrInstanceUnknown) {
			t.Errorf("an unknown instance = %v, want ErrInstanceUnknown", err)
		}
		if _, err := e.Candidates(t.Context(), owner, inst, mustTag("de")); !errors.Is(err, search.ErrTargetUnconfigured) {
			t.Errorf("an unconfigured target = %v, want ErrTargetUnconfigured", err)
		}
		if p.calls() != 0 {
			t.Errorf("a refused search asked the provider %d times", p.calls())
		}
	})
}

func TestCandidatesChecks(t *testing.T) {
	inRig(t, "default: [{tag: en, variants: [standard, hi]}]", func(t *testing.T, r *rig) {
		other := func(c *provider.Candidate) { c.Release = "Show.S01E05.1080p.WEB-DL.x264-GRP" }
		forced := func(c *provider.Candidate) { c.Flags = language.Flags{Forced: true} }
		hi := func(c *provider.Candidate) { c.Flags = language.Flags{HI: true} }
		unconfirmed := func(c *provider.Candidate) { c.Unconfirmed, c.Release = true, "Show" }
		r.provider("p", "en", "fr").answer(nil,
			cand(1, "en", withRef("ok"), sameRelease),
			cand(1, "en", withRef("hi"), hi),
			cand(1, "en", withRef("forced"), forced),
			cand(1, "en", withRef("other"), other),
			cand(1, "fr", withRef("fr")),
			cand(1, "en", withRef("unconfirmed"), unconfirmed),
		)
		inst := r.seed(show, nil, ep1)[0]
		got := r.candidates(r.engine(), inst, "en")
		var listed []string
		for _, c := range got.Candidates {
			listed = append(listed, c.Release)
			if c.Handle == "" || len(c.Handle) != 22 {
				t.Errorf("candidate %s has handle %q, want 22 base64url characters", c.Release, c.Handle)
			}
		}
		if len(got.Candidates) != 3 {
			t.Fatalf("listed %q, want the same-release, the HI and the unconfirmed candidates", listed)
		}
		if c := got.Candidates[0]; c.Release != stem(1) || !c.Automatic || c.WhyNot != search.None {
			t.Errorf("first %+v, want the same-release candidate, automatic", c)
		}
		last := got.Candidates[len(got.Candidates)-1]
		if last.Automatic || last.WhyNot != search.IdentityUnconfirmed {
			t.Errorf("last %+v, want the unconfirmed candidate, manual only", last)
		}
	})
}

func TestCandidatesAtMostMax(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		var cands []provider.Candidate
		for i := range search.MaxCandidates + 10 {
			cands = append(cands, cand(1, "en", withRef("r"+two(i%100)+string(rune('a'+i/100)))))
		}
		r.provider("p", "en").answer(nil, cands...)
		inst := r.seed(show, nil, ep1)[0]
		if got := r.candidates(r.engine(), inst, "en"); len(got.Candidates) != search.MaxCandidates {
			t.Errorf("%d candidates, want %d", len(got.Candidates), search.MaxCandidates)
		}
	})
}

func TestCandidateBounds(t *testing.T) {
	t.Run("handles", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			covers := make([]media.Numbering, provider.MaxCovers)
			for i := range covers {
				covers[i] = media.Numbering{Scheme: media.TVDB, Season: 1, Episode: i + 1}
			}
			var cands []provider.Candidate
			for i := range search.MaxCandidates {
				ref := strings.Repeat(string(rune('a'+i%26)), provider.MaxRefToken-2) + two(i)
				cands = append(cands, cand(1, "en", func(c *provider.Candidate) {
					c.Ref, c.Covers = provider.NewRef(ref), covers
					c.Release = "Show.S01E01." + strings.Repeat("x", provider.MaxText-len("Show.S01E01."))
					c.Title = "Show" + strings.Repeat(" ", provider.MaxText-len("Show"))
				}))
			}
			huge := strings.Repeat("y", 10<<20-1)
			oversized := append([]provider.Candidate{
				cand(1, "en", withRef(huge)),
				cand(1, "en", withRef("huge-release"), func(c *provider.Candidate) { c.Release = "Show.S01E01." + huge }),
			}, cands[:10]...)
			r.provider("p", "en").answer(nil, oversized...).answer(nil, cands...)
			inst := r.seed(show, nil, ep1)[0]
			e := r.engine()
			first, err := e.Candidates(t.Context(), 1, inst, mustTag("en"))
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Candidates) != 11 {
				t.Errorf("%d candidates, want the oversized Ref dropped and the others kept", len(first.Candidates))
			}
			for _, c := range first.Candidates {
				if len(c.Release) > provider.MaxText {
					t.Fatalf("a %d-byte release reached a handle", len(c.Release))
				}
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&before)
			held := len(first.Candidates)
			for o := int64(2); held+search.MaxCandidates <= search.MaxHandles; o++ {
				for range search.MaxHandlesPerOwner / search.MaxCandidates {
					if held+search.MaxCandidates > search.MaxHandles {
						break
					}
					got, err := e.Candidates(t.Context(), o, inst, mustTag("en"))
					if err != nil {
						t.Fatalf("search %d: %v", held/search.MaxCandidates, err)
					}
					held += len(got.Candidates)
				}
			}
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(e)
			grown := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			t.Logf("%d handles: %d KiB of live heap", held, grown>>10)
			if grown > 24<<20 {
				t.Errorf("%d handles hold %d MiB of live heap, want under 24", held, grown>>20)
			}
			if held < search.MaxHandles-search.MaxCandidates {
				t.Errorf("only %d handles held", held)
			}
		})
	})
}

func TestCandidatesCapacity(t *testing.T) {
	inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
		p := r.provider("p", "en").answer(nil, cand(1, "en"))
		inst := r.seed(show, nil, ep1)[0]
		e := r.engine()
		search.FillHandles(e)
		if _, err := e.Candidates(t.Context(), owner, inst, mustTag("en")); !errors.Is(err, search.ErrSearchCapacity) {
			t.Errorf("a search with every handle held = %v, want ErrSearchCapacity", err)
		}
		if p.calls() != 0 {
			t.Errorf("a search refused for capacity asked the provider %d times", p.calls())
		}
	})
}
