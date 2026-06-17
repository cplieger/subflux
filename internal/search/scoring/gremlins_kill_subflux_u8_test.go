package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// Tests in this file kill surviving gremlins mutation-testing mutants in
// internal/search/scoring/identity_filter.go (unit subflux-u8). Every
// identifier defined here is prefixed gk_subflux_u8_ so it never collides
// with a sibling unit that shares package scoring.

// --- FilterByIdentity: early-return guard (identity_filter.go:9) ---
//
// Kills: 9:15, 9:35, 9:55 (CONDITIONALS_NEGATION on the three == checks).
// When Title=="" && Season==0 && Episode==0 the function early-returns the
// input untouched with dropped==0. Negating any single == flips the guard to
// false, so the loop runs and the movie subtitle below (which carries a
// season) is dropped -> dropped==1, kept empty.
func Test_gk_subflux_u8_FilterByIdentity_earlyReturnWhenNoCriteria(t *testing.T) {
	req := &api.SearchRequest{Title: "", Season: 0, Episode: 0, MediaType: api.MediaTypeMovie}
	results := []api.Subtitle{{Season: 5, Episode: 3}}

	kept, dropped := FilterByIdentity(results, req)

	if dropped != 0 {
		t.Errorf("FilterByIdentity(no criteria) dropped = %d, want 0", dropped)
	}
	if len(kept) != 1 {
		t.Errorf("FilterByIdentity(no criteria) len(kept) = %d, want 1", len(kept))
	}
}

// --- FilterByIdentity: dropped counter increments (identity_filter.go:17) ---
//
// Kills: 17:11 (INCREMENT_DECREMENT on dropped++). A single non-matching
// result must yield dropped==1; dropped-- would give -1.
func Test_gk_subflux_u8_FilterByIdentity_droppedCountsUpward(t *testing.T) {
	req := &api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeEpisode, Season: 1, Episode: 1}
	results := []api.Subtitle{{Season: 5, Episode: 5}}

	kept, dropped := FilterByIdentity(results, req)

	if dropped != 1 {
		t.Errorf("FilterByIdentity(one non-match) dropped = %d, want 1", dropped)
	}
	if len(kept) != 0 {
		t.Errorf("FilterByIdentity(one non-match) len(kept) = %d, want 0", len(kept))
	}
}

// --- IdentityOK: hash match bypasses checks (identity_filter.go:25) ---
//
// Kills: 25:19 (CONDITIONALS_NEGATION on MatchedBy == MatchByHash).
func Test_gk_subflux_u8_IdentityOK_hashMatch(t *testing.T) {
	tests := []struct {
		name string
		sub  api.Subtitle
		req  api.SearchRequest
		want bool
	}{
		{
			// Hash match keeps a season-bearing sub even on a movie request.
			// == -> != loses the bypass and the movie path returns false.
			name: "hash bypasses movie season rejection",
			sub:  api.Subtitle{MatchedBy: api.MatchByHash, Season: 5, Episode: 5},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie},
			want: true,
		},
		{
			// Non-hash season sub on a movie request is dropped.
			// == -> != would treat it as hash-matched and keep it.
			name: "non-hash season sub dropped on movie",
			sub:  api.Subtitle{MatchedBy: "", Season: 5, Episode: 5},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IdentityOK(&tc.sub, &tc.req)
			if got != tc.want {
				t.Errorf("IdentityOK(%+v) = %v, want %v", tc.sub, got, tc.want)
			}
		})
	}
}

// --- IdentityOK: season/episode presence branch (identity_filter.go:29) ---
//
// Kills: 29:16 (BOUNDARY + NEGATION on Season>0) and 29:35 (BOUNDARY +
// NEGATION on Episode>0). With a movie request, entering the branch rejects
// (false); skipping it routes to validateNoMetadata, which keeps empty
// metadata (true).
func Test_gk_subflux_u8_IdentityOK_seasonEpisodePresence(t *testing.T) {
	tests := []struct {
		name string
		sub  api.Subtitle
		want bool
	}{
		{
			// No season/episode -> branch skipped -> kept (true).
			// Any >0 -> >=0 or >0 -> <=0 flip makes the branch fire -> false.
			name: "no season or episode keeps sub",
			sub:  api.Subtitle{Season: 0, Episode: 0},
			want: true,
		},
		{
			// Season present -> branch fires -> movie rejects (false).
			// Season>0 -> Season<=0 skips the branch -> true.
			name: "season present dropped on movie",
			sub:  api.Subtitle{Season: 5, Episode: 0},
			want: false,
		},
		{
			// Episode present -> branch fires -> movie rejects (false).
			// Episode>0 -> Episode<=0 skips the branch -> true.
			name: "episode present dropped on movie",
			sub:  api.Subtitle{Season: 0, Episode: 5},
			want: false,
		},
	}
	req := api.SearchRequest{MediaType: api.MediaTypeMovie}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IdentityOK(&tc.sub, &req)
			if got != tc.want {
				t.Errorf("IdentityOK(%+v, movie) = %v, want %v", tc.sub, got, tc.want)
			}
		})
	}
}

// --- IdentityOK: movie rejects season-bearing subs (identity_filter.go:30) ---
//
// Kills: 30:20 (CONDITIONALS_NEGATION on MediaType == MediaTypeMovie).
func Test_gk_subflux_u8_IdentityOK_movieMediaTypeCheck(t *testing.T) {
	tests := []struct {
		name string
		sub  api.Subtitle
		req  api.SearchRequest
		want bool
	}{
		{
			// Movie request rejects a season sub. == -> != skips the movie
			// guard and the matching episode-number path returns true.
			name: "movie rejects matching season sub",
			sub:  api.Subtitle{Season: 5, Episode: 5},
			req:  api.SearchRequest{MediaType: api.MediaTypeMovie, Season: 5, Episode: 5},
			want: false,
		},
		{
			// Episode request keeps a matching season sub. == -> != treats the
			// episode request as a movie and returns false.
			name: "episode keeps matching season sub",
			sub:  api.Subtitle{Season: 1, Episode: 1},
			req:  api.SearchRequest{MediaType: api.MediaTypeEpisode, Season: 1, Episode: 1},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IdentityOK(&tc.sub, &tc.req)
			if got != tc.want {
				t.Errorf("IdentityOK(%+v) = %v, want %v", tc.sub, got, tc.want)
			}
		})
	}
}

// --- validateNoMetadata: title / release / release-season checks ---
// (identity_filter.go:43, 48, 53, 54)
//
// Kills: 43:15, 43:34 (title guards); 48:21, 48:40 (release-name guards);
// 53:19 (MediaType==episode), 53:57 NEGATION+BOUNDARY (Season>0), 54:19
// (ReleaseName!="").
func Test_gk_subflux_u8_validateNoMetadata(t *testing.T) {
	tests := []struct {
		name string
		sub  api.Subtitle
		req  api.SearchRequest
		want bool
	}{
		{
			// Mismatching sub.Title vs req.Title -> dropped (line 43).
			// Negating sub.Title!="" or req.Title!="" skips the check -> true.
			name: "mismatched title dropped",
			sub:  api.Subtitle{Title: "Wrong Show"},
			req:  api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeMovie},
			want: false,
		},
		{
			// Mismatching release name vs req.Title -> dropped (line 48).
			// Negating sub.ReleaseName!="" or req.Title!="" skips it -> true.
			name: "mismatched release name dropped",
			sub:  api.Subtitle{ReleaseName: "Totally.Different.Show.2020.1080p"},
			req:  api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeMovie},
			want: false,
		},
		{
			// Episode + Season 3 + release tagged S05 -> wrong season -> dropped
			// (lines 53,54). Negating MediaType==episode, Season>0 (to <=0), or
			// ReleaseName!="" skips the release-season check -> true.
			name: "wrong release season dropped",
			sub:  api.Subtitle{ReleaseName: "Breaking.Bad.S05.1080p"},
			req:  api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeEpisode, Season: 3},
			want: false,
		},
		{
			// Season 0 -> the release-season check must NOT run -> kept (true).
			// Season>0 -> Season>=0 makes the check run and the S05 release
			// mismatches season 0 -> false.
			name: "season zero skips release-season check",
			sub:  api.Subtitle{ReleaseName: "Breaking.Bad.S05.1080p"},
			req:  api.SearchRequest{Title: "Breaking Bad", MediaType: api.MediaTypeEpisode, Season: 0},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validateNoMetadata(&tc.sub, &tc.req)
			if got != tc.want {
				t.Errorf("validateNoMetadata(%+v) = %v, want %v", tc.sub, got, tc.want)
			}
		})
	}
}

// --- IdentityTitleOK: matched-by bypass (identity_filter.go:66) ---
//
// Kills: 66:19 (MatchedBy!=""), 66:42 (MatchedBy!=MatchByTitle). A non-title
// match method (imdb) bypasses title validation -> true even when the title
// mismatches. Negating either operand loses the bypass -> false.
func Test_gk_subflux_u8_IdentityTitleOK_matchedByBypass(t *testing.T) {
	sub := api.Subtitle{MatchedBy: api.MatchByIMDB, Title: "Wrong Show"}
	req := api.SearchRequest{Title: "Breaking Bad"}

	got := IdentityTitleOK(&sub, &req)

	if got != true {
		t.Errorf("IdentityTitleOK(imdb-matched, mismatching title) = %v, want true", got)
	}
}

// --- IdentityTitleOK: title-presence gate (identity_filter.go:70) ---
//
// Kills: 70:15 (sub.Title!=""), 70:34 (req.Title!=""). Both titles set and
// mismatching -> the title check runs and drops the sub (false). Negating
// either gate skips the check -> true.
func Test_gk_subflux_u8_IdentityTitleOK_titleGate(t *testing.T) {
	sub := api.Subtitle{MatchedBy: "", Title: "Wrong Show"}
	req := api.SearchRequest{Title: "Breaking Bad"}

	got := IdentityTitleOK(&sub, &req)

	if got != false {
		t.Errorf("IdentityTitleOK(mismatching titles) = %v, want false", got)
	}
}

// --- IdentityTitleOK: episode-title match (identity_filter.go:71) ---
//
// Kills: 71:36 (req.EpisodeTitle!=""). sub.Title matches the requested
// EpisodeTitle but not the show Title; episodeMatch=true keeps it (true).
// Negating EpisodeTitle!="" makes episodeMatch false, the show title still
// mismatches, and the sub is dropped (false).
func Test_gk_subflux_u8_IdentityTitleOK_episodeTitleMatch(t *testing.T) {
	sub := api.Subtitle{MatchedBy: "", Title: "Pilot"}
	req := api.SearchRequest{Title: "Breaking Bad", EpisodeTitle: "Pilot"}

	got := IdentityTitleOK(&sub, &req)

	if got != true {
		t.Errorf("IdentityTitleOK(episode-title match) = %v, want true", got)
	}
}

// --- IdentityTitleOK: release-name fallback (identity_filter.go:77,78) ---
//
// Kills: 77:19 (MatchedBy==MatchByTitle), 77:52 (sub.Title==""), 78:19
// (sub.ReleaseName!=""). Title-matched sub with empty Title but a
// non-matching ReleaseName -> the fallback fires and drops it (false).
// Negating any of those three guards skips the fallback -> true.
func Test_gk_subflux_u8_IdentityTitleOK_releaseNameFallback(t *testing.T) {
	sub := api.Subtitle{MatchedBy: api.MatchByTitle, Title: "", ReleaseName: "Totally.Different.2020.1080p"}
	req := api.SearchRequest{Title: "Breaking Bad"}

	got := IdentityTitleOK(&sub, &req)

	if got != false {
		t.Errorf("IdentityTitleOK(title-matched, mismatching release) = %v, want false", got)
	}
}
