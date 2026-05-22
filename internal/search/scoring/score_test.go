package scoring

import (
	"testing"

	"subflux/internal/api"
)

func TestBuildMatches(t *testing.T) {
	t.Parallel()

	baseDeps := MatchDeps{
		ParseRelease: func(s string) ReleaseInfo {
			// Simple parser for test: returns fields based on known patterns.
			switch s {
			case "":
				return ReleaseInfo{}
			default:
				return ReleaseInfo{
					Source:       "bluray",
					VideoCodec:   "x264",
					ReleaseGroup: "GRP",
				}
			}
		},
		CompareSource: func(m *api.MatchSet, videoSrc, subSrc string) {
			if videoSrc != "" && subSrc != "" && videoSrc == subSrc {
				m.Source = true
			}
		},
		IsSeasonPack: func(string) bool { return false },
	}

	tests := []struct {
		name  string
		video *api.VideoInfo
		sub   *api.Subtitle
		deps  MatchDeps
		check func(t *testing.T, got api.MatchSet)
	}{
		{
			name:  "hash_match",
			video: &api.VideoInfo{MediaType: api.MediaTypeMovie, ReleaseGroup: "Movie.2024.BluRay.x264-GRP"},
			sub:   &api.Subtitle{ReleaseName: "Movie.2024.BluRay.x264-GRP.srt", MatchedBy: api.MatchByHash},
			deps:  baseDeps,
			check: func(t *testing.T, got api.MatchSet) {
				if !got.Hash {
					t.Error("expected Hash to be true")
				}
				if !got.ReleaseGroup {
					t.Error("expected ReleaseGroup to be true")
				}
				if !got.VideoCodec {
					t.Error("expected VideoCodec to be true")
				}
				if !got.Source {
					t.Error("expected Source to be true")
				}
			},
		},
		{
			name:  "imdb_match_movie",
			video: &api.VideoInfo{MediaType: api.MediaTypeMovie, ReleaseGroup: "Movie.2024"},
			sub:   &api.Subtitle{ReleaseName: "Movie.2024.srt", MatchedBy: api.MatchByIMDB},
			deps:  baseDeps,
			check: func(t *testing.T, got api.MatchSet) {
				if !got.IMDB {
					t.Error("expected IMDB to be true")
				}
				if got.SeriesIMDB {
					t.Error("expected SeriesIMDB to be false for movie")
				}
			},
		},
		{
			name:  "imdb_match_episode_uses_series_key",
			video: &api.VideoInfo{MediaType: api.MediaTypeEpisode, ReleaseGroup: "Show.S01E01"},
			sub:   &api.Subtitle{ReleaseName: "Show.S01E01.srt", MatchedBy: api.MatchByIMDB},
			deps:  baseDeps,
			check: func(t *testing.T, got api.MatchSet) {
				if !got.SeriesIMDB {
					t.Error("expected SeriesIMDB to be true")
				}
				if got.IMDB {
					t.Error("expected IMDB to be false for episode")
				}
			},
		},
		{
			name:  "release_group_match",
			video: &api.VideoInfo{MediaType: api.MediaTypeMovie, ReleaseGroup: "Movie.BluRay.x264-GRP"},
			sub:   &api.Subtitle{ReleaseName: "Movie.BluRay.x264-GRP.srt", MatchedBy: api.MatchByTitle},
			deps:  baseDeps,
			check: func(t *testing.T, got api.MatchSet) {
				if !got.ReleaseGroup {
					t.Error("expected ReleaseGroup to be true")
				}
				if !got.VideoCodec {
					t.Error("expected VideoCodec to be true")
				}
				if !got.Source {
					t.Error("expected Source to be true")
				}
				if got.Hash {
					t.Error("expected Hash to be false")
				}
			},
		},
		{
			name:  "season_pack_detection",
			video: &api.VideoInfo{MediaType: api.MediaTypeEpisode, ReleaseGroup: "Show.S01.Complete"},
			sub:   &api.Subtitle{ReleaseName: "Show.S01.Complete.srt", MatchedBy: api.MatchByTitle},
			deps: MatchDeps{
				ParseRelease:  baseDeps.ParseRelease,
				CompareSource: baseDeps.CompareSource,
				IsSeasonPack:  func(string) bool { return true },
			},
			check: func(t *testing.T, got api.MatchSet) {
				if !got.SeasonPack {
					t.Error("expected SeasonPack to be true")
				}
			},
		},
		{
			name:  "no_matches_when_fields_empty",
			video: &api.VideoInfo{MediaType: api.MediaTypeMovie, ReleaseGroup: ""},
			sub:   &api.Subtitle{ReleaseName: "", MatchedBy: api.MatchByTitle},
			deps: MatchDeps{
				ParseRelease:  func(string) ReleaseInfo { return ReleaseInfo{} },
				CompareSource: func(*api.MatchSet, string, string) {},
				IsSeasonPack:  func(string) bool { return false },
			},
			check: func(t *testing.T, got api.MatchSet) {
				if got != (api.MatchSet{}) {
					t.Errorf("expected empty MatchSet, got %+v", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuildMatches(tc.video, tc.sub, tc.deps)
			tc.check(t, got)
		})
	}
}

func TestMatchBreakdown(t *testing.T) {
	t.Parallel()

	scores := &api.DefaultScores

	tests := []struct {
		name    string
		matches api.MatchSet
		wantLen int
		wantKey string
		wantVal int
	}{
		{
			name:    "hash_match_returns_hash_score",
			matches: api.MatchSet{Hash: true},
			wantLen: 1,
			wantKey: "hash",
			wantVal: scores.Hash,
		},
		{
			name:    "source_match",
			matches: api.MatchSet{Source: true},
			wantLen: 1,
			wantKey: "source",
			wantVal: scores.Source,
		},
		{
			name:    "multiple_matches_accumulate",
			matches: api.MatchSet{Source: true, ReleaseGroup: true, VideoCodec: true},
			wantLen: 3,
		},
		{
			name:    "empty_matches_returns_empty",
			matches: api.MatchSet{},
			wantLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MatchBreakdown(scores, tc.matches)
			if len(got) != tc.wantLen {
				t.Errorf("len(MatchBreakdown) = %d, want %d; got %v", len(got), tc.wantLen, got)
			}
			if tc.wantKey != "" {
				if v, ok := got[tc.wantKey]; !ok || v != tc.wantVal {
					t.Errorf("MatchBreakdown[%q] = %d, want %d", tc.wantKey, v, tc.wantVal)
				}
			}
		})
	}
}
