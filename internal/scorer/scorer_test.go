package scorer

import (
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
	"pgregory.net/rapid"
)

func TestScoreToTier(t *testing.T) {
	t.Parallel()
	engine := New(&subflux.DefaultScores)

	tests := []struct {
		name  string
		want  subflux.ScoreTier
		score int
	}{
		{"excellent", subflux.TierExcellent, 80},
		{"good", subflux.TierGood, 50},
		{"acceptable", subflux.TierAcceptable, 20},
		{"minimal", subflux.TierMinimal, 5},
		{"none", subflux.TierNone, 0},
		{"above excellent", subflux.TierExcellent, 200},
		{"between good and excellent", subflux.TierGood, 70},
		{"between acceptable and good", subflux.TierAcceptable, 30},
		{"boundary just below excellent", subflux.TierGood, 79},
		{"boundary just below good", subflux.TierAcceptable, 49},
		{"boundary just below acceptable", subflux.TierMinimal, 19},
		{"boundary minimum minimal", subflux.TierMinimal, 1},
		{"negative score", subflux.TierNone, -1},
		{"large negative score", subflux.TierNone, -100},
		{"hash match 100", subflux.TierExcellent, 100},
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
	engine := New(&subflux.DefaultScores)

	sub := subflux.SubtitleInfo{HashVerifiable: true}
	matches := subflux.MatchSet{
		Hash:       true,
		VideoCodec: true,
		Source:     true,
	}

	score, scoreNoHash := engine.Score(sub, matches)

	if score != subflux.DefaultScores.Hash {
		t.Errorf("Score() with hash match = %d, want %d", score, subflux.DefaultScores.Hash)
	}
	if scoreNoHash != 0 {
		t.Errorf("ScoreNoHash with hash match = %d, want 0", scoreNoHash)
	}
}

func TestScore_hash_not_verifiable_passes_through(t *testing.T) {
	t.Parallel()
	engine := New(&subflux.DefaultScores)

	sub := subflux.SubtitleInfo{HashVerifiable: false}
	matches := subflux.MatchSet{
		Hash:   true,
		Source: true,
	}

	score, scoreNoHash := engine.Score(sub, matches)

	// Non-verifiable hash: hash weight + source weight.
	wantTotal := subflux.DefaultScores.Hash + subflux.DefaultScores.Source
	if score != wantTotal {
		t.Errorf("Score() hash not verifiable = %d, want %d", score, wantTotal)
	}
	wantNoHash := subflux.DefaultScores.Source
	if scoreNoHash != wantNoHash {
		t.Errorf("ScoreNoHash hash not verifiable = %d, want %d", scoreNoHash, wantNoHash)
	}
}

func TestScore_release_attributes_only(t *testing.T) {
	t.Parallel()
	engine := New(&subflux.DefaultScores)

	sub := subflux.SubtitleInfo{}
	matches := subflux.MatchSet{
		Source:     true,
		VideoCodec: true,
	}

	score, scoreNoHash := engine.Score(sub, matches)

	want := subflux.DefaultScores.Source + subflux.DefaultScores.VideoCodec
	if score != want {
		t.Errorf("Score() = %d, want %d", score, want)
	}
	if scoreNoHash != want {
		t.Errorf("ScoreNoHash = %d, want %d", scoreNoHash, want)
	}
}

func TestScore_identity_fields_ignored(t *testing.T) {
	t.Parallel()
	engine := New(&subflux.DefaultScores)

	sub := subflux.SubtitleInfo{}
	// Identity fields (SeriesIMDB, IMDB) are not scored by sumScores.
	matches := subflux.MatchSet{
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
	engine := New(&subflux.DefaultScores)

	sub := subflux.SubtitleInfo{}
	matches := subflux.MatchSet{}

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
	engine := New(&subflux.DefaultScores)

	sub := subflux.SubtitleInfo{}
	matches := subflux.MatchSet{Edition: true}

	score, _ := engine.Score(sub, matches)

	if score != subflux.DefaultScores.Edition {
		t.Errorf("movie edition score = %d, want %d", score, subflux.DefaultScores.Edition)
	}
}

func TestScore_edition_contributes_for_episodes(t *testing.T) {
	t.Parallel()
	engine := New(&subflux.DefaultScores)

	sub := subflux.SubtitleInfo{}
	matches := subflux.MatchSet{Edition: true}

	score, _ := engine.Score(sub, matches)

	if score != subflux.DefaultScores.Edition {
		t.Errorf("episode edition score = %d, want %d", score, subflux.DefaultScores.Edition)
	}
}

func TestScore_all_release_attribute_keys(t *testing.T) {
	t.Parallel()
	engine := New(&subflux.DefaultScores)

	tests := []struct {
		name    string
		matches subflux.MatchSet
		want    int
	}{
		{"source", subflux.MatchSet{Source: true}, subflux.DefaultScores.Source},
		{"release_group", subflux.MatchSet{ReleaseGroup: true}, subflux.DefaultScores.ReleaseGroup},
		{"streaming_service", subflux.MatchSet{StreamingService: true}, subflux.DefaultScores.StreamingService},
		{"video_codec", subflux.MatchSet{VideoCodec: true}, subflux.DefaultScores.VideoCodec},
		{"hdr", subflux.MatchSet{HDR: true}, subflux.DefaultScores.HDR},
		{"edition", subflux.MatchSet{Edition: true}, subflux.DefaultScores.Edition},
		{"season_pack", subflux.MatchSet{SeasonPack: true}, subflux.DefaultScores.SeasonPack},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sub := subflux.SubtitleInfo{}

			score, _ := engine.Score(sub, tt.matches)

			if score != tt.want {
				t.Errorf("Score(%s) = %d, want %d", tt.name, score, tt.want)
			}
		})
	}
}

func TestNew_uses_custom_weights(t *testing.T) {
	t.Parallel()
	custom := subflux.Scores{Hash: 999, Source: 50}
	engine := New(&custom)

	sub := subflux.SubtitleInfo{HashVerifiable: true}
	matches := subflux.MatchSet{Hash: true}

	score, _ := engine.Score(sub, matches)
	if score != 999 {
		t.Errorf("Score() with custom hash weight = %d, want 999", score)
	}
}

// --- Property-based tests ---

// matchSetField is a helper for PBT that maps field indices to MatchSet fields.
type matchSetField struct {
	set  func(*subflux.MatchSet)
	name string
}

var releaseFields = []matchSetField{
	{name: "release_group", set: func(m *subflux.MatchSet) { m.ReleaseGroup = true }},
	{name: "source", set: func(m *subflux.MatchSet) { m.Source = true }},
	{name: "streaming_service", set: func(m *subflux.MatchSet) { m.StreamingService = true }},
	{name: "video_codec", set: func(m *subflux.MatchSet) { m.VideoCodec = true }},
	{name: "hdr", set: func(m *subflux.MatchSet) { m.HDR = true }},
	{name: "edition", set: func(m *subflux.MatchSet) { m.Edition = true }},
	{name: "season_pack", set: func(m *subflux.MatchSet) { m.SeasonPack = true }},
}

var allFields = []matchSetField{
	{name: "hash", set: func(m *subflux.MatchSet) { m.Hash = true }},
	{name: "release_group", set: func(m *subflux.MatchSet) { m.ReleaseGroup = true }},
	{name: "source", set: func(m *subflux.MatchSet) { m.Source = true }},
	{name: "streaming_service", set: func(m *subflux.MatchSet) { m.StreamingService = true }},
	{name: "video_codec", set: func(m *subflux.MatchSet) { m.VideoCodec = true }},
	{name: "hdr", set: func(m *subflux.MatchSet) { m.HDR = true }},
	{name: "edition", set: func(m *subflux.MatchSet) { m.Edition = true }},
	{name: "season_pack", set: func(m *subflux.MatchSet) { m.SeasonPack = true }},
	{name: "series_imdb", set: func(m *subflux.MatchSet) { m.SeriesIMDB = true }},
	{name: "imdb", set: func(m *subflux.MatchSet) { m.IMDB = true }},
}

func randomMatchSet(t *rapid.T, fields []matchSetField, prefix string) subflux.MatchSet {
	var m subflux.MatchSet
	for _, f := range fields {
		if rapid.Bool().Draw(t, prefix+f.name) {
			f.set(&m)
		}
	}
	return m
}

func TestScore_always_non_negative(t *testing.T) {
	t.Parallel()
	engine := New(&subflux.DefaultScores)

	rapid.Check(t, func(t *rapid.T) {
		sub := subflux.SubtitleInfo{
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
	engine := New(&subflux.DefaultScores)
	validTiers := map[subflux.ScoreTier]bool{
		subflux.TierExcellent: true, subflux.TierGood: true, subflux.TierAcceptable: true,
		subflux.TierMinimal: true, subflux.TierNone: true,
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
	engine := New(&subflux.DefaultScores)

	rapid.Check(t, func(t *rapid.T) {
		sub := subflux.SubtitleInfo{
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
	engine := New(&subflux.DefaultScores)

	rapid.Check(t, func(t *rapid.T) {
		sub := subflux.SubtitleInfo{}

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
