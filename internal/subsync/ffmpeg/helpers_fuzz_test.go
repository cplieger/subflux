package ffmpeg

import (
	"math"
	"testing"
)

// FuzzNormalizeFFprobeLangUnd asserts that "und"/"undetermined" inputs
// (regardless of case) always normalize to an empty string.
//
// Bug class: an "undetermined" track language slipping through as
// non-empty would mask itself as a real Alpha-2 code in track selection,
// causing the wrong subtitle track to be picked.
func FuzzNormalizeFFprobeLangUnd(f *testing.F) {
	f.Add("und")
	f.Add("UND")
	f.Add("Und")
	f.Add("undetermined")
	f.Add("UNDETERMINED")

	f.Fuzz(func(t *testing.T, in string) {
		// Only test capitalizations of "und"/"undetermined" — everything else has
		// known surprising fallbacks the SUT preserves verbatim.
		lower := lowerASCII(in)
		if lower != "und" && lower != "undetermined" {
			t.Skip()
		}
		got := NormalizeFFprobeLang(in, nil)
		if got != "" {
			t.Fatalf("NormalizeFFprobeLang(%q, nil) = %q; want empty for undetermined", in, got)
		}
	})
}

// FuzzNormalizeFFprobeLang2Char asserts that any 2-char lowercase ASCII
// input round-trips through normalization unchanged.
//
// Bug class: spurious normalization (e.g. case mangling, char dropping)
// of a valid Alpha-2 code would break upstream language matching against
// provider catalogs that use exact Alpha-2 keys.
func FuzzNormalizeFFprobeLang2Char(f *testing.F) {
	f.Add(byte('e'), byte('n'))
	f.Add(byte('z'), byte('h'))
	f.Add(byte('p'), byte('t'))

	f.Fuzz(func(t *testing.T, a, b byte) {
		if a < 'a' || a > 'z' || b < 'a' || b > 'z' {
			t.Skip()
		}
		in := string([]byte{a, b})
		got := NormalizeFFprobeLang(in, nil)
		if got != in {
			t.Fatalf("NormalizeFFprobeLang(%q, nil) = %q; want unchanged", in, got)
		}
	})
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// FuzzParseFrameRate exercises the fractional frame-rate parser with arbitrary
// strings, including division-by-zero and non-numeric input. The parser must
// never return NaN or Inf (a non-finite rate is meaningless to callers and
// would poison framerate-mismatch detection downstream).
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
