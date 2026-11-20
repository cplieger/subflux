package status_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/go/httpwire/httpwiretest"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

// codeArm is one way a job call refuses, and the code api answers for it.
type codeArm struct {
	call func() error
	name string
	want string
}

// codeOf is the code of the first entry of owners' Codes that err matches by
// errors.Is, "" when none does: the lookup httpwire's answer mapping makes.
func codeOf(err error, owners ...*httpwire.Owner) string {
	for _, o := range owners {
		for _, c := range o.Codes {
			if errors.Is(err, c.Err) {
				return c.Code
			}
		}
	}
	return ""
}

// startArms reach every refusal of Keyed and Begin.
func startArms(t *testing.T) []codeArm {
	t.Helper()
	keyed := open(t, statustest.NewMemPersister())
	k := status.JobKey{Key: "K", Fingerprint: [32]byte{1}}
	begin(t, keyed, 7, &k, nil)

	full := open(t, statustest.NewMemPersister())
	for range status.DefaultLimits.PerOwner {
		begin(t, full, 7, nil, nil)
	}

	h := newHook()
	h.insert = func(context.Context, status.Job) error { return errors.New("bolt: closed") }
	failing := open(t, h)

	return []codeArm{
		{func() error {
			_, _, err := keyed.Keyed(t.Context(), 7, status.JobKey{Key: "K", Fingerprint: [32]byte{2}})
			return err
		}, "Keyed with a used key and another fingerprint", "idempotency_key_reused"},
		{func() error {
			_, _, err := full.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: 7}, nil)
			return err
		}, "Begin past the owner's unfinished records", "job_capacity"},
		{func() error {
			_, _, err := failing.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: 7}, nil)
			return err
		}, "Begin whose insert fails", "job_unrecorded"},
	}
}

func TestCodesCoverFailureTable(t *testing.T) {
	s := open(t, statustest.NewMemPersister())
	done := begin(t, s, 7, nil, func() {})
	finish(s, done.ID, status.Succeeded)
	arms := append(startArms(t),
		codeArm{func() error { return s.Cancel("00000000000000000000000000000000") }, "Cancel of an unknown id", "job_unknown"},
		codeArm{func() error { return s.Cancel(done.ID) }, "Cancel of a Done job", "job_not_cancellable"},
	)
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			if got := codeOf(arm.call(), status.Owner); got != arm.want {
				t.Errorf("code %q, want %q", got, arm.want)
			}
		})
	}
}

func TestOwnerSentinels(t *testing.T) {
	httpwiretest.Sentinels(t, status.Owner)
}
