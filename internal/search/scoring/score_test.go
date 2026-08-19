package scoring

import (
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
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
		CompareSource: func(m *subflux.MatchSet, videoSrc, subSrc string) {
			if videoSrc != "" && subSrc != "" && videoSrc == subSrc {
				m.Source = true
			}
		},
		IsSeasonPack: func(string) bool { return false },
	}

	tests := []struct {
		deps  MatchDeps
		video *subflux.VideoInfo
		sub   *subflux.Subtitle
		check func(t *testing.T, got subflux.MatchSet)
		name  string
	}{
		{
			name:  "hash_match",
			video: &subflux.VideoInfo{MediaType: subflux.MediaTypeMovie, ReleaseGroup: "Movie.2024.BluRay.x264-GRP"},
			sub:   &subflux.Subtitle{ReleaseName: "Movie.2024.BluRay.x264-GRP.srt", MatchedBy: subflux.MatchByHash},
			deps:  baseDeps,
			check: func(t *testing.T, got subflux.MatchSet) {
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
			video: &subflux.VideoInfo{MediaType: subflux.MediaTypeMovie, ReleaseGroup: "Movie.2024"},
			sub:   &subflux.Subtitle{ReleaseName: "Movie.2024.srt", MatchedBy: subflux.MatchByIMDB},
			deps:  baseDeps,
			check: func(t *testing.T, got subflux.MatchSet) {
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
			video: &subflux.VideoInfo{MediaType: subflux.MediaTypeEpisode, ReleaseGroup: "Show.S01E01"},
			sub:   &subflux.Subtitle{ReleaseName: "Show.S01E01.srt", MatchedBy: subflux.MatchByIMDB},
			deps:  baseDeps,
			check: func(t *testing.T, got subflux.MatchSet) {
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
			video: &subflux.VideoInfo{MediaType: subflux.MediaTypeMovie, ReleaseGroup: "Movie.BluRay.x264-GRP"},
			sub:   &subflux.Subtitle{ReleaseName: "Movie.BluRay.x264-GRP.srt", MatchedBy: subflux.MatchByTitle},
			deps:  baseDeps,
			check: func(t *testing.T, got subflux.MatchSet) {
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
			video: &subflux.VideoInfo{MediaType: subflux.MediaTypeEpisode, ReleaseGroup: "Show.S01.Complete"},
			sub:   &subflux.Subtitle{ReleaseName: "Show.S01.Complete.srt", MatchedBy: subflux.MatchByTitle},
			deps: MatchDeps{
				ParseRelease:  baseDeps.ParseRelease,
				CompareSource: baseDeps.CompareSource,
				IsSeasonPack:  func(string) bool { return true },
			},
			check: func(t *testing.T, got subflux.MatchSet) {
				if !got.SeasonPack {
					t.Error("expected SeasonPack to be true")
				}
			},
		},
		{
			name:  "no_matches_when_fields_empty",
			video: &subflux.VideoInfo{MediaType: subflux.MediaTypeMovie, ReleaseGroup: ""},
			sub:   &subflux.Subtitle{ReleaseName: "", MatchedBy: subflux.MatchByTitle},
			deps: MatchDeps{
				ParseRelease:  func(string) ReleaseInfo { return ReleaseInfo{} },
				CompareSource: func(*subflux.MatchSet, string, string) {},
				IsSeasonPack:  func(string) bool { return false },
			},
			check: func(t *testing.T, got subflux.MatchSet) {
				if got != (subflux.MatchSet{}) {
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

	scores := &subflux.DefaultScores

	tests := []struct {
		name    string
		wantKey string
		wantLen int
		wantVal int
		matches subflux.MatchSet
	}{
		{
			name:    "hash_match_returns_hash_score",
			matches: subflux.MatchSet{Hash: true},
			wantLen: 1,
			wantKey: "hash",
			wantVal: scores.Hash,
		},
		{
			name:    "source_match",
			matches: subflux.MatchSet{Source: true},
			wantLen: 1,
			wantKey: "source",
			wantVal: scores.Source,
		},
		{
			name:    "multiple_matches_accumulate",
			matches: subflux.MatchSet{Source: true, ReleaseGroup: true, VideoCodec: true},
			wantLen: 3,
		},
		{
			name:    "empty_matches_returns_empty",
			matches: subflux.MatchSet{},
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

// A matched category contributes to the breakdown only when its weight is
// strictly positive; a matched-but-zero-weight category is excluded.
func TestMatchBreakdown_excludes_zero_weight_match(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		scores  subflux.Scores
		matches subflux.MatchSet
		wantLen int
		wantKey string
		wantVal int
		present bool
	}{
		{
			name:    "zero weight matched category excluded",
			scores:  subflux.Scores{Source: 0},
			matches: subflux.MatchSet{Source: true},
			wantLen: 0,
			wantKey: "source",
			present: false,
		},
		{
			name:    "positive weight matched category included",
			scores:  subflux.Scores{Source: 28},
			matches: subflux.MatchSet{Source: true},
			wantLen: 1,
			wantKey: "source",
			wantVal: 28,
			present: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scores := tc.scores
			got := MatchBreakdown(&scores, tc.matches)
			if len(got) != tc.wantLen {
				t.Errorf("len(MatchBreakdown) = %d, want %d; got %v", len(got), tc.wantLen, got)
			}
			v, ok := got[tc.wantKey]
			if ok != tc.present {
				t.Errorf("MatchBreakdown key %q present = %v, want %v", tc.wantKey, ok, tc.present)
			}
			if tc.present && v != tc.wantVal {
				t.Errorf("MatchBreakdown[%q] = %d, want %d", tc.wantKey, v, tc.wantVal)
			}
		})
	}
}

// TestCategories_row_coherence pins the merged category table that the match
// builder, the breakdown, and internal/scorer all derive from. With a Scores
// struct assigning a distinct sentinel weight to every category, each row's
// SetMatch must surface exactly its own key and weight in the breakdown.
// This catches accessor mismatches (e.g. edition reading the season_pack
// weight) that the default weights cannot detect because some categories
// share a value.
func TestCategories_row_coherence(t *testing.T) {
	t.Parallel()

	scores := subflux.Scores{
		Hash:             1,
		Source:           2,
		ReleaseGroup:     3,
		StreamingService: 4,
		VideoCodec:       5,
		HDR:              6,
		Edition:          7,
		SeasonPack:       8,
	}
	wantWeights := map[string]int{
		"source":            2,
		"release_group":     3,
		"streaming_service": 4,
		"video_codec":       5,
		"hdr":               6,
		"edition":           7,
		"season_pack":       8,
	}

	if len(Categories) != len(wantWeights) {
		t.Fatalf("len(Categories) = %d, want %d", len(Categories), len(wantWeights))
	}
	seen := make(map[string]bool)
	for _, c := range Categories {
		want, ok := wantWeights[c.Key]
		if !ok {
			t.Errorf("unexpected category key %q", c.Key)
			continue
		}
		if seen[c.Key] {
			t.Errorf("duplicate category key %q", c.Key)
		}
		seen[c.Key] = true

		if got := c.Weight(&scores); got != want {
			t.Errorf("Categories[%q].Weight = %d, want %d", c.Key, got, want)
		}
		var m subflux.MatchSet
		if c.Match(m) {
			t.Errorf("Categories[%q].Match(zero MatchSet) = true, want false", c.Key)
		}
		c.SetMatch(&m)
		if !c.Match(m) {
			t.Errorf("Categories[%q].SetMatch does not set the bit Match reads", c.Key)
		}
		got := MatchBreakdown(&scores, m)
		if len(got) != 1 || got[c.Key] != want {
			t.Errorf("MatchBreakdown after SetMatch(%q) = %v, want map[%s:%d]",
				c.Key, got, c.Key, want)
		}
	}
}
