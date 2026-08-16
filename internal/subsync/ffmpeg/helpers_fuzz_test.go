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
	f.Add(" und ")

	f.Fuzz(func(t *testing.T, in string) {
		// Only test capitalizations of "und"/"undetermined" — everything else
		// resolves through the injected canonicalizer and is covered below.
		lower := lowerASCII(in)
		if lower != "und" && lower != "undetermined" {
			t.Skip()
		}
		// The real mapper is used, not nil: a nil mapper rejects everything, so
		// the assertion would hold vacuously and pin nothing.
		got := NormalizeFFprobeLang(in, testLangMapper)
		if got != "" {
			t.Fatalf("NormalizeFFprobeLang(%q, testLangMapper) = %q; want empty for undetermined", in, got)
		}
	})
}

// FuzzNormalizeFFprobeLangNamespace asserts that the result is always either
// empty or exactly two lowercase ASCII letters.
//
// Bug class: the result becomes a segment of the subtitle filename on disk and
// part of the bbolt state key, so anything outside that shape cannot round-trip
// through a scan — it matches no configured target, silently and permanently.
// The previous implementation returned an unrecognized three-letter code
// verbatim ("xyz" for "xyz") and a malformed tag verbatim ("-en" for "-en"),
// both of which this property rejects.
func FuzzNormalizeFFprobeLangNamespace(f *testing.F) {
	f.Add("en")
	f.Add("eng")
	f.Add("pt-BR")
	f.Add("por-BR")
	f.Add("xyz")
	f.Add("-en")
	f.Add("und")
	f.Add("")
	f.Add("yue")
	f.Add("zh-Hant")
	f.Add("abcdefghijklmnopqrstuvwxyz")
	f.Add("\x00\x01")

	f.Fuzz(func(t *testing.T, in string) {
		got := NormalizeFFprobeLang(in, testLangMapper)
		if got == "" {
			return
		}
		if len(got) != 2 {
			t.Fatalf("NormalizeFFprobeLang(%q, testLangMapper) = %q (len %d); want len 0 or 2", in, got, len(got))
		}
		for _, r := range got {
			if r < 'a' || r > 'z' {
				t.Fatalf("NormalizeFFprobeLang(%q, testLangMapper) = %q; want lowercase ASCII letters", in, got)
			}
		}
		// A resolved code is already in the internal space, so resolving it
		// again must not move it.
		if again := NormalizeFFprobeLang(got, testLangMapper); again != got {
			t.Fatalf("NormalizeFFprobeLang not idempotent: %q -> %q -> %q", in, got, again)
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
