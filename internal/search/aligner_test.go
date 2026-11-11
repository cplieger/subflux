package search_test

import (
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/searchtest"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

var _ search.Aligner = (*syncsched.Aligner)(nil)

func TestScriptedAlignerDrivesAttempts(t *testing.T) {
	verdict := func(v timing.Verdict) searchtest.Result {
		return searchtest.Result{Aligned: syncsched.Aligned{Outcome: timing.Outcome{Verdict: v}}}
	}
	script := []searchtest.Result{
		verdict(timing.Trusted),
		verdict(timing.Verified),
		verdict(timing.Unverified),
		{Aligned: syncsched.Aligned{Outcome: timing.Outcome{Verdict: timing.Unverified}}, Err: syncsched.ErrSyncFailed},
		{Err: syncsched.ErrDifferentCut},
	}
	deps := search.Deps{Aligner: searchtest.NewAligner(script...)}
	for i, want := range script {
		got, err := deps.Aligner.Align(t.Context(), statedb.InstanceID(1), nil, policy(t).Targets()[0].Tag, timing.Decision{})
		if got.Outcome.Verdict != want.Aligned.Outcome.Verdict || !errors.Is(err, want.Err) || (want.Err == nil) != (err == nil) {
			t.Errorf("call %d = %v, %v; want %v, %v", i, got.Outcome.Verdict, err, want.Aligned.Outcome.Verdict, want.Err)
		}
	}
	if _, err := deps.Aligner.Align(t.Context(), statedb.InstanceID(1), nil, policy(t).Targets()[0].Tag, timing.Decision{}); !errors.Is(err, searchtest.ErrScriptEnded) {
		t.Errorf("a call past the script = %v, want ErrScriptEnded", err)
	}
}

func TestScriptedAlignerAnswersPerMember(t *testing.T) {
	al := searchtest.NewAligner(searchtest.Result{Err: syncsched.ErrDifferentCut})
	al.Member("member B", searchtest.Result{Aligned: syncsched.Aligned{Outcome: timing.Outcome{Confidence: 0.8}}})
	tag := policy(t).Targets()[0].Tag
	ref := timing.Decision{Action: timing.ActionReference}
	b, errB := al.Align(t.Context(), 1, []byte("1\nmember B\n"), tag, ref)
	_, errA := al.Align(t.Context(), 1, []byte("1\nmember A\n"), tag, timing.Decision{Action: timing.Skip, Skip: timing.SameRelease})
	b2, _ := al.Align(t.Context(), 1, []byte("member B again"), tag, ref)
	if b.Outcome.Confidence != 0.8 || errB != nil || b2.Outcome.Confidence != 0.8 {
		t.Errorf("member B answered %v, %v then %v; want its rule both times", b.Outcome, errB, b2.Outcome)
	}
	if !errors.Is(errA, syncsched.ErrDifferentCut) {
		t.Errorf("member A answered %v, want the queue's first result", errA)
	}
	got, plans := al.Payloads(), al.Decisions()
	if len(got) != 3 || string(got[1]) != "1\nmember A\n" || len(plans) != 3 || plans[1].Skip != timing.SameRelease || plans[0] != ref {
		t.Errorf("payloads %q, decisions %+v; want each recorded in order", got, plans)
	}
}
