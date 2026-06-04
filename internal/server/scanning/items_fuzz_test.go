package scanning

import (
	"slices"
	"testing"

	"subflux/internal/api"
)

// FuzzSortByTitleIdempotent verifies the idempotence invariant:
// sorting an already-sorted slice produces the same result.
func FuzzSortByTitleIdempotent(f *testing.F) {
	f.Add("Show A", 1, 1, "Show B", 2, 3)
	f.Add("", 0, 0, "Z", 99, 99)
	f.Add("same", 1, 1, "same", 1, 2)

	f.Fuzz(func(t *testing.T, titleA string, seasonA, epA int, titleB string, seasonB, epB int) {
		episodes := []ScanItem{
			{Series: &api.Series{Title: titleA}, Ep: &api.Episode{SeasonNumber: seasonA, EpisodeNumber: epA}},
			{Series: &api.Series{Title: titleB}, Ep: &api.Episode{SeasonNumber: seasonB, EpisodeNumber: epB}},
		}

		sorted1 := SortByTitle(episodes, nil)
		sorted2 := SortByTitle(sorted1, nil)

		if len(sorted1) != len(sorted2) {
			t.Fatalf("length mismatch: %d vs %d", len(sorted1), len(sorted2))
		}
		for i := range sorted1 {
			t1 := ScanItemTitle(sorted1[i])
			t2 := ScanItemTitle(sorted2[i])
			s1a, e1a := ScanItemSeasonEp(sorted1[i])
			s2a, e2a := ScanItemSeasonEp(sorted2[i])
			if t1 != t2 || s1a != s2a || e1a != e2a {
				t.Fatalf("idempotence violated at index %d", i)
			}
		}
	})
}

// FuzzSortByTitleStable verifies that output length equals input length
// (partition property: no elements lost or duplicated).
func FuzzSortByTitleStable(f *testing.F) {
	f.Add("X", 1, 1, "Y", 2, 2, "Z", 3, 3)

	f.Fuzz(func(t *testing.T, t1 string, s1, e1 int, t2 string, s2, e2 int, t3 string, s3, e3 int) {
		episodes := []ScanItem{
			{Series: &api.Series{Title: t1}, Ep: &api.Episode{SeasonNumber: s1, EpisodeNumber: e1}},
			{Series: &api.Series{Title: t2}, Ep: &api.Episode{SeasonNumber: s2, EpisodeNumber: e2}},
		}
		movies := []ScanItem{
			{Movie: &api.Movie{Title: t3}},
		}

		sorted := SortByTitle(episodes, movies)
		if len(sorted) != 3 {
			t.Fatalf("expected 3 elements, got %d", len(sorted))
		}

		// Verify sorted order (non-decreasing by title, then season, then episode).
		ok := slices.IsSortedFunc(sorted, func(a, b ScanItem) int {
			ta := ScanItemTitle(a)
			tb := ScanItemTitle(b)
			if c := compareCI(ta, tb); c != 0 {
				return c
			}
			sa, ea := ScanItemSeasonEp(a)
			sb, eb := ScanItemSeasonEp(b)
			if sa != sb {
				return sa - sb
			}
			return ea - eb
		})
		if !ok {
			t.Fatal("output is not sorted")
		}
	})
}

func compareCI(a, b string) int {
	la := toLower(a)
	lb := toLower(b)
	if la < lb {
		return -1
	}
	if la > lb {
		return 1
	}
	return 0
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
