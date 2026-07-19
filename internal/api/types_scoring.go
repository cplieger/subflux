package api

// --- Scorer types (canonical definitions) ---

// Scores defines the weight for each release attribute match type.
//
// Identity fields (series, season, episode, title, year) are not
// scored here; they are enforced by the provider query (identity
// gate). Resolution is excluded; its signal is captured by source +
// release_group.
//
// Movie-only: Edition. For episodes, edition=0 and season_pack takes
// its place. Applicable non-hash weights sum to 98 for each media
// type. Hash match bypasses attribute scoring entirely (score = 100).
type Scores struct {
	Hash             int `json:"hash" yaml:"hash"`
	Source           int `json:"source" yaml:"source"`
	ReleaseGroup     int `json:"release_group" yaml:"release_group"`
	StreamingService int `json:"streaming_service" yaml:"streaming_service"`
	VideoCodec       int `json:"video_codec" yaml:"video_codec"`
	HDR              int `json:"hdr" yaml:"hdr"`
	Edition          int `json:"edition" yaml:"edition"`         // movies only
	SeasonPack       int `json:"season_pack" yaml:"season_pack"` // episodes only
}

// DefaultScores for release attribute matching.
//
// Movie (98):   source=28, release_group=23, streaming_service=14, edition=15,    video_codec=10, hdr=8.
// Episode (98): source=28, release_group=23, streaming_service=14, season_pack=15, video_codec=10, hdr=8.
//
// Hash match = 100 directly, bypasses attribute scoring.
var DefaultScores = Scores{
	Hash:             100,
	Source:           28,
	ReleaseGroup:     23,
	StreamingService: 14,
	VideoCodec:       10,
	HDR:              8,
	Edition:          15,
	SeasonPack:       15,
}

// MatchSet tracks which attributes matched between a subtitle and a video.
// Each field corresponds to a release attribute; struct fields make invalid
// keys impossible at compile time.
type MatchSet struct {
	Hash             bool
	Source           bool
	ReleaseGroup     bool
	StreamingService bool
	VideoCodec       bool
	HDR              bool
	Edition          bool
	SeasonPack       bool
	SeriesIMDB       bool
	IMDB             bool
}

// Keys returns the names of matched attributes (for debug logging and JSON breakdown).
func (m MatchSet) Keys() []string {
	var keys []string
	if m.Hash {
		keys = append(keys, "hash")
	}
	if m.Source {
		keys = append(keys, "source")
	}
	if m.ReleaseGroup {
		keys = append(keys, "release_group")
	}
	if m.StreamingService {
		keys = append(keys, "streaming_service")
	}
	if m.VideoCodec {
		keys = append(keys, "video_codec")
	}
	if m.HDR {
		keys = append(keys, "hdr")
	}
	if m.Edition {
		keys = append(keys, "edition")
	}
	if m.SeasonPack {
		keys = append(keys, "season_pack")
	}
	if m.SeriesIMDB {
		keys = append(keys, "series_imdb_id")
	}
	if m.IMDB {
		keys = append(keys, "imdb_id")
	}
	return keys
}

// VideoInfo describes the video file for scoring purposes.
// Only MediaType and ReleaseGroup are used by the match builder.
type VideoInfo struct {
	ReleaseGroup string    // Full release name for parsing.
	MediaType    MediaType // "episode" or "movie"
}

// SubtitleInfo describes a subtitle result for scoring purposes.
type SubtitleInfo struct {
	HashVerifiable bool
}

// ScoreTier is a typed string for release quality tier labels.
type ScoreTier string

// Tier constants for release quality scoring.
const (
	TierExcellent  ScoreTier = "excellent"  // 80+
	TierGood       ScoreTier = "good"       // 50+
	TierAcceptable ScoreTier = "acceptable" // 20+
	TierMinimal    ScoreTier = "minimal"    // 1+
	TierNone       ScoreTier = "none"       // 0
)

// ScoreResult holds the output of a score simulation.
type ScoreResult struct {
	Tier        ScoreTier // Quality tier label (excellent/good/acceptable/minimal/none).
	Score       int       // Final score including hash match bonus.
	ScoreNoHash int       // Score from release attributes only, excluding hash match.
}
