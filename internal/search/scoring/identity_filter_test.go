package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

// --- FilterByIdentity ---

func TestFilterByIdentity_returns_all_when_no_criteria(t *testing.T) {
	t.Parallel()
	req := &subflux.SearchRequest{Title: "", Season: 0, Episode: 0, MediaType: subflux.MediaTypeMovie}
	results := []subflux.Subtitle{{Season: 5, Episode: 3}}

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
	req := &subflux.SearchRequest{Title: "Breaking Bad", MediaType: subflux.MediaTypeEpisode, Season: 1, Episode: 1}
	results := []subflux.Subtitle{{Season: 5, Episode: 5}}

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
		sub  subflux.Subtitle
		req  subflux.SearchRequest
		want bool
	}{
		{
			name: "hash keeps season sub on movie request",
			sub:  subflux.Subtitle{MatchedBy: subflux.MatchByHash, Season: 5, Episode: 5},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeMovie},
			want: true,
		},
		{
			name: "non-hash season sub dropped on movie request",
			sub:  subflux.Subtitle{MatchedBy: "", Season: 5, Episode: 5},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeMovie},
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
		sub  subflux.Subtitle
		req  subflux.SearchRequest
		want bool
	}{
		{
			name: "no season or episode is kept",
			sub:  subflux.Subtitle{Season: 0, Episode: 0},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeMovie},
			want: true,
		},
		{
			name: "season present dropped on movie",
			sub:  subflux.Subtitle{Season: 5, Episode: 0},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeMovie},
			want: false,
		},
		{
			name: "episode present dropped on movie",
			sub:  subflux.Subtitle{Season: 0, Episode: 5},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeMovie},
			want: false,
		},
		{
			name: "matching numbers still dropped on movie",
			sub:  subflux.Subtitle{Season: 5, Episode: 5},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeMovie, Season: 5, Episode: 5},
			want: false,
		},
		{
			name: "matching season sub kept on episode request",
			sub:  subflux.Subtitle{Season: 1, Episode: 1},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeEpisode, Season: 1, Episode: 1},
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
		sub  subflux.Subtitle
		req  subflux.SearchRequest
		want bool
	}{
		{
			name: "mismatched title dropped",
			sub:  subflux.Subtitle{Title: "Wrong Show"},
			req:  subflux.SearchRequest{Title: "Breaking Bad", MediaType: subflux.MediaTypeMovie},
			want: false,
		},
		{
			name: "mismatched release name dropped",
			sub:  subflux.Subtitle{ReleaseName: "Totally.Different.Show.2020.1080p"},
			req:  subflux.SearchRequest{Title: "Breaking Bad", MediaType: subflux.MediaTypeMovie},
			want: false,
		},
		{
			name: "wrong release season dropped",
			sub:  subflux.Subtitle{ReleaseName: "Breaking.Bad.S05.1080p"},
			req:  subflux.SearchRequest{Title: "Breaking Bad", MediaType: subflux.MediaTypeEpisode, Season: 3},
			want: false,
		},
		{
			name: "season zero skips release-season check",
			sub:  subflux.Subtitle{ReleaseName: "Breaking.Bad.S05.1080p"},
			req:  subflux.SearchRequest{Title: "Breaking Bad", MediaType: subflux.MediaTypeEpisode, Season: 0},
			want: true,
		},
		{
			name: "release with no extractable season is accepted",
			sub:  subflux.Subtitle{ReleaseName: "Great.Film.2020.1080p.WEB"},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeEpisode, Season: 1},
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
	sub := subflux.Subtitle{MatchedBy: subflux.MatchByIMDB, Title: "Wrong Show"}
	req := subflux.SearchRequest{Title: "Breaking Bad"}
	if got := IdentityTitleOK(&sub, &req); !got {
		t.Errorf("IdentityTitleOK(imdb-matched, mismatching title) = %v, want true", got)
	}
}

func TestIdentityTitleOK_mismatching_titles_dropped(t *testing.T) {
	t.Parallel()
	sub := subflux.Subtitle{MatchedBy: "", Title: "Wrong Show"}
	req := subflux.SearchRequest{Title: "Breaking Bad"}
	if got := IdentityTitleOK(&sub, &req); got {
		t.Errorf("IdentityTitleOK(mismatching titles) = %v, want false", got)
	}
}

func TestIdentityTitleOK_episode_title_match_kept(t *testing.T) {
	t.Parallel()
	// The subtitle title matches the requested episode title (not the show
	// title), so it is kept.
	sub := subflux.Subtitle{MatchedBy: "", Title: "Pilot"}
	req := subflux.SearchRequest{Title: "Breaking Bad", EpisodeTitle: "Pilot"}
	if got := IdentityTitleOK(&sub, &req); !got {
		t.Errorf("IdentityTitleOK(episode-title match) = %v, want true", got)
	}
}

func TestIdentityTitleOK_release_name_fallback_dropped(t *testing.T) {
	t.Parallel()
	// A title-matched sub with an empty Title but a non-matching ReleaseName is
	// dropped: every operand of the fallback's AND-chain is satisfied.
	sub := subflux.Subtitle{MatchedBy: subflux.MatchByTitle, Title: "", ReleaseName: "Totally.Different.Show.2020.1080p"}
	req := subflux.SearchRequest{Title: "Breaking Bad"}
	if got := IdentityTitleOK(&sub, &req); got {
		t.Errorf("IdentityTitleOK(title-matched, mismatching release) = %v, want false", got)
	}
}

func TestIdentityOK_release_name_episode_marker(t *testing.T) {
	t.Parallel()
	s01e01 := subflux.SearchRequest{MediaType: subflux.MediaTypeEpisode, Title: "Unforgotten", Season: 1, Episode: 1}
	tests := []struct {
		name string
		sub  subflux.Subtitle
		req  subflux.SearchRequest
		want bool
	}{
		{
			name: "request-copied numbers cannot rescue another episode's release",
			sub:  subflux.Subtitle{ReleaseName: "Unforgotten.S01E06.720p.HDTV.x264-ORGANiC", Season: 1, Episode: 1, MatchedBy: subflux.MatchByIMDB},
			req:  s01e01, want: false,
		},
		{
			name: "another episode without metadata",
			sub:  subflux.Subtitle{ReleaseName: "Unforgotten.S01E06.720p.HDTV.x264-ORGANiC"},
			req:  s01e01, want: false,
		},
		{
			name: "the requested episode",
			sub:  subflux.Subtitle{ReleaseName: "Unforgotten.S01E01.720p.HDTV.x264-ORGANiC", Season: 1, Episode: 1, MatchedBy: subflux.MatchByIMDB},
			req:  s01e01, want: true,
		},
		{
			name: "a season pack claims no episode",
			sub:  subflux.Subtitle{ReleaseName: "Unforgotten.S01.720p.HDTV.x264-ORGANiC", Season: 1, Episode: 1, MatchedBy: subflux.MatchByIMDB},
			req:  s01e01, want: true,
		},
		{
			name: "a range covering the episode",
			sub:  subflux.Subtitle{ReleaseName: "Unforgotten.S01E01E02.720p.HDTV.x264-ORGANiC", Season: 1, Episode: 1, MatchedBy: subflux.MatchByIMDB},
			req:  s01e01, want: true,
		},
		{
			name: "a hash match on a contradicting name",
			sub:  subflux.Subtitle{ReleaseName: "Unforgotten.S01E06.720p.HDTV.x264-ORGANiC", MatchedBy: subflux.MatchByHash},
			req:  s01e01, want: true,
		},
		{
			name: "absolute numbering matches",
			sub:  subflux.Subtitle{ReleaseName: "Show.S01E13.1080p.WEB-DL", Season: 2, Episode: 1, MatchedBy: subflux.MatchByIMDB},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeEpisode, Title: "Show", Season: 2, Episode: 1, AbsoluteEpisode: 13},
			want: true,
		},
		{
			name: "a movie request ignores episode markers",
			sub:  subflux.Subtitle{ReleaseName: "Movie.S01E06.2019.1080p"},
			req:  subflux.SearchRequest{MediaType: subflux.MediaTypeMovie},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IdentityOK(&tc.sub, &tc.req); got != tc.want {
				t.Errorf("IdentityOK(%q) = %v, want %v", tc.sub.ReleaseName, got, tc.want)
			}
		})
	}
}
