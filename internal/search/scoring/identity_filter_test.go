package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// --- FilterByIdentity ---

func TestFilterByIdentity_returns_all_when_no_criteria(t *testing.T) {
	t.Parallel()
	req := &api.SearchRequest{Title: "", Season: 0, Episode: 0, MediaType: api.MediaTypeMovie}
	results := []api.Subtitle{{Season: 5, Episode: 3}}

	kept, dropped := FilterByIdentity(results, req)

	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if len(kept) != 1 {
		t.Errorf("len(kept) = %d, want 1", len(kept))
	}
}

func TestFilterByIdentity_counts_dropped_non_matches(t *testing.T) {
	t.Parallel()
	req := &api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeEpisode, Season: 1, Episode: 1}
	results := []api.Subtitle{{Season: 5, Episode: 5}}

	kept, dropped := FilterByIdentity(results, req)

	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if len(kept) != 0 {
		t.Errorf("len(kept) = %d, want 0", len(kept))
	}
}

// --- IdentityOK ---

func TestIdentityOK_hash_match_bypasses_checks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		sub  api.Subtitle
		req  api.SearchRequest
		want bool
	}{
		{
			name: "hash keeps season sub on movie request",
			sub:  api.Subtitle{MatchedBy: api.MatchByHash, Season: 5, Episode: 5},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie},
			want: true,
		},
		{
			name: "non-hash season sub dropped on movie request",
			sub:  api.Subtitle{MatchedBy: "", Season: 5, Episode: 5},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IdentityOK(&tc.sub, &tc.req); got != tc.want {
				t.Errorf("IdentityOK(%+v) = %v, want %v", tc.sub, got, tc.want)
			}
		})
	}
}

func TestIdentityOK_season_bearing_subs_only_kept_on_matching_episode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		sub  api.Subtitle
		req  api.SearchRequest
		want bool
	}{
		{
			name: "no season or episode is kept",
			sub:  api.Subtitle{Season: 0, Episode: 0},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie},
			want: true,
		},
		{
			name: "season present dropped on movie",
			sub:  api.Subtitle{Season: 5, Episode: 0},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie},
			want: false,
		},
		{
			name: "episode present dropped on movie",
			sub:  api.Subtitle{Season: 0, Episode: 5},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie},
			want: false,
		},
		{
			name: "matching numbers still dropped on movie",
			sub:  api.Subtitle{Season: 5, Episode: 5},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie, Season: 5, Episode: 5},
			want: false,
		},
		{
			name: "matching season sub kept on episode request",
			sub:  api.Subtitle{Season: 1, Episode: 1},
			req:  api.SearchRequest{MediaType: api.MediaTypeEpisode, Season: 1, Episode: 1},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IdentityOK(&tc.sub, &tc.req); got != tc.want {
				t.Errorf("IdentityOK(%+v, %v) = %v, want %v", tc.sub, tc.req.MediaType, got, tc.want)
			}
		})
	}
}

// IdentityOK routes a subtitle with no season/episode metadata through the
// title, release-name, and release-season checks. These cases exercise that
// path through the public API.
func TestIdentityOK_no_metadata_title_and_release_checks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		sub  api.Subtitle
		req  api.SearchRequest
		want bool
	}{
		{
			name: "mismatched title dropped",
			sub:  api.Subtitle{Title: "Wrong Show"},
			req:  api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeMovie},
			want: false,
		},
		{
			name: "mismatched release name dropped",
			sub:  api.Subtitle{ReleaseName: "Totally.Different.Show.2020.1080p"},
			req:  api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeMovie},
			want: false,
		},
		{
			name: "wrong release season dropped",
			sub:  api.Subtitle{ReleaseName: "Breaking.Bad.S05.1080p"},
			req:  api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeEpisode, Season: 3},
			want: false,
		},
		{
			name: "season zero skips release-season check",
			sub:  api.Subtitle{ReleaseName: "Breaking.Bad.S05.1080p"},
			req:  api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeEpisode, Season: 0},
			want: true,
		},
		{
			name: "release with no extractable season is accepted",
			sub:  api.Subtitle{ReleaseName: "Great.Film.2020.1080p.WEB"},
			req:  api.SearchRequest{MediaType: api.MediaTypeEpisode, Season: 1},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IdentityOK(&tc.sub, &tc.req); got != tc.want {
				t.Errorf("IdentityOK(%+v) = %v, want %v", tc.sub, got, tc.want)
			}
		})
	}
}

// --- IdentityTitleOK ---

func TestIdentityTitleOK_stable_id_match_bypasses_title(t *testing.T) {
	t.Parallel()
	// A non-title match method (IMDB) skips title validation even when the
	// title mismatches.
	sub := api.Subtitle{MatchedBy: api.MatchByIMDB, Title: "Wrong Show"}
	req := api.SearchRequest{Title: "Breaking Bad"}
	if got := IdentityTitleOK(&sub, &req); !got {
		t.Errorf("IdentityTitleOK(imdb-matched, mismatching title) = %v, want true", got)
	}
}

func TestIdentityTitleOK_mismatching_titles_dropped(t *testing.T) {
	t.Parallel()
	sub := api.Subtitle{MatchedBy: "", Title: "Wrong Show"}
	req := api.SearchRequest{Title: "Breaking Bad"}
	if got := IdentityTitleOK(&sub, &req); got {
		t.Errorf("IdentityTitleOK(mismatching titles) = %v, want false", got)
	}
}

func TestIdentityTitleOK_episode_title_match_kept(t *testing.T) {
	t.Parallel()
	// The subtitle title matches the requested episode title (not the show
	// title), so it is kept.
	sub := api.Subtitle{MatchedBy: "", Title: "Pilot"}
	req := api.SearchRequest{Title: "Breaking Bad", EpisodeTitle: "Pilot"}
	if got := IdentityTitleOK(&sub, &req); !got {
		t.Errorf("IdentityTitleOK(episode-title match) = %v, want true", got)
	}
}

func TestIdentityTitleOK_release_name_fallback_dropped(t *testing.T) {
	t.Parallel()
	// A title-matched sub with an empty Title but a non-matching ReleaseName is
	// dropped: every operand of the fallback's AND-chain is satisfied.
	sub := api.Subtitle{MatchedBy: api.MatchByTitle, Title: "", ReleaseName: "Totally.Different.Show.2020.1080p"}
	req := api.SearchRequest{Title: "Breaking Bad"}
	if got := IdentityTitleOK(&sub, &req); got {
		t.Errorf("IdentityTitleOK(title-matched, mismatching release) = %v, want false", got)
	}
}
