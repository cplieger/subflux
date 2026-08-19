package api

import (
	"context"
	"time"

	"github.com/cplieger/arrapi/v2"
)

// This file contains implementation/provider contracts: interfaces that
// implementations provide (Provider, ProviderRegistry, ArrClient,
// Scorer, SubtitleProcessor, etc.).
// Consumer contracts live in interfaces.go.
//
// Note: MetricsRecorder uses consumer-side placement (Go idiom): narrow
// interfaces are defined at each consumer site (search.SearchMetrics,
// scanning.ScanMetrics, polling.PollerMetrics, queryhandlers.MetricsReader,
// server.ServerMetrics). The concrete *obs.Metrics satisfies all via
// structural typing.
// WireFunc lives in internal/wiring/ as wiring.Func.

// --- Provider ---

// Provider is the interface all subtitle providers must implement.
type Provider interface {
	// Name returns the provider identifier (e.g. "opensubtitles", "yifysubtitles").
	Name() ProviderID

	// Search finds subtitles matching the request.
	Search(ctx context.Context, req *SearchRequest) ([]Subtitle, error)

	// Download fetches the subtitle content for the given search result.
	//
	// A subtitle the upstream no longer holds is reported as an error
	// wrapping ErrSubtitleAbsent, never as a successful download of zero
	// bytes: absence and a truncated or corrupt payload are different
	// operator problems, and returning nil bytes with a nil error made them
	// read the same. Implementations therefore never return (nil, nil) —
	// every return either carries subtitle content or names why it does not.
	Download(ctx context.Context, sub *Subtitle) ([]byte, error)
}

// ShowSubtitleQuery names the two values a show-level count is taken over.
// They are both free-form strings that no call site can tell apart by shape,
// so they are named rather than positional: a transposed pair would query a
// language as if it were a show and answer zero, which the caller reads as a
// legitimate "this show has almost no subtitles" and acts on by skipping the
// whole series.
type ShowSubtitleQuery struct {
	// ImdbID is the show's IMDb identifier ("tt0903747").
	ImdbID string
	// Language is the subtitle language, in subflux's internal code space.
	Language string
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
