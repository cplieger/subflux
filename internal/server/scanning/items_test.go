package scanning

import (
	"testing"

	"github.com/cplieger/arrapi"
)

// SortByTitle returns every input item; one episode and one movie together
// yield two results. (Treating equal episode/movie counts as an empty queue
// would silently drop the whole queue.)
func TestSortByTitle_returns_all_items(t *testing.T) {
	t.Parallel()
	ep := ScanItem{Series: &arrapi.Series{Title: "Show"}, Ep: &arrapi.Episode{SeasonNumber: 1, EpisodeNumber: 1}}
	mv := ScanItem{Movie: &arrapi.Movie{Title: "Movie"}}

	got := SortByTitle([]ScanItem{ep}, []ScanItem{mv})

	if len(got) != 2 {
		t.Errorf("SortByTitle(1 episode, 1 movie) len = %d, want 2", len(got))
	}
}

// Same-title episodes are ordered by season number ahead of episode number.
func TestSortByTitle_orders_by_season(t *testing.T) {
	t.Parallel()
	s1 := ScanItem{Series: &arrapi.Series{Title: "Show"}, Ep: &arrapi.Episode{SeasonNumber: 1, EpisodeNumber: 9}}
	s2 := ScanItem{Series: &arrapi.Series{Title: "Show"}, Ep: &arrapi.Episode{SeasonNumber: 2, EpisodeNumber: 1}}

	got := SortByTitle([]ScanItem{s2, s1}, nil)

	if len(got) != 2 {
		t.Fatalf("SortByTitle len = %d, want 2", len(got))
	}
	if got[0].Ep.SeasonNumber != 1 {
		t.Errorf("SortByTitle[0].SeasonNumber = %d, want 1 (season ordering)", got[0].Ep.SeasonNumber)
	}
}

// Same-title, same-season episodes are ordered by ascending episode number.
func TestSortByTitle_orders_by_episode(t *testing.T) {
	t.Parallel()
	e3 := ScanItem{Series: &arrapi.Series{Title: "Show"}, Ep: &arrapi.Episode{SeasonNumber: 1, EpisodeNumber: 3}}
	e1 := ScanItem{Series: &arrapi.Series{Title: "Show"}, Ep: &arrapi.Episode{SeasonNumber: 1, EpisodeNumber: 1}}

	got := SortByTitle([]ScanItem{e3, e1}, nil)

	if len(got) != 2 {
		t.Fatalf("SortByTitle len = %d, want 2", len(got))
	}
	if got[0].Ep.EpisodeNumber != 1 || got[1].Ep.EpisodeNumber != 3 {
		t.Errorf("SortByTitle episode order = [%d, %d], want [1, 3]",
			got[0].Ep.EpisodeNumber, got[1].Ep.EpisodeNumber)
	}
}

// ScanItemTitle returns the series title for an episode item, the movie title
// for a movie item, and "" when neither is set.
func TestScanItemTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		item ScanItem
		want string
	}{
		{"series", ScanItem{Series: &arrapi.Series{Title: "TheSeries"}}, "TheSeries"},
		{"movie", ScanItem{Movie: &arrapi.Movie{Title: "TheMovie"}}, "TheMovie"},
		{"neither", ScanItem{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ScanItemTitle(tc.item); got != tc.want {
				t.Errorf("ScanItemTitle(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
