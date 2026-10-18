package status_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

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

	codeOf := func(err error) string {
		i := slices.IndexFunc(status.StartCodes, func(c struct {
			Err  error
			Code string
		},
		) bool {
			return errors.Is(err, c.Err)
		})
		if i < 0 {
			return ""
		}
		return status.StartCodes[i].Code
	}
	reached := map[string]bool{}

	s := open(t, statustest.NewMemPersister())
	k := status.JobKey{Key: "K", Fingerprint: [32]byte{1}}
	begin(t, s, 7, &k, nil)
	_, _, err := s.Keyed(t.Context(), 7, status.JobKey{Key: "K", Fingerprint: [32]byte{2}})
	reached[codeOf(err)] = true

	for range status.DefaultLimits.PerOwner - 1 {
		begin(t, s, 7, nil, nil)
	}
	_, _, err = s.Begin(t.Context(), status.Job{Kind: status.Scan, Owner: 7}, nil)
	reached[codeOf(err)] = true

	h := newHook()
	h.insert = func(context.Context, status.Job) error { return errors.New("bolt: closed") }
	_, _, err = open(t, h).Begin(t.Context(), status.Job{Kind: status.Scan, Owner: 7}, nil)
	reached[codeOf(err)] = true

	for _, code := range want {
		if !reached[code] {
			t.Errorf("no arm reached %q (reached %v)", code, reached)
		}
	}
}
