package search

import (
	"path/filepath"
	"testing"

	"subflux/internal/api"
)

func FuzzFilterByScore(f *testing.F) {
	f.Add(10, 20, 30, 15)
	f.Add(0, 0, 0, 0)
	f.Add(100, 50, 75, 60)
	f.Add(-1, 5, 10, 0)

	f.Fuzz(func(t *testing.T, s1, s2, s3, minScore int) {
		if s1 < -1000 || s1 > 1000 || s2 < -1000 || s2 > 1000 || s3 < -1000 || s3 > 1000 {
			return
		}
		scored := []scoredSub{
			{sub: api.Subtitle{ID: "1"}, score: s1},
			{sub: api.Subtitle{ID: "2"}, score: s2},
			{sub: api.Subtitle{ID: "3"}, score: s3},
		}
		result := filterByScore(scored, minScore)
		// Invariant: all results must be >= minScore.
		for _, r := range result {
			if r.score < minScore {
				t.Fatalf("filterByScore returned score %d below min %d", r.score, minScore)
			}
		}
		// Invariant: result is a subset.
		if len(result) > len(scored) {
			t.Fatalf("filterByScore output (%d) larger than input (%d)", len(result), len(scored))
		}
	})
}

func FuzzGlobEscape(f *testing.F) {
	f.Add("/media/movie.mkv")
	f.Add("/path/with[brackets]")
	f.Add("file*.txt")
	f.Add("question?mark")
	f.Add(`back\slash`)
	f.Add("")

	f.Fuzz(func(t *testing.T, s string) {
		escaped := globEscape(s)
		// The escaped string used in filepath.Match should match the literal s.
		matched, err := filepath.Match(escaped, s)
		if err != nil {
			// Some inputs produce invalid patterns even after escaping;
			// that's acceptable but must not panic.
			return
		}
		if !matched {
			t.Fatalf("globEscape(%q) = %q does not match original via filepath.Match", s, escaped)
		}
	})
}
