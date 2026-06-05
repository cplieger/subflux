package ffmpeg

import (
	"math"
	"testing"
)

// FuzzParseFrameRate exercises the fractional frame rate parser with arbitrary
// strings including division-by-zero cases and non-numeric input.
//
// Bug class: division by zero panic; NaN/Inf return values; panic on
// malformed fraction strings.
func FuzzParseFrameRate(f *testing.F) {
	f.Add("24000/1001")
	f.Add("30")
	f.Add("0/0")
	f.Add("")
	f.Add("abc/def")
	f.Add("1/0")
	f.Add("-1/1")

	f.Fuzz(func(t *testing.T, s string) {
		result := parseFrameRate(s)
		if math.IsNaN(result) {
			t.Fatalf("NaN returned for input %q", s)
		}
		if math.IsInf(result, 0) {
			t.Fatalf("Inf returned for input %q", s)
		}
	})
}
