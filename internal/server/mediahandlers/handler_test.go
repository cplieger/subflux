package mediahandlers

import (
	"testing"
)

// The HTTP handlers (HandleMediaSeries, HandleMediaMovies, HandleMediaEpisodes)
// are exercised end-to-end through the server root's delegate tests
// (internal/server/media_handlers_test.go). These white-box tests cover the
// package's pure helpers directly, including edge cases the handler-level tests
// don't reach.

// --- extractPathSegment ---

func TestExtractPathSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		path   string
		prefix string
		suffix string
		want   string
	}{
		{
			name:   "extracts id between prefix and suffix",
			path:   "/api/media/series/1/episodes",
			prefix: "/api/media/series/",
			suffix: "/episodes",
			want:   "1",
		},
		{
			name:   "multi-digit id",
			path:   "/api/media/series/12345/episodes",
			prefix: "/api/media/series/",
			suffix: "/episodes",
			want:   "12345",
		},
		{
			name:   "prefix mismatch returns empty",
			path:   "/other/path/1/episodes",
			prefix: "/api/media/series/",
			suffix: "/episodes",
			want:   "",
		},
		{
			name:   "suffix not present returns empty",
			path:   "/api/media/series/1",
			prefix: "/api/media/series/",
			suffix: "/episodes",
			want:   "",
		},
		{
			name:   "empty suffix returns remainder after prefix",
			path:   "/api/media/series/1",
			prefix: "/api/media/series/",
			suffix: "",
			want:   "1",
		},
		{
			name:   "empty segment between prefix and suffix",
			path:   "/api/media/series//episodes",
			prefix: "/api/media/series/",
			suffix: "/episodes",
			want:   "",
		},
		{
			name:   "stops at first suffix occurrence",
			path:   "/api/media/series/42/episodes/extra",
			prefix: "/api/media/series/",
			suffix: "/episodes",
			want:   "42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractPathSegment(tt.path, tt.prefix, tt.suffix)
			if got != tt.want {
				t.Errorf("extractPathSegment(%q, %q, %q) = %q, want %q",
					tt.path, tt.prefix, tt.suffix, got, tt.want)
			}
		})
	}
}

// --- groupEpisodesBySeason ---

func TestGroupEpisodesBySeason_empty_returns_empty(t *testing.T) {
	t.Parallel()
	got := groupEpisodesBySeason(nil)
	if len(got) != 0 {
		t.Errorf("groupEpisodesBySeason(nil) returned %d groups, want 0", len(got))
	}
}

func TestGroupEpisodesBySeason_sorts_seasons_ascending(t *testing.T) {
	t.Parallel()
	eps := []episodeItem{
		{ID: 1, SeasonNumber: 2, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 3, SeasonNumber: 3, EpisodeNumber: 1},
	}
	got := groupEpisodesBySeason(eps)
	if len(got) != 3 {
		t.Fatalf("groupEpisodesBySeason() returned %d groups, want 3", len(got))
	}
	if got[0].Season != 1 || got[1].Season != 2 || got[2].Season != 3 {
		t.Errorf("season order = [%d, %d, %d], want [1, 2, 3]",
			got[0].Season, got[1].Season, got[2].Season)
	}
}

func TestGroupEpisodesBySeason_specials_sort_first(t *testing.T) {
	t.Parallel()
	// Season 0 holds specials; the ascending sort places them before season 1.
	eps := []episodeItem{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 0, EpisodeNumber: 1},
	}
	got := groupEpisodesBySeason(eps)
	if len(got) != 2 {
		t.Fatalf("groupEpisodesBySeason() returned %d groups, want 2", len(got))
	}
	if got[0].Season != 0 {
		t.Errorf("got[0].Season = %d, want 0 (specials first)", got[0].Season)
	}
}

func TestGroupEpisodesBySeason_sorts_episodes_within_season(t *testing.T) {
	t.Parallel()
	eps := []episodeItem{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 3},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 3, SeasonNumber: 1, EpisodeNumber: 2},
	}
	got := groupEpisodesBySeason(eps)
	if len(got) != 1 {
		t.Fatalf("groupEpisodesBySeason() returned %d groups, want 1", len(got))
	}
	nums := []int{
		got[0].Episodes[0].EpisodeNumber,
		got[0].Episodes[1].EpisodeNumber,
		got[0].Episodes[2].EpisodeNumber,
	}
	if nums[0] != 1 || nums[1] != 2 || nums[2] != 3 {
		t.Errorf("episode order within season 1 = %v, want [1, 2, 3]", nums)
	}
}

func TestGroupEpisodesBySeason_preserves_episode_count_per_season(t *testing.T) {
	t.Parallel()
	eps := []episodeItem{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 2},
		{ID: 3, SeasonNumber: 2, EpisodeNumber: 1},
	}
	got := groupEpisodesBySeason(eps)
	if len(got) != 2 {
		t.Fatalf("groupEpisodesBySeason() returned %d groups, want 2", len(got))
	}
	if len(got[0].Episodes) != 2 {
		t.Errorf("season 1 episode count = %d, want 2", len(got[0].Episodes))
	}
	if len(got[1].Episodes) != 1 {
		t.Errorf("season 2 episode count = %d, want 1", len(got[1].Episodes))
	}
}
