package timing_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/go/httpwire/httpwiretest"
	"github.com/cplieger/subflux/internal/timing"
)

// The two codes api answers for timing's sentinels.
const (
	invalid     = "timing_invalid"
	unsupported = "format_unsupported"
)

// failureArm is one way an exported call fails, and the code api answers for
// it.
type failureArm struct {
	name string
	call func() error
	want string
}

func failureArms(t testing.TB) []failureArm {
	t.Helper()
	codeFor := map[error]string{timing.ErrTimingInvalid: invalid, timing.ErrFormatUnsupported: unsupported}
	var arms []failureArm
	for _, tc := range invalidTimings {
		arms = append(arms, failureArm{"Validate " + tc.name, func() error { return timing.Validate(tc.t) }, invalid})
	}
	for _, tc := range undecodable {
		arms = append(arms, failureArm{"DecodeTiming " + tc.name, func() error {
			_, err := timing.DecodeTiming(tc.b)
			return err
		}, invalid})
	}
	for _, tc := range refusedDocs(t) {
		arms = append(arms, failureArm{"ParseCues " + tc.name, func() error {
			_, err := timing.ParseCues(tc.b)
			return err
		}, unsupported})
	}
	for _, tc := range renderFailures(t) {
		arms = append(arms, failureArm{"Render " + tc.name, func() error {
			_, err := timing.Render(tc.c, tc.t)
			return err
		}, codeFor[tc.want]})
	}
	for _, tc := range renderVTTFailures(t) {
		arms = append(arms, failureArm{"RenderVTT " + tc.name, tc.call, codeFor[tc.want]})
	}
	for _, tc := range cleanFailures(t) {
		arms = append(arms, failureArm{"Clean " + tc.name, func() error {
			_, err := timing.Clean(tc.c, tc.opts, tc.tag)
			return err
		}, unsupported})
	}
	for _, tc := range undecodableCues(t) {
		arms = append(arms, failureArm{"DecodeCues " + tc.name, func() error {
			_, err := timing.DecodeCues(tc.b)
			return err
		}, unsupported})
	}
	return arms
}

func TestCodesCoverFailureTable(t *testing.T) {
	t.Parallel()
	for _, arm := range failureArms(t) {
		t.Run(arm.name, func(t *testing.T) {
			t.Parallel()
			err := arm.call()
			if err == nil {
				t.Fatal("call succeeded, want a failure")
			}
			i := slices.IndexFunc(timing.Owner.Codes, func(c httpwire.OwnerCode) bool { return errors.Is(err, c.Err) })
			if i < 0 {
				t.Fatalf("error %v is no entry of timing.Owner.Codes", err)
			}
			if got := timing.Owner.Codes[i].Code; got != arm.want {
				t.Errorf("error %v answers code %q, want %q", err, got, arm.want)
			}
		})
	}
}

func TestOwnerSentinels(t *testing.T) {
	t.Parallel()
	httpwiretest.Sentinels(t, timing.Owner)
}
