package syncsched_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

func TestReferenceAlignmentMemory(t *testing.T) {
	r := newRig(t)
	video := episode(7, 10_000)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	payload := srt(shifted(video, -2_000))
	var got syncsched.Aligned
	var err error
	n := held(func() { got, err = r.align(t, payload, byReference) })
	if err != nil || got.Timing != (timing.Shift{Ms: 2_000}) {
		t.Fatalf("Align = %#v, %v; want Shift{2000}", got.Timing, err)
	}
	if n >= 96<<20 {
		t.Errorf("a reference Align at 10,000 spans a side held %d MiB at its peak, want under 96", n>>20)
	}
}
