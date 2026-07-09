package api

import (
	"context"
	"time"

	"github.com/cplieger/arrapi"
)

// This file contains implementation/provider contracts: interfaces that
// implementations provide (Provider, ProviderRegistry, ArrClient,
// Scorer, SubtitleProcessor, etc.).
// Consumer contracts live in interfaces.go.
//
// Note: MetricsRecorder uses consumer-side placement (Go idiom): narrow
// interfaces are defined at each consumer site (search.SearchMetrics,
// scanning.ScanMetrics, polling.PollerMetrics, queryhandlers.MetricsReader,
// server.ServerMetrics). The concrete *metrics.Metrics satisfies all via
// structural typing.
// WireFunc lives in internal/wiring/ as wiring.Func.

// --- Provider ---

// Transient is implemented by errors that can classify themselves as
// retryable (transient server/network failures) vs permanent. Used by
// retry logic to decide whether to retry without importing concrete
// error packages.
type Transient interface {
	IsTransient() bool
}

// Provider is the interface all subtitle providers must implement.
type Provider interface {
	// Name returns the provider identifier (e.g. "opensubtitles", "yifysubtitles").
	Name() ProviderID

	// Search finds subtitles matching the request.
	Search(ctx context.Context, req *SearchRequest) ([]Subtitle, error)

	// Download fetches the subtitle content for the given search result.
	Download(ctx context.Context, sub *Subtitle) ([]byte, error)
}

// ShowSubtitleCounter can count total subtitles for a show+language without
// specifying season/episode. Used for show-level pre-checks: if a show has
// very few subtitles relative to its episode count, skip the entire series.
// Only providers with show-level query support implement this (OpenSubtitles).
type ShowSubtitleCounter interface {
	// CountShowSubtitles returns the total number of subtitles available for
	// a show in the given language. The request should have ImdbID set and
	// Season/Episode set to 0.
	CountShowSubtitles(ctx context.Context, imdbID, lang string) (int, error)
}

// CacheClearer is an optional interface for providers that cache download
// data (e.g. season pack zips). Called after scan completion to free memory.
// Providers implementing this get compile-time verification via
// var _ api.CacheClearer = (*Provider)(nil).
type CacheClearer interface {
	ClearCache()
}

// --- Provider Registry ---

// ProviderRegistry manages provider factories and schema metadata.
type ProviderRegistry interface {
	// LoadAll instantiates providers from the given config map, skipping
	// unconfigured entries. Returns an error if a configured provider fails.
	LoadAll(ctx context.Context, configs map[ProviderID]ProviderCfg) ([]Provider, error)
	// ProviderNames returns all registered provider names in priority order.
	ProviderNames() []ProviderID
	// Schema returns the UI label and settings fields for a named provider.
	Schema(name ProviderID) (label string, fields []ProviderSchemaField)
}

// --- Scoring ---

// Scorer evaluates subtitle matches against video metadata.
type Scorer interface {
	// Score computes a quality score for a subtitle match. Returns the full
	// score (including hash bonus) and the release-attribute-only score.
	Score(video *VideoInfo, sub SubtitleInfo, matches MatchSet) (score, scoreNoHash int)
	// ScoreToTier maps a numeric score to a human-readable tier label
	// (excellent/good/acceptable/minimal/none) based on media type thresholds.
	ScoreToTier(score int, mediaType MediaType) ScoreTier
}

// --- Arr clients ---
//
// arrapi splits Sonarr and Radarr into two concrete clients, so subflux models
// them as two role interfaces rather than one combined client. A single
// subflux instance holds one SonarrClient and one RadarrClient; calling a movie
// method on a series client is a compile error, not a runtime 404.
// *arrsvc.Sonarr and *arrsvc.Radarr satisfy these structurally. By-ID getters
// return values (an absent ID surfaces as an IsNotFound error, not a nil
// pointer), matching arrapi. GetHistorySince is variadic (no event types = all).

// SonarrClient is the Sonarr-side surface subflux consumes: library reads,
// per-item lookups, import-history polling, wanted-episode iteration,
// exclude-tag resolution, and a post-download rescan.
type SonarrClient interface {
	Ping(ctx context.Context) error
	GetSeries(ctx context.Context) ([]arrapi.Series, error)
	GetEpisodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error)
	GetSeriesByID(ctx context.Context, id int) (arrapi.Series, error)
	GetEpisodeByID(ctx context.Context, id int) (arrapi.Episode, error)
	GetHistorySince(ctx context.Context, since time.Time, eventTypes ...arrapi.EventType) ([]arrapi.HistoryRecord, error)
	GetWantedEpisodes(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(arrapi.Series, arrapi.Episode) error) error
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RescanSeries(ctx context.Context, seriesID int) error
}

// RadarrClient is the Radarr-side surface subflux consumes: library reads,
// per-item lookups, import-history polling, wanted-movie iteration, exclude-tag
// resolution, and a post-download rescan.
type RadarrClient interface {
	Ping(ctx context.Context) error
	GetMovies(ctx context.Context) ([]arrapi.Movie, error)
	GetMovieByID(ctx context.Context, id int) (arrapi.Movie, error)
	GetHistorySince(ctx context.Context, since time.Time, eventTypes ...arrapi.EventType) ([]arrapi.HistoryRecord, error)
	GetWantedMovies(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(arrapi.Movie) error) error
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RescanMovie(ctx context.Context, movieID int) error
}

// --- Config loading ---

// ConfigLoader parses and validates config from raw YAML bytes.
type ConfigLoader func(data []byte) (ConfigProvider, error)

// --- Schema ---

// SchemaFunc returns the full configuration schema for the UI.
type SchemaFunc func(providers []ProviderSchema) []SchemaSection

// --- Subtitle processing ---

// SubtitleCue represents a single subtitle entry with timing.
type SubtitleCue struct {
	Text  string
	Start time.Duration
	End   time.Duration
}

// AudioSyncResult holds the output of an audio-based sync operation.
type AudioSyncResult struct {
	Method     string
	Cues       []SubtitleCue
	Offset     int64   // milliseconds
	Confidence float64 // 0.0 to 1.0
	Applied    bool    // true if sync was applied and should be saved
}

// SubtitleProcessor provides low-level SRT manipulation operations.
// Used by sync handlers to avoid importing the subsync package directly.
type SubtitleProcessor interface {
	// NormalizeEncoding converts subtitle data to UTF-8 from detected encoding.
	NormalizeEncoding(data []byte) []byte
	// ParseSRT parses SRT subtitle data into individual cues.
	ParseSRT(data []byte) ([]SubtitleCue, error)
	// WriteSRT serializes cues back to SRT format.
	WriteSRT(cues []SubtitleCue) ([]byte, error)
	// ShiftCues applies a timing offset to all cues.
	ShiftCues(cues []SubtitleCue, offset time.Duration) []SubtitleCue
	// SyncFromAudio runs audio-based sync on subtitle data against the video.
	SyncFromAudio(ctx context.Context, data []byte, videoPath, subtitlePath string) AudioSyncResult
}
