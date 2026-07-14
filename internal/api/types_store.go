package api

import "time"

// --- Query parameter structs ---

// StateQuery groups the filter parameters for QueryStore.GetState.
// Using a named struct (rather than positional string/int arguments)
// makes call sites self-documenting and prevents silent swaps of
// same-typed fields like Language/Provider/Search.
type StateQuery struct {
	MediaType MediaType
	Language  string
	Provider  ProviderID
	Search    string
	Limit     int
	Offset    int
}

// ScanRecord groups the parameters for CoverageStore.RecordScanState.
type ScanRecord struct {
	MediaType MediaType
	MediaID   string
	Title     string
	AudioLang string
	Season    int
	Episode   int
}

// --- Store types (canonical, moved from store package) ---

// BackoffParams groups adaptive backoff configuration for RecordNoResult.
type BackoffParams struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
}

// DownloadRecord groups the parameters for Store.SaveDownload.
// Using a named struct (rather than positional string arguments)
// makes call sites self-documenting and prevents silent swaps of
// same-typed fields like Language/ProviderName.
type DownloadRecord struct {
	Meta         *DownloadMeta
	MediaType    MediaType
	MediaID      string
	Language     string
	Variant      Variant // subtitle variant (standard/hi/forced); empty is normalized to standard
	ProviderName ProviderID
	ReleaseName  string
	Path         string
	Score        int
}

// DownloadMeta holds optional metadata for a subtitle state record.
type DownloadMeta struct {
	Title      string
	ImdbID     string
	ReleaseTag string
	VideoPath  string // Path to the video file (for reconciliation and upgrades).
	Season     int
	Episode    int
	Manual     bool // True if user manually selected this subtitle.
}

// DownloadedRef identifies a previously-downloaded subtitle by its
// release name and provider. Returned by Store.DownloadedRefs to mark
// matching entries in the manual search popup as already on disk.
type DownloadedRef struct {
	ReleaseName string
	Provider    ProviderID
}

// StateEntry represents a subtitle state record for API responses.
type StateEntry struct {
	MediaImported time.Time  `json:"media_imported"`
	Title         string     `json:"title"`
	MediaID       string     `json:"media_id"`
	Language      string     `json:"language"`
	Variant       Variant    `json:"variant"`
	Provider      ProviderID `json:"provider"`
	Path          string     `json:"path"`
	ReleaseName   string     `json:"release_name"`
	ImdbID        string     `json:"imdb_id,omitempty"`
	MediaType     MediaType  `json:"media_type"`
	ID            int64      `json:"id"`
	Score         int        `json:"score"`
	Season        int        `json:"season,omitempty"`
	Episode       int        `json:"episode,omitempty"`
	Manual        bool       `json:"manual"`
}

// BackoffEntry represents an item in adaptive search backoff.
type BackoffEntry struct {
	LastTried time.Time  `json:"last_tried"`
	NextRetry time.Time  `json:"next_retry"`
	MediaType MediaType  `json:"media_type"`
	MediaID   string     `json:"media_id"`
	Language  string     `json:"language"`
	Provider  ProviderID `json:"provider"`
	Failures  int        `json:"failures"`
}

// ManualLockEntry represents a manually locked media+language+variant quad.
type ManualLockEntry struct {
	MediaType MediaType `json:"media_type"`
	MediaID   string    `json:"media_id"`
	Language  string    `json:"language"`
	Variant   Variant   `json:"variant"`
	Count     int       `json:"count"`
}

// EmbeddedTrack represents a subtitle track detected inside a video container.
type EmbeddedTrack struct {
	Codec           string
	Lang            string
	Name            string
	Index           int
	Forced          bool
	HearingImpaired bool
}

// ConfigDrift describes DB cleanup actions needed after a config change.
type ConfigDrift struct {
	// RemovedLanguages are language codes that were in the old config
	// but not in the new one. Their search_attempts should be cleared.
	RemovedLanguages []string

	// RemovedProviders are provider names that were enabled in the old
	// config but disabled or removed in the new one. Their
	// search_attempts should be cleared.
	RemovedProviders []ProviderID

	// AdaptiveDisabled is true when adaptive search was enabled in the
	// old config but disabled in the new one. All search_attempts
	// should be cleared.
	AdaptiveDisabled bool
}

// CleanupResult holds the outcome of a media cleanup operation.
// Used by both CleanupForMediaUpgrade and ReconcileState.
type CleanupResult struct {
	// Paths are subtitle file paths whose DB entries were removed.
	// The caller is responsible for validating and deleting files from disk.
	Paths []string
}

// ReconcileResult holds the outcome of a state reconciliation pass.
type ReconcileResult struct {
	// Deleted contains subtitle paths removed because the video is gone.
	Deleted CleanupResult

	// ResetCount is the number of entries reset for re-search because
	// the subtitle file was missing but the video still exists.
	ResetCount int64
}

// ScanStats tracks scan progress and outcomes for logging.
type ScanStats struct {
	// Pre-scan totals (from API).
	TotalSeries       int
	TotalEpisodeFiles int // Sum of episodeFileCount across all series.
	TotalMovies       int
	TotalMovieFiles   int // Movies with hasFile=true.

	// Post-scan outcomes for episodes. EpisodesSearched counts every episode
	// the scan loop processed (it drives progress reporting); the other
	// fields are its per-outcome buckets.
	EpisodesSearched  int // Episodes processed by the scan loop.
	EpisodesSkipped   int // Episodes with existing subs (no search needed).
	EpisodesFound     int // Episodes where a subtitle was downloaded.
	EpisodesNoResult  int // Episodes searched but no subtitle found.
	EpisodesBackedOff int // Episodes where every needed provider was in adaptive backoff (no query ran).
	SeriesSkipped     int // Series skipped by show-level pre-check.

	// Post-scan outcomes for movies.
	MoviesSearched  int
	MoviesSkipped   int
	MoviesFound     int
	MoviesNoResult  int
	MoviesBackedOff int
}
