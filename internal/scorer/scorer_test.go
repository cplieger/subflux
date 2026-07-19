package scorer

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

func TestScoreToTier(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	tests := []struct {
		name  string
		want  api.ScoreTier
		score int
	}{
		{"excellent", api.TierExcellent, 80},
		{"good", api.TierGood, 50},
		{"acceptable", api.TierAcceptable, 20},
		{"minimal", api.TierMinimal, 5},
		{"none", api.TierNone, 0},
		{"above excellent", api.TierExcellent, 200},
		{"between good and excellent", api.TierGood, 70},
		{"between acceptable and good", api.TierAcceptable, 30},
		{"boundary just below excellent", api.TierGood, 79},
		{"boundary just below good", api.TierAcceptable, 49},
		{"boundary just below acceptable", api.TierMinimal, 19},
		{"boundary minimum minimal", api.TierMinimal, 1},
		{"negative score", api.TierNone, -1},
		{"large negative score", api.TierNone, -100},
		{"hash match 100", api.TierExcellent, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := engine.ScoreToTier(tt.score)
			if got != tt.want {
				t.Errorf("ScoreToTier(%d) = %q, want %q", tt.score, got, tt.want)
			}
		})
	}
}

func TestScore_hash_match_returns_100(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	sub := api.SubtitleInfo{HashVerifiable: true}
	matches := api.MatchSet{
		Hash:       true,
		VideoCodec: true,
		Source:     true,
	}

	score, scoreNoHash := engine.Score(sub, matches)

	if score != api.DefaultScores.Hash {
		t.Errorf("Score() with hash match = %d, want %d", score, api.DefaultScores.Hash)
	}
	if scoreNoHash != 0 {
		t.Errorf("ScoreNoHash with hash match = %d, want 0", scoreNoHash)
	}
}

func TestScore_hash_not_verifiable_passes_through(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	sub := api.SubtitleInfo{HashVerifiable: false}
	matches := api.MatchSet{
		Hash:   true,
		Source: true,
	}

	score, scoreNoHash := engine.Score(sub, matches)

	// Non-verifiable hash: hash weight + source weight.
	wantTotal := api.DefaultScores.Hash + api.DefaultScores.Source
	if score != wantTotal {
		t.Errorf("Score() hash not verifiable = %d, want %d", score, wantTotal)
	}
	wantNoHash := api.DefaultScores.Source
	if scoreNoHash != wantNoHash {
		t.Errorf("ScoreNoHash hash not verifiable = %d, want %d", scoreNoHash, wantNoHash)
	}
}

func TestScore_release_attributes_only(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	sub := api.SubtitleInfo{}
	matches := api.MatchSet{
		Source:     true,
		VideoCodec: true,
	}

	score, scoreNoHash := engine.Score(sub, matches)

	want := api.DefaultScores.Source + api.DefaultScores.VideoCodec
	if score != want {
		t.Errorf("Score() = %d, want %d", score, want)
	}
	if scoreNoHash != want {
		t.Errorf("ScoreNoHash = %d, want %d", scoreNoHash, want)
	}
}

func TestScore_identity_fields_ignored(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	sub := api.SubtitleInfo{}
	// Identity fields (SeriesIMDB, IMDB) are not scored by sumScores.
	matches := api.MatchSet{
		SeriesIMDB: true,
		IMDB:       true,
	}

	score, _ := engine.Score(sub, matches)

	if score != 0 {
		t.Errorf("Score() with identity-only matches = %d, want 0", score)
	}
}

func TestScore_empty_matches_returns_zero(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	sub := api.SubtitleInfo{}
	matches := api.MatchSet{}

	score, scoreNoHash := engine.Score(sub, matches)

	if score != 0 {
		t.Errorf("Score(empty matches) = %d, want 0", score)
	}
	if scoreNoHash != 0 {
		t.Errorf("ScoreNoHash(empty matches) = %d, want 0", scoreNoHash)
	}
}

func TestScore_edition_contributes_for_movies(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	sub := api.SubtitleInfo{}
	matches := api.MatchSet{Edition: true}

	score, _ := engine.Score(sub, matches)

	if score != api.DefaultScores.Edition {
		t.Errorf("movie edition score = %d, want %d", score, api.DefaultScores.Edition)
	}
}

func TestScore_edition_contributes_for_episodes(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	sub := api.SubtitleInfo{}
	matches := api.MatchSet{Edition: true}

	score, _ := engine.Score(sub, matches)

	if score != api.DefaultScores.Edition {
		t.Errorf("episode edition score = %d, want %d", score, api.DefaultScores.Edition)
	}
}

func TestScore_all_release_attribute_keys(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	tests := []struct {
		name    string
		matches api.MatchSet
		want    int
	}{
		{"source", api.MatchSet{Source: true}, api.DefaultScores.Source},
		{"release_group", api.MatchSet{ReleaseGroup: true}, api.DefaultScores.ReleaseGroup},
		{"streaming_service", api.MatchSet{StreamingService: true}, api.DefaultScores.StreamingService},
		{"video_codec", api.MatchSet{VideoCodec: true}, api.DefaultScores.VideoCodec},
		{"hdr", api.MatchSet{HDR: true}, api.DefaultScores.HDR},
		{"edition", api.MatchSet{Edition: true}, api.DefaultScores.Edition},
		{"season_pack", api.MatchSet{SeasonPack: true}, api.DefaultScores.SeasonPack},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sub := api.SubtitleInfo{}

			score, _ := engine.Score(sub, tt.matches)

			if score != tt.want {
				t.Errorf("Score(%s) = %d, want %d", tt.name, score, tt.want)
			}
		})
	}
}

func TestNew_uses_custom_weights(t *testing.T) {
	t.Parallel()
	custom := api.Scores{Hash: 999, Source: 50}
	engine := New(&custom)

	sub := api.SubtitleInfo{HashVerifiable: true}
	matches := api.MatchSet{Hash: true}

	score, _ := engine.Score(sub, matches)
	if score != 999 {
		t.Errorf("Score() with custom hash weight = %d, want 999", score)
	}
}

// --- Property-based tests ---

// matchSetField is a helper for PBT that maps field indices to MatchSet fields.
type matchSetField struct {
	set  func(*api.MatchSet)
	name string
}

var releaseFields = []matchSetField{
	{name: "release_group", set: func(m *api.MatchSet) { m.ReleaseGroup = true }},
	{name: "source", set: func(m *api.MatchSet) { m.Source = true }},
	{name: "streaming_service", set: func(m *api.MatchSet) { m.StreamingService = true }},
	{name: "video_codec", set: func(m *api.MatchSet) { m.VideoCodec = true }},
	{name: "hdr", set: func(m *api.MatchSet) { m.HDR = true }},
	{name: "edition", set: func(m *api.MatchSet) { m.Edition = true }},
	{name: "season_pack", set: func(m *api.MatchSet) { m.SeasonPack = true }},
}

var allFields = []matchSetField{
	{name: "hash", set: func(m *api.MatchSet) { m.Hash = true }},
	{name: "release_group", set: func(m *api.MatchSet) { m.ReleaseGroup = true }},
	{name: "source", set: func(m *api.MatchSet) { m.Source = true }},
	{name: "streaming_service", set: func(m *api.MatchSet) { m.StreamingService = true }},
	{name: "video_codec", set: func(m *api.MatchSet) { m.VideoCodec = true }},
	{name: "hdr", set: func(m *api.MatchSet) { m.HDR = true }},
	{name: "edition", set: func(m *api.MatchSet) { m.Edition = true }},
	{name: "season_pack", set: func(m *api.MatchSet) { m.SeasonPack = true }},
	{name: "series_imdb", set: func(m *api.MatchSet) { m.SeriesIMDB = true }},
	{name: "imdb", set: func(m *api.MatchSet) { m.IMDB = true }},
}

func randomMatchSet(t *rapid.T, fields []matchSetField, prefix string) api.MatchSet {
	var m api.MatchSet
	for _, f := range fields {
		if rapid.Bool().Draw(t, prefix+f.name) {
			f.set(&m)
		}
	}
	return m
}

func TestScore_always_non_negative(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	rapid.Check(t, func(t *rapid.T) {
		sub := api.SubtitleInfo{
			HashVerifiable: rapid.Bool().Draw(t, "hash_verifiable"),
		}

		matches := randomMatchSet(t, allFields, "")

		score, scoreNoHash := engine.Score(sub, matches)

		if score < 0 {
			t.Errorf("Score() = %d, must be >= 0", score)
		}
		if scoreNoHash < 0 {
			t.Errorf("ScoreNoHash = %d, must be >= 0", scoreNoHash)
		}
	})
}

func TestScoreToTier_always_valid(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)
	validTiers := map[api.ScoreTier]bool{
		api.TierExcellent: true, api.TierGood: true, api.TierAcceptable: true,
		api.TierMinimal: true, api.TierNone: true,
	}

	rapid.Check(t, func(t *rapid.T) {
		score := rapid.IntRange(-100, 1000).Draw(t, "score")

		tier := engine.ScoreToTier(score)

		if !validTiers[tier] {
			t.Errorf("ScoreToTier(%d) = %q, not a valid tier", score, tier)
		}
	})
}

func TestScore_scoreNoHash_leq_score(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	rapid.Check(t, func(t *rapid.T) {
		sub := api.SubtitleInfo{
			HashVerifiable: rapid.Bool().Draw(t, "hash_verifiable"),
		}

		matches := randomMatchSet(t, allFields, "")

		score, scoreNoHash := engine.Score(sub, matches)

		if scoreNoHash > score {
			t.Errorf("scoreNoHash (%d) > score (%d)", scoreNoHash, score)
		}
	})
}

func TestScore_adding_release_match_never_decreases_score(t *testing.T) {
	t.Parallel()
	engine := New(&api.DefaultScores)

	rapid.Check(t, func(t *rapid.T) {
		sub := api.SubtitleInfo{}

		baseMatches := randomMatchSet(t, releaseFields, "base_")

		extraIdx := rapid.IntRange(0, len(releaseFields)-1).Draw(t, "extra_idx")

		baseScore, _ := engine.Score(sub, baseMatches)

		extendedMatches := baseMatches
		releaseFields[extraIdx].set(&extendedMatches)

		extendedScore, _ := engine.Score(sub, extendedMatches)

		if extendedScore < baseScore {
			t.Errorf("adding %q decreased score: %d -> %d",
				releaseFields[extraIdx].name, baseScore, extendedScore)
		}
	})
}
