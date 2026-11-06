package timing_test

import (
	"bytes"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// FuzzDecodeTiming: every accepted input is a valid Timing whose encoding
// is exactly the input, so stored bytes have one form per value.
func FuzzDecodeTiming(f *testing.F) {
	for _, tc := range validTimings {
		// A seed of thousands of bytes (2,048 segments) stalls the fuzzer for
		// a minute minimizing each input derived from it.
		if b := timing.AppendTiming(nil, tc.t); len(b) <= 256 {
			f.Add(b)
		}
	}
	for _, tc := range undecodable {
		f.Add(tc.b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := timing.DecodeTiming(b)
		if err != nil {
			return
		}
		if err := timing.Validate(got); err != nil {
			t.Fatalf("DecodeTiming(% x) = %#v, which fails Validate: %v", b, got, err)
		}
		if again := timing.AppendTiming(nil, got); !bytes.Equal(again, b) {
			t.Fatalf("AppendTiming(DecodeTiming(% x)) = % x", b, again)
		}
	})
}
