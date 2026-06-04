package ffmpeg

import (
	"math"
	"testing"
)

func FuzzParseFrameRate(f *testing.F) {
	f.Add("24000/1001")
	f.Add("25/1")
	f.Add("23.976")
	f.Add("30")
	f.Add("")
	f.Add("abc")
	f.Add("24/0")
	f.Add("0/0")
	f.Add("-1/1")
	f.Add("999999999999/1")

	f.Fuzz(func(t *testing.T, s string) {
		result := parseFrameRate(s)
		if math.IsNaN(result) {
			t.Fatalf("parseFrameRate(%q) returned NaN", s)
		}
		if math.IsInf(result, 0) {
			t.Fatalf("parseFrameRate(%q) returned Inf", s)
		}
	})
}

func FuzzNormalizeFFprobeLang(f *testing.F) {
	f.Add("eng")
	f.Add("en")
	f.Add("en-US")
	f.Add("und")
	f.Add("undetermined")
	f.Add("")
	f.Add("fre")
	f.Add("FR")
	f.Add("zh-Hans")

	f.Fuzz(func(t *testing.T, lang string) {
		result := NormalizeFFprobeLang(lang, nil)
		if lang == "" && result != "" {
			t.Fatalf("NormalizeFFprobeLang(%q, nil) = %q, want empty", lang, result)
		}
		if (lang == "und" || lang == "undetermined") && result != "" {
			t.Fatalf("NormalizeFFprobeLang(%q, nil) = %q, want empty", lang, result)
		}
	})
}
