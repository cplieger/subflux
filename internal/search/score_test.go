package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search/scoring"
	"pgregory.net/rapid"
)

func TestFilterByIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		req     *api.SearchRequest
		subs    []api.Subtitle
		wantLen int
	}{
		{
			name:    "movies_pass_through",
			req:     &api.SearchRequest{MediaType: "movie", Season: 0, Episode: 0},
			subs:    []api.Subtitle{{ReleaseName: "test"}},
			wantLen: 1,
		},
		{
			name: "drops_wrong_season",
			req:  &api.SearchRequest{MediaType: "episode", Title: "Test", Season: 1, Episode: 1},
			subs: []api.Subtitle{
				{ReleaseName: "correct", Season: 1, Episode: 1},
				{ReleaseName: "wrong season", Season: 2, Episode: 1},
			},
			wantLen: 1,
		},
		{
			name: "drops_wrong_episode",
			req:  &api.SearchRequest{MediaType: "episode", Title: "Test", Season: 3, Episode: 21},
			subs: []api.Subtitle{
				{ReleaseName: "correct", Season: 3, Episode: 21},
				{ReleaseName: "wrong ep", Season: 3, Episode: 22},
				{ReleaseName: "wrong ep 101", Season: 3, Episode: 101},
			},
			wantLen: 1,
		},
		{
			name:    "keeps_unknown_identity",
			req:     &api.SearchRequest{MediaType: "episode", Title: "Test", Season: 1, Episode: 5},
			subs:    []api.Subtitle{{Season: 0, Episode: 0}},
			wantLen: 1,
		},
		{
			name:    "keeps_hash_match",
			req:     &api.SearchRequest{MediaType: "episode", Title: "Test", Season: 1, Episode: 1},
			subs:    []api.Subtitle{{ReleaseName: "hash match", Season: 2, Episode: 5, MatchedBy: "hash"}},
			wantLen: 1,
		},
		{
			name: "mixed",
			req:  &api.SearchRequest{MediaType: "episode", Title: "Dragon Ball", Season: 1, Episode: 1},
			subs: []api.Subtitle{
				{ReleaseName: "Dragon.Ball.S01E01.mkv", Season: 1, Episode: 1, Provider: "opensubtitles"},
				{ReleaseName: "wrong ep from OS", Season: 1, Episode: 101, Provider: "opensubtitles"},
				{ReleaseName: "Dragon.Ball.DAIMA.S01E01.mkv", Season: 0, Episode: 0, Provider: "animetosho"},
				{Provider: "betaseries"},
				{ReleaseName: "hash", Season: 5, Episode: 99, MatchedBy: "hash", Provider: "opensubtitles"},
			},
			wantLen: 3,
		},
		{
			name:    "no_season_episode_in_request",
			req:     &api.SearchRequest{MediaType: "episode", Title: "Test", Season: 0, Episode: 0},
			subs:    []api.Subtitle{{ReleaseName: "any", Season: 5, Episode: 10}},
			wantLen: 1,
		},
		{
			name:    "drops_wrong_show_from_release_name",
			req:     &api.SearchRequest{MediaType: "episode", Title: "Dragon Ball", Season: 1, Episode: 1},
			subs:    []api.Subtitle{{ReleaseName: "Dragon.Ball.DAIMA.S01E01.1080p.WEB-DL.mkv", Provider: "animetosho"}},
			wantLen: 0,
		},
		{
			name:    "keeps_correct_show_from_release_name",
			req:     &api.SearchRequest{MediaType: "episode", Title: "Dragon Ball", Season: 1, Episode: 1},
			subs:    []api.Subtitle{{ReleaseName: "Dragon.Ball.S01E01.480p.DVD.mkv", Provider: "animetosho"}},
			wantLen: 1,
		},
		{
			name:    "drops_wrong_title_from_provider",
			req:     &api.SearchRequest{MediaType: "episode", Title: "Dragon Ball", Season: 1, Episode: 1},
			subs:    []api.Subtitle{{Title: "Dragon Ball DAIMA", Season: 1, Episode: 1}},
			wantLen: 0,
		},
		{
			name:    "keeps_matching_title_from_provider",
			req:     &api.SearchRequest{MediaType: "episode", Title: "Dragon Ball", Season: 1, Episode: 1},
			subs:    []api.Subtitle{{Title: "Dragon Ball", Season: 1, Episode: 1}},
			wantLen: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := filterByIdentity(tt.subs, tt.req)
			if len(got) != tt.wantLen {
				t.Fatalf("filterByIdentity() returned %d results, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestNormalizeTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{"Dragon Ball", "dragon ball"},
		{"Dragon.Ball", "dragon ball"},
		{"Dragon-Ball", "dragon ball"},
		{"Dragon_Ball", "dragon ball"},
		{"  Dragon  Ball  ", "dragon ball"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeTitle(tt.input)
		if got != tt.want {
			t.Errorf("normalizeTitle(%q) = %q, want %q",
				tt.input, got, tt.want)
		}
	}
}

func TestReleaseNameMatchesTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		title, release string
		want           bool
	}{
		{"Dragon Ball", "Dragon.Ball.S01E01.480p.mkv", true},
		{"Dragon Ball", "Dragon.Ball.DAIMA.S01E01.mkv", false},
		{"Breaking Bad", "Breaking.Bad.S01E01.720p.mkv", true},
		{"Breaking Bad", "Better.Call.Saul.S01E01.mkv", false},
		{"The Office", "The.Office.US.S01E01.mkv", false},
		// No S##E## marker: can't extract title, let it through.
		{"Dragon Ball", "001 - The Secret Of The Dragon Balls", true},
	}
	for _, tt := range tests {
		got := releaseNameMatchesTitle(tt.title, tt.release)
		if got != tt.want {
			t.Errorf("releaseNameMatchesTitle(%q, %q) = %v, want %v",
				tt.title, tt.release, got, tt.want)
		}
	}
}

// --- isSeasonPack ---

func TestIsSeasonPack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"season only S04", "Show.S04.Complete.720p-GRP", true},
		{"season only S01", "Show.S01.1080p.BluRay-GRP", true},
		{"season+episode S01E01", "Show.S01E01.720p-GRP", false},
		{"season+episode S04E08", "Show.S04E08.WEB-DL-GRP", false},
		{"no season marker", "Movie.2024.BluRay.1080p-GRP", false},
		{"empty string", "", false},
		{"lowercase s01", "show.s01.complete-grp", true},
		{"lowercase s01e01", "show.s01e01.720p-grp", false},
		{"multi-digit season S12", "Show.S12.Complete-GRP", true},
		{"multi-digit episode S01E101", "Show.S01E101.720p-GRP", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scoring.IsSeasonPack(tt.input)
			if got != tt.want {
				t.Errorf("scoring.IsSeasonPack(%q) = %v, want %v",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- matchBreakdown ---

func TestMatchBreakdown_all_categories(t *testing.T) {
	t.Parallel()

	scores := &api.DefaultScores
	matches := api.MatchSet{
		Hash: true, Source: true, ReleaseGroup: true,
		StreamingService: true, VideoCodec: true, HDR: true,
		Edition: true, SeasonPack: true,
	}

	got := matchBreakdown(scores, matches)

	wantKeys := []string{
		"hash", "source", "release_group", "streaming_service",
		"video_codec", "hdr", "edition", "season_pack",
	}
	for _, key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Errorf("matchBreakdown() missing key %q", key)
		}
	}
	if got["hash"] != scores.Hash {
		t.Errorf("matchBreakdown()[hash] = %d, want %d", got["hash"], scores.Hash)
	}
}

func TestMatchBreakdown_empty_matches(t *testing.T) {
	t.Parallel()
	scores := &api.DefaultScores
	got := matchBreakdown(scores, api.MatchSet{})
	if len(got) != 0 {
		t.Errorf("matchBreakdown(empty) = %v, want empty map", got)
	}
}

func TestMatchBreakdown_hash_only(t *testing.T) {
	t.Parallel()
	scores := &api.DefaultScores
	matches := api.MatchSet{Hash: true}
	got := matchBreakdown(scores, matches)
	if len(got) != 1 {
		t.Errorf("matchBreakdown(hash only) has %d keys, want 1", len(got))
	}
	if got["hash"] != scores.Hash {
		t.Errorf("matchBreakdown(hash only)[hash] = %d, want %d", got["hash"], scores.Hash)
	}
}

func TestMatchBreakdown_false_match_excluded(t *testing.T) {
	t.Parallel()
	scores := &api.DefaultScores
	matches := api.MatchSet{Source: true}
	got := matchBreakdown(scores, matches)
	if _, ok := got["release_group"]; ok {
		t.Error("matchBreakdown() included false match for release_group")
	}
	if _, ok := got["source"]; !ok {
		t.Error("matchBreakdown() missing true match for source")
	}
}

// --- filterByVariant: forced and hi variant paths ---

func TestFilterByVariant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		variant      api.Variant
		results      []api.Subtitle
		wantLen      int
		wantFallback bool
	}{
		{
			name: "forced_keeps_only_forced",
			results: []api.Subtitle{
				{Provider: "os", ReleaseName: "Regular", HearingImp: false, Forced: false},
				{Provider: "os", ReleaseName: "HI-Sub", HearingImp: true, Forced: false},
				{Provider: "os", ReleaseName: "Forced-Sub", Forced: true},
			},
			variant: "forced", wantLen: 1, wantFallback: false,
		},
		{
			name: "forced_empty_when_none",
			results: []api.Subtitle{
				{Provider: "os", ReleaseName: "Regular"},
				{Provider: "os", ReleaseName: "HI-Sub", HearingImp: true},
			},
			variant: "forced", wantLen: 0, wantFallback: false,
		},
		{
			name: "hi_keeps_only_hi",
			results: []api.Subtitle{
				{Provider: "os", ReleaseName: "Regular"},
				{Provider: "os", ReleaseName: "HI-Sub", HearingImp: true},
				{Provider: "os", ReleaseName: "Forced-HI", HearingImp: true, Forced: true},
			},
			variant: "hi", wantLen: 1, wantFallback: false,
		},
		{
			name: "hi_excludes_forced_hi",
			results: []api.Subtitle{
				{Provider: "os", ReleaseName: "Forced-HI", HearingImp: true, Forced: true},
			},
			variant: "hi", wantLen: 0, wantFallback: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, fallback := filterByVariant(tc.results, tc.variant)
			if len(got) != tc.wantLen {
				t.Errorf("filterByVariant(%q) returned %d results, want %d", tc.variant, len(got), tc.wantLen)
			}
			if fallback != tc.wantFallback {
				t.Errorf("filterByVariant(%q) fallback = %v, want %v", tc.variant, fallback, tc.wantFallback)
			}
		})
	}
}

// --- episodeNumberMatch: scene and absolute numbering ---

func TestEpisodeNumberMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		req        *api.SearchRequest
		name       string
		subSeason  int
		subEpisode int
		want       bool
	}{
		{
			name: "scene_episode", subSeason: 2, subEpisode: 10,
			req:  &api.SearchRequest{Season: 1, Episode: 5, SceneEpisode: 10, SceneSeason: 2},
			want: true,
		},
		{
			name: "scene_no_match", subSeason: 3, subEpisode: 5,
			req:  &api.SearchRequest{Season: 1, Episode: 5, SceneEpisode: 10, SceneSeason: 2},
			want: false,
		},
		{
			name: "scene_episode_inherits_season", subSeason: 1, subEpisode: 10,
			req:  &api.SearchRequest{Season: 1, Episode: 5, SceneEpisode: 10, SceneSeason: 0},
			want: true,
		},
		{
			name: "absolute_episode", subSeason: 1, subEpisode: 50,
			req:  &api.SearchRequest{Season: 1, Episode: 5, AbsoluteEpisode: 50, SceneSeason: 0},
			want: true,
		},
		{
			name: "absolute_with_scene_season", subSeason: 3, subEpisode: 50,
			req:  &api.SearchRequest{Season: 1, Episode: 5, AbsoluteEpisode: 50, SceneSeason: 3},
			want: true,
		},
		{
			name: "no_match", subSeason: 2, subEpisode: 10,
			req:  &api.SearchRequest{Season: 1, Episode: 5},
			want: false,
		},
		{
			name: "sub_season_zero_matches_any", subSeason: 0, subEpisode: 7,
			req:  &api.SearchRequest{Season: 3, Episode: 7},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := episodeNumberMatch(tt.subSeason, tt.subEpisode, tt.req)
			if got != tt.want {
				t.Errorf("episodeNumberMatch(%d, %d) = %v, want %v", tt.subSeason, tt.subEpisode, got, tt.want)
			}
		})
	}
}

func TestIdentityOK(t *testing.T) {
	t.Parallel()
	tests := []struct {
		sub  *api.Subtitle
		req  *api.SearchRequest
		name string
		want bool
	}{
		{
			name: "movie_rejects_episode_subtitle",
			sub:  &api.Subtitle{Season: 1, Episode: 1},
			req:  &api.SearchRequest{MediaType: "movie", Title: "Test"},
			want: false,
		},
		{
			name: "release_season_mismatch_rejected",
			sub:  &api.Subtitle{ReleaseName: "Show.S04E01.720p-GRP"},
			req:  &api.SearchRequest{MediaType: "episode", Title: "Show", Season: 3, Episode: 8},
			want: false,
		},
		{
			name: "release_season_match_accepted",
			sub:  &api.Subtitle{ReleaseName: "Show.S03E08.720p-GRP"},
			req:  &api.SearchRequest{MediaType: "episode", Title: "Show", Season: 3, Episode: 8},
			want: true,
		},
		{
			name: "no_season_marker_passes",
			sub:  &api.Subtitle{ReleaseName: "001 - Show Episode Title"},
			req:  &api.SearchRequest{MediaType: "episode", Title: "Show", Season: 1, Episode: 1},
			want: true,
		},
		{
			name: "provider_title_mismatch_rejected",
			sub:  &api.Subtitle{Title: "Wrong Show"},
			req:  &api.SearchRequest{MediaType: "episode", Title: "My Show"},
			want: false,
		},
		{
			name: "provider_title_match_accepted",
			sub:  &api.Subtitle{Title: "My Show"},
			req:  &api.SearchRequest{MediaType: "episode", Title: "My Show"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := identityOK(tt.sub, tt.req)
			if got != tt.want {
				t.Errorf("identityOK() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIdentityTitleOK(t *testing.T) {
	t.Parallel()
	tests := []struct {
		sub  *api.Subtitle
		req  *api.SearchRequest
		name string
		want bool
	}{
		{
			name: "id_matched_bypasses_title_check",
			sub:  &api.Subtitle{MatchedBy: "imdb", Title: "Completely Different Show", Season: 1, Episode: 1},
			req:  &api.SearchRequest{MediaType: "episode", Title: "My Show", Season: 1, Episode: 1},
			want: true,
		},
		{
			name: "episode_title_mismatch_rejected",
			sub:  &api.Subtitle{MatchedBy: "title", Title: "Wrong Episode Title", Season: 1, Episode: 1},
			req:  &api.SearchRequest{MediaType: "episode", Title: "My Show", EpisodeTitle: "Correct Episode Title", Season: 1, Episode: 1},
			want: false,
		},
		{
			name: "episode_title_matches_series_title",
			sub:  &api.Subtitle{MatchedBy: "title", Title: "My Show", Season: 1, Episode: 1},
			req:  &api.SearchRequest{MediaType: "episode", Title: "My Show", EpisodeTitle: "Pilot", Season: 1, Episode: 1},
			want: true,
		},
		{
			name: "no_episode_title_validates_series",
			sub:  &api.Subtitle{MatchedBy: "title", Title: "Wrong Show", Season: 1, Episode: 1},
			req:  &api.SearchRequest{MediaType: "episode", Title: "My Show", Season: 1, Episode: 1},
			want: false,
		},
		{
			name: "title_matched_no_title_validates_release",
			sub:  &api.Subtitle{MatchedBy: "title", ReleaseName: "Wrong.Show.S01E01.720p-GRP", Season: 1, Episode: 1},
			req:  &api.SearchRequest{MediaType: "episode", Title: "My Show", Season: 1, Episode: 1},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := identityTitleOK(tt.sub, tt.req)
			if got != tt.want {
				t.Errorf("identityTitleOK() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- anyTitleMatches / anyReleaseNameMatches with alternatives ---

func TestAnyTitleMatches_alternative_title(t *testing.T) {
	t.Parallel()
	req := &api.SearchRequest{Title: "Dragon Ball", AlternativeTitles: []string{"Dragonball", "DB"}}
	if !anyTitleMatches(req, "Dragonball") {
		t.Error("anyTitleMatches(Dragonball) = false, want true (alternative)")
	}
	if anyTitleMatches(req, "Naruto") {
		t.Error("anyTitleMatches(Naruto) = true, want false")
	}
}

func TestAnyReleaseNameMatches_alternative_title(t *testing.T) {
	t.Parallel()
	req := &api.SearchRequest{Title: "Dragon Ball", AlternativeTitles: []string{"Dragonball"}}
	if !anyReleaseNameMatches(req, "Dragonball.S01E01.720p-GRP") {
		t.Error("anyReleaseNameMatches(alt title) = false, want true")
	}
}

// --- releaseNameMatchesTitle ---

func TestReleaseNameMatchesTitle_sequel_indicators(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		title   string
		release string
		want    bool
	}{
		{"rejects Z sequel", "Dragon Ball", "Dragon Ball Z 001", false},
		{"rejects GT sequel", "Dragon Ball", "Dragon Ball GT 001", false},
		{"rejects Super sequel", "Dragon Ball", "Dragon Ball Super 001", false},
		{"rejects Kai sequel", "Dragon Ball", "Dragon Ball Kai 001", false},
		{"accepts year suffix", "Dragon Ball", "Dragon Ball 2024", true},
		{"accepts mid-word match", "Dragon Ball", "Dragon Balls 001", true},
		{"accepts exact match no suffix", "Dragon Ball", "Dragon Ball", true},
		{"rejects word boundary before", "Dragon Ball", "XDragon Ball 001", false},
		{"accepts title at start", "Dragon Ball", "Dragon Ball 720p", true},
		{"empty title passes through", "", "Some Release Name", true},
		{"empty release passes through", "Dragon Ball", "", true},
		{"title not in release", "Breaking Bad", "Naruto 001 720p", false},
		{"title at end with trailing space", "Dragon Ball", "[720p] Dragon Ball ", true},
		{"title at exact end", "Dragon Ball", "001 Dragon Ball", true},
		{"title followed by only spaces", "Dragon Ball", "Dragon Ball   ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := releaseNameMatchesTitle(tt.title, tt.release)
			if got != tt.want {
				t.Errorf("releaseNameMatchesTitle(%q, %q) = %v, want %v",
					tt.title, tt.release, got, tt.want)
			}
		})
	}
}

// --- titlesMatch ---

func TestTitlesMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"empty_requested", "", "Some Title", true},
		{"empty_candidate", "Some Title", "", true},
		{"both_empty", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := titlesMatch(tc.a, tc.b); got != tc.want {
				t.Errorf("titlesMatch(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// --- extractReleaseSeason ---

func TestExtractReleaseSeason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"no_marker", "Movie 2024 BluRay", 0},
		{"with_episode", "Show.S03E08.720p", 3},
		{"season_only", "Show.S04.Complete.720p", 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractReleaseSeason(tc.input); got != tc.want {
				t.Errorf("extractReleaseSeason(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeTitle_Idempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.String().Draw(t, "title")
		once := normalizeTitle(input)
		twice := normalizeTitle(once)
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", input, once, twice)
		}
	})
}
