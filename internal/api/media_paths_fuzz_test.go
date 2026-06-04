package api

import (
	"strings"
	"testing"
)

func FuzzSubtitlePath(f *testing.F) {
	f.Add("/media/movie.mkv", "en", true, false)
	f.Add("/media/show.S01E01.mp4", "fr", false, true)
	f.Add("", "", false, false)
	f.Add("noext", "zh", true, true)
	f.Add("/path/with spaces/file.avi", "pt-br", false, false)

	f.Fuzz(func(t *testing.T, videoPath, lang string, hi, forced bool) {
		result := SubtitlePath(videoPath, lang, hi, forced)
		if !strings.HasSuffix(result, SubtitleExtSRT) {
			t.Errorf("SubtitlePath(%q,%q,%v,%v) = %q, missing .srt suffix", videoPath, lang, hi, forced, result)
		}
		if hi && !strings.Contains(result, ".hi") {
			t.Errorf("SubtitlePath(%q,%q,%v,%v) = %q, missing .hi", videoPath, lang, hi, forced, result)
		}
		if forced && !strings.Contains(result, ".forced") {
			t.Errorf("SubtitlePath(%q,%q,%v,%v) = %q, missing .forced", videoPath, lang, hi, forced, result)
		}
	})
}

func FuzzManualSubtitlePath(f *testing.F) {
	f.Add("/media/movie.mkv", "en", 1, false, false)
	f.Add("file.mp4", "fr", 0, true, true)
	f.Add("", "", -1, false, false)

	f.Fuzz(func(t *testing.T, videoPath, lang string, n int, hi, forced bool) {
		result := ManualSubtitlePath(videoPath, lang, n, hi, forced)
		if !strings.HasSuffix(result, SubtitleExtSRT) {
			t.Errorf("ManualSubtitlePath result %q missing .srt suffix", result)
		}
	})
}
