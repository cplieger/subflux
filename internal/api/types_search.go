package api

import "fmt"

// --- Media ID builders ---

// MediaType is a typed string identifying the kind of media being searched.
// Use the exported constants rather than raw string literals.
type MediaType string

// Media type constants used by SearchRequest.MediaType and related helpers.
// Canonical source for all packages; import from here instead of declaring
// local unexported copies.
const (
	MediaTypeMovie   MediaType = "movie"
	MediaTypeEpisode MediaType = "episode"
)

// Valid reports whether mt is a recognized media type.
func (mt MediaType) Valid() bool {
	return mt == MediaTypeMovie || mt == MediaTypeEpisode
}

// String implements fmt.Stringer.
func (mt MediaType) String() string { return string(mt) }

// Unexported aliases for internal use within this package.
const (
	mediaTypeMovie   = MediaTypeMovie
	mediaTypeEpisode = MediaTypeEpisode
)

// --- Provider types (canonical, moved from provider package) ---

// ProviderID is a typed string identifying a subtitle provider.
// Use this instead of raw strings when passing provider names to metrics,
// backoff, and other subsystems that key on provider identity.
type ProviderID string

// String implements fmt.Stringer.
func (p ProviderID) String() string { return string(p) }

// --- Provider name constants (canonical SSOT) ---

// Provider name constants (canonical SSOT). All packages MUST reference these
// instead of declaring local string literals.
const (
	ProviderNameEmbedded      ProviderID = "embedded"
	ProviderNameMock          ProviderID = "mock"
	ProviderNameHDBits        ProviderID = "hdbits"
	ProviderNameOpenSubtitles ProviderID = "opensubtitles"
	ProviderNameSubSource     ProviderID = "subsource"
	ProviderNameAnimeTosho    ProviderID = "animetosho"
	ProviderNameYifySubtitles ProviderID = "yifysubtitles"
	ProviderNameSubDL         ProviderID = "subdl"
	ProviderNameBetaSeries    ProviderID = "betaseries"
	ProviderNameGestdown      ProviderID = "gestdown"
)

// --- Embedded provider setting keys (SSOT) ---

// EmbeddedSettingIgnorePGS is the setting key controlling PGS subtitle filtering.
const EmbeddedSettingIgnorePGS = "ignore_pgs"

// EmbeddedSettingIgnoreVobSub is the setting key controlling VobSub subtitle filtering.
const EmbeddedSettingIgnoreVobSub = "ignore_vobsub"

// EmbeddedSettingIgnoreASS is the setting key controlling ASS/SSA subtitle filtering.
const EmbeddedSettingIgnoreASS = "ignore_ass"

// SearchRequest holds the parameters for a subtitle search across providers.
type SearchRequest struct {
	MediaType         MediaType // "episode" or "movie"
	ImdbID            string    // IMDB ID; fallback for providers without TMDB/TVDB support
	Title             string
	AlternativeTitles []string // from Sonarr/Radarr alternateTitles
	EpisodeTitle      string   // Episode title from Sonarr (for identity validation across numbering schemes)
	ReleaseName       string
	VideoPath         string   // Absolute path to the video file (for embedded provider and sync)
	VideoHash         string   // OpenSubtitles hash
	AudioLang         string   // resolved audio language (for coverage tracking)
	Languages         []string // ISO 639-1 codes to search for
	TmdbID            int      // Movie TMDB ID from Radarr; 0 for episodes
	VideoSize         int64    // file size for hash-based search
	Year              int
	Season            int // 0 for movies
	Episode           int // 0 for movies
	TvdbID            int // Series TVDB ID from Sonarr; 0 for movies
	MaxResults        int // 0 = provider default (typically one page)

	// Alternate episode numbering from Sonarr (0 = not available).
	// Scene numbers come from TheXEM; absolute numbers from TVDB.
	SceneSeason     int
	SceneEpisode    int
	AbsoluteEpisode int

	// ForceUpgrade makes the search engine treat all external subtitles
	// as eligible for upgrade, regardless of the upgrade_enabled config.
	// Set by manual scan triggers (series/season search buttons).
	ForceUpgrade bool
}

// MediaLabel returns a human-readable label for log messages.
// Movies: "Inception (2010)", episodes: "Bleach (2004) - S09E15".
func (r *SearchRequest) MediaLabel() string {
	if r.Year > 0 {
		if r.MediaType == mediaTypeEpisode {
			return fmt.Sprintf("%s (%d) - S%02dE%02d", r.Title, r.Year, r.Season, r.Episode)
		}
		return fmt.Sprintf("%s (%d)", r.Title, r.Year)
	}
	if r.MediaType == mediaTypeEpisode {
		return fmt.Sprintf("%s - S%02dE%02d", r.Title, r.Season, r.Episode)
	}
	return r.Title
}

// MatchMethod is a typed string identifying how a subtitle was matched to
// the requested media. Use the exported constants rather than raw string
// literals. Follows the same pattern as MediaType and AuthMethod.
type MatchMethod string

// Match method constants. Canonical source for all packages.
const (
	MatchByHash     MatchMethod = "hash"
	MatchByIMDB     MatchMethod = "imdb"
	MatchByTitle    MatchMethod = "title"
	MatchByTVDB     MatchMethod = "tvdb"
	MatchByTMDB     MatchMethod = "tmdb"
	MatchByEmbedded MatchMethod = "embedded"
)

// String implements fmt.Stringer.
func (m MatchMethod) String() string { return string(m) }

// Compile-time assertion: MatchMethod satisfies fmt.Stringer.
var _ fmt.Stringer = MatchMethod("")

// Subtitle is a search result returned by a provider.
type Subtitle struct {
	Provider    ProviderID
	ID          string // provider-specific identifier
	Language    string // ISO 639-1 code
	ReleaseName string
	DownloadURL string
	MatchedBy   MatchMethod // how this subtitle was matched (hash, title, imdb, etc.)
	Title       string      // show/movie title from provider (for identity validation)
	Year        int         // year from provider (0 if unknown)
	Season      int         // season from provider (0 if unknown or movie)
	Episode     int         // episode from provider (0 if unknown or movie)
	Score       int
	HearingImp  bool
	Forced      bool
}

// ScoredResult is a scored subtitle for use by the manual search and CLI APIs.
type ScoredResult struct {
	Matches map[string]int // Per-category score contributions.
	Sub     Subtitle
	Score   int
}

// --- Search result ---

// SearchResult holds the outcome of a SearchTargets call.
type SearchResult struct {
	Paths           []string // Subtitle files downloaded.
	FoundLangs      []string // Language codes that had at least one subtitle downloaded.
	Searched        int      // Languages where providers were actually queried.
	Skipped         int      // Languages skipped (subs already exist, not eligible for upgrade).
	CoverageChanged bool     // True if RecordSubtitleFiles detected changes on disk.
}
