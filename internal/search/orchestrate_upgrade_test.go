package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/scorer"
)

// --- SearchTargets upgrade behavior ---

func TestSearchTargets_upgrade(t *testing.T) {
	t.Parallel()

	type upgradeCase struct {
		mediaImported   time.Time
		name            string
		providerResults []api.Subtitle
		providerData    []byte
		score           int
		upgradeWindow   int
		wantPaths       int
		found           bool
		upgradeEnabled  bool
		adaptiveEnabled bool
		wantSuccess     bool
		wantFailure     bool
	}

	// Pre-compute a "same score" value for the same_score_skips case.
	sameScoreReq := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
	sameScoreVideo := videoInfoFromRequest(sameScoreReq)
	sameScoreResults := []api.Subtitle{{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"}}
	sameScored := scoreResults(scorer.New(&api.DefaultScores), &sameScoreVideo, sameScoreResults, noPriority)
	sameScore := sameScored[0].score

	// Pre-compute a "no improvement" score.
	noImpReq := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123", ReleaseName: "Movie-GRP"}
	noImpVideo := videoInfoFromRequest(noImpReq)
	noImpResults := []api.Subtitle{{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "title", Language: "fr"}}
	noImpScored := scoreResults(scorer.New(&api.DefaultScores), &noImpVideo, noImpResults, noPriority)
	noImpScore := noImpScored[0].score

	tests := []upgradeCase{
		{
			name: "better_score_downloads", score: 10, mediaImported: time.Now(), found: true,
			upgradeEnabled: true, upgradeWindow: 7,
			providerResults: []api.Subtitle{{Provider: "test", ReleaseName: "Movie.2024.BluRay.x264-GRP", MatchedBy: "imdb", Language: "fr"}},
			providerData:    []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nBetter\r\n"),
			wantPaths:       1, wantSuccess: true,
		},
		{
			name: "same_score_skips", score: sameScore, mediaImported: time.Now(), found: true,
			upgradeEnabled: true, upgradeWindow: 7,
			providerResults: sameScoreResults,
			providerData:    []byte("sub data"),
			wantPaths:       0,
		},
		{
			name: "disabled_skips", score: 50, mediaImported: time.Now(), found: true,
			upgradeEnabled:  false,
			providerResults: []api.Subtitle{{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"}},
			providerData:    []byte("better sub"),
			wantPaths:       0,
		},
		{
			name: "outside_window_skips", score: 50, mediaImported: time.Now().AddDate(0, 0, -30), found: true,
			upgradeEnabled: true, upgradeWindow: 7,
			providerResults: []api.Subtitle{{Provider: "test", ReleaseName: "Movie-GRP", MatchedBy: "imdb", Language: "fr"}},
			providerData:    []byte("better sub"),
			wantPaths:       0,
		},
		{
			name: "no_results_skips_backoff", score: 50, mediaImported: time.Now(), found: true,
			upgradeEnabled: true, upgradeWindow: 7, adaptiveEnabled: true,
			providerResults: nil,
			wantPaths:       0, wantFailure: false,
		},
		{
			name: "no_improvement_skips_backoff", score: noImpScore + 10, mediaImported: time.Now(), found: true,
			upgradeEnabled: true, upgradeWindow: 7, adaptiveEnabled: true,
			providerResults: noImpResults,
			providerData:    []byte("sub data"),
			wantPaths:       0, wantFailure: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			videoPath := filepath.Join(dir, "movie.mkv")

			srtPath := filepath.Join(dir, "movie.fr.srt")
			if err := os.WriteFile(srtPath, []byte("existing"), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			ms := &mockStoreWithScore{
				score: tc.score, mediaImported: tc.mediaImported, found: tc.found,
			}
			mc := &mockConfig{
				searchCfg: api.SearchConfig{
					UpgradeEnabled:    tc.upgradeEnabled,
					UpgradeWindowDays: tc.upgradeWindow,
				},
				adaptiveCfg: api.AdaptiveConfig{Enabled: tc.adaptiveEnabled},
				minScore:    0,
			}
			p := &mockProvider{
				name:    "test",
				results: tc.providerResults,
				data:    tc.providerData,
			}
			e := newEngine([]api.Provider{p}, ms, mc, nil, scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

			req := &api.SearchRequest{
				MediaType:   "movie",
				ImdbID:      "tt123",
				ReleaseName: "Movie-GRP",
			}
			targets := []api.SubtitleTarget{{Code: "fr"}}

			result, err := e.SearchTargets(context.Background(), req, videoPath, targets)
			if err != nil {
				t.Fatalf("SearchTargets() unexpected error: %v", err)
			}
			if len(result.Paths) != tc.wantPaths {
				t.Errorf("SearchTargets() returned %d paths, want %d", len(result.Paths), tc.wantPaths)
			}
			if tc.wantSuccess && !ms.successCalled {
				t.Error("SaveDownload not called, want called")
			}
			if tc.wantFailure && !ms.failureCalled {
				t.Error("RecordNoResult not called, want called")
			}
			if !tc.wantFailure && ms.failureCalled {
				t.Error("RecordNoResult called, want not called")
			}
		})
	}
}
