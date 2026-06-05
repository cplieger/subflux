package api

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzDetectDrift(f *testing.F) {
	f.Add("en,fr", "en", "os,subdl", "os", true, false)
	f.Add("", "", "", "", false, false)
	f.Add("en", "en", "os", "os", true, true)
	f.Add("en,fr,de", "en,de", "os,subdl,gestdown", "os", false, true)
	f.Add("en,en,en", "en", "os,os", "os", true, false)

	f.Fuzz(func(t *testing.T, oldLangsStr, newLangsStr, oldProvsStr, newProvsStr string, oldAdaptive, newAdaptive bool) {
		oldLangs := splitNonEmpty(oldLangsStr)
		newLangs := splitNonEmpty(newLangsStr)
		oldProvs := toProviderIDs(splitNonEmpty(oldProvsStr))
		newProvs := toProviderIDs(splitNonEmpty(newProvsStr))

		d := DetectDrift(oldLangs, newLangs, oldProvs, newProvs, oldAdaptive, newAdaptive)

		// Invariant: removed languages must be in old but not in new
		newSet := make(map[string]bool)
		for _, l := range newLangs {
			newSet[l] = true
		}
		for _, r := range d.RemovedLanguages {
			if newSet[r] {
				t.Errorf("removed lang %q still in new set", r)
			}
		}

		// Invariant: removed providers must be in old but not in new
		newProvSet := make(map[ProviderID]bool)
		for _, p := range newProvs {
			newProvSet[p] = true
		}
		for _, r := range d.RemovedProviders {
			if newProvSet[r] {
				t.Errorf("removed provider %q still in new set", r)
			}
		}

		// Invariant: AdaptiveDisabled iff old=true && new=false
		wantDisabled := oldAdaptive && !newAdaptive
		if d.AdaptiveDisabled != wantDisabled {
			t.Errorf("AdaptiveDisabled=%v, want %v", d.AdaptiveDisabled, wantDisabled)
		}

		// Invariant: no duplicates in removed languages
		seen := make(map[string]bool)
		for _, r := range d.RemovedLanguages {
			if seen[r] {
				t.Errorf("duplicate in RemovedLanguages: %q", r)
			}
			seen[r] = true
		}
	})
}

func FuzzSubtitlePath(f *testing.F) {
	f.Add("/movies/Movie.2024.mkv", "fr", false, false)
	f.Add("/tv/Show.S01E01.mp4", "en", true, false)
	f.Add("/tv/Show.S01E01.mp4", "en", false, true)
	f.Add("/tv/Show.S01E01.mp4", "en", true, true)
	f.Add("video.avi", "", false, false)

	f.Fuzz(func(t *testing.T, videoPath, lang string, hi, forced bool) {
		result := SubtitlePath(videoPath, lang, hi, forced)

		// Must end with .srt
		if !strings.HasSuffix(result, SubtitleExtSRT) {
			t.Errorf("SubtitlePath(%q,%q,%v,%v) = %q, missing .srt", videoPath, lang, hi, forced, result)
		}

		// Must contain the language code
		if lang != "" && !strings.Contains(result, lang) {
			t.Errorf("SubtitlePath result %q missing lang %q", result, lang)
		}

		// Extension of videoPath must be stripped
		ext := filepath.Ext(videoPath)
		if ext != "" && ext != SubtitleExtSRT {
			base := strings.TrimSuffix(videoPath, ext)
			if !strings.HasPrefix(result, base) {
				t.Errorf("SubtitlePath result %q missing base %q", result, base)
			}
		}
	})
}

func FuzzManualSubtitlePath(f *testing.F) {
	f.Add("/movies/Movie.mkv", "fr", 1, false, false)
	f.Add("/tv/Show.mp4", "en", 2, true, false)
	f.Add("video.avi", "de", 3, false, true)

	f.Fuzz(func(t *testing.T, videoPath, lang string, n int, hi, forced bool) {
		if n < 0 || n > 9999 {
			return
		}
		result := ManualSubtitlePath(videoPath, lang, n, hi, forced)

		// Must end with .srt
		if !strings.HasSuffix(result, SubtitleExtSRT) {
			t.Errorf("ManualSubtitlePath result %q missing .srt", result)
		}

		// Must contain the language
		if lang != "" && !strings.Contains(result, lang) {
			t.Errorf("ManualSubtitlePath result %q missing lang %q", result, lang)
		}
	})
}

func FuzzLogOnceCapacity(f *testing.F) {
	f.Add("key1", "key2", "key3", 2)
	f.Add("a", "a", "b", 1)
	f.Add("", "x", "y", 0)

	f.Fuzz(func(t *testing.T, k1, k2, k3 string, cap int) {
		if cap < 0 || cap > 100 {
			return
		}
		l := newLogOnce(cap)

		// First call for a key should return true (if capacity allows)
		r1 := l.first(k1)
		if cap > 0 && !r1 {
			t.Errorf("first(%q) = false with cap=%d, want true", k1, cap)
		}
		if cap == 0 && r1 {
			t.Errorf("first(%q) = true with cap=0, want false", k1)
		}

		// Second call for same key must always return false
		r2 := l.first(k1)
		if r2 {
			t.Error("second call to first() should return false")
		}
	})
}

func FuzzVariantFromFlags(f *testing.F) {
	f.Add(true, true)
	f.Add(true, false)
	f.Add(false, true)
	f.Add(false, false)

	f.Fuzz(func(t *testing.T, hi, forced bool) {
		v := VariantFromFlags(hi, forced)
		switch {
		case hi:
			if v != VariantHI {
				t.Errorf("VariantFromFlags(true, %v) = %q, want %q", forced, v, VariantHI)
			}
		case forced:
			if v != VariantForced {
				t.Errorf("VariantFromFlags(false, true) = %q, want %q", v, VariantForced)
			}
		default:
			if v != DefaultVariant {
				t.Errorf("VariantFromFlags(false, false) = %q, want %q", v, DefaultVariant)
			}
		}
	})
}

func FuzzUniqueStrings(f *testing.F) {
	f.Add("a,b,c,a,b")
	f.Add("")
	f.Add("x")
	f.Add("a,a,a,a")

	f.Fuzz(func(t *testing.T, input string) {
		items := splitNonEmpty(input)
		result := uniqueStrings(items)

		// No duplicates in result
		seen := make(map[string]bool)
		for _, s := range result {
			if seen[s] {
				t.Errorf("duplicate in uniqueStrings result: %q", s)
			}
			seen[s] = true
		}

		// All items in input appear in result
		for _, s := range items {
			if !seen[s] {
				t.Errorf("item %q from input missing in result", s)
			}
		}

		// Idempotence
		result2 := uniqueStrings(result)
		if len(result2) != len(result) {
			t.Errorf("uniqueStrings not idempotent: %d vs %d", len(result), len(result2))
		}
	})
}

// helpers

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func toProviderIDs(ss []string) []ProviderID {
	out := make([]ProviderID, len(ss))
	for i, s := range ss {
		out[i] = ProviderID(s)
	}
	return out
}
