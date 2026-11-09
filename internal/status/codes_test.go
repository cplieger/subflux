package status_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/sentineltest"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

// codeArm is one way a job call refuses, and the code api answers for it.
type codeArm struct {
	call func() error
	name string
	want string
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

func TestStartCodesAreKeyedAndBeginCodes(t *testing.T) {
	want := map[error]string{
		status.ErrKeyReused:     "idempotency_key_reused",
		status.ErrJobCapacity:   "job_capacity",
		status.ErrJobUnrecorded: "job_unrecorded",
	}
	if len(status.StartCodes) != len(want) {
		t.Errorf("StartCodes holds %d entries, want %d", len(status.StartCodes), len(want))
	}
	for _, c := range status.StartCodes {
		if code, ok := want[c.Err]; !ok || code != c.Code {
			t.Errorf("StartCodes pairs %v with %q; want only %v", c.Err, c.Code, want)
		}
	}
	for _, arm := range startArms(t) {
		t.Run(arm.name, func(t *testing.T) {
			if got := sentineltest.CodeOf(status.StartCodes, arm.call()); got != arm.want {
				t.Errorf("code %q, want %q", got, arm.want)
			}
		})
	}
}

func TestCodesCoverFailureTable(t *testing.T) {
	if n := len(status.StartCodes); len(status.Codes) < n || !slices.Equal(status.Codes[:n], status.StartCodes) {
		t.Errorf("Codes %v does not begin with StartCodes %v", status.Codes, status.StartCodes)
	}

	s := open(t, statustest.NewMemPersister())
	done := begin(t, s, 7, nil, func() {})
	finish(s, done.ID, status.Succeeded)
	arms := append(startArms(t),
		codeArm{func() error { return s.Cancel("00000000000000000000000000000000") }, "Cancel of an unknown id", "job_unknown"},
		codeArm{func() error { return s.Cancel(done.ID) }, "Cancel of a Done job", "job_not_cancellable"},
	)
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			if got := sentineltest.CodeOf(status.Codes, arm.call()); got != arm.want {
				t.Errorf("code %q, want %q", got, arm.want)
			}
		})
	}

	sentineltest.Placed(t, status.Codes, map[string]error{
		"ErrJobUnrecorded":  status.ErrJobUnrecorded,
		"ErrKeyReused":      status.ErrKeyReused,
		"ErrJobCapacity":    status.ErrJobCapacity,
		"ErrNotCancellable": status.ErrNotCancellable,
		"ErrUnknownJob":     status.ErrUnknownJob,
	}, nil)
}
