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

// --- Embedded subtitle policy (typed search policy, from config) ---

// EmbeddedPolicy is the typed search policy for embedded subtitle codecs,
// resolved from the top-level embedded_subtitles config section. The
// detector always returns every normalized track; this policy decides
// which codecs count as "present but not usable" for target satisfaction
// (they stay visible in coverage but trigger external search).
type EmbeddedPolicy struct {
	// IgnorePGS excludes PGS bitmap subs (Blu-ray) from target satisfaction.
	IgnorePGS bool
	// IgnoreVobSub excludes VobSub bitmap subs (DVD) from target satisfaction.
	IgnoreVobSub bool
	// IgnoreASS excludes ASS/SSA styled subs (anime) from target satisfaction.
	IgnoreASS bool
}

// SearchRequest holds the parameters for a subtitle search across providers.
type SearchRequest struct {
	MediaType         MediaType // "episode" or "movie"
	ImdbID            string    // IMDB ID; fallback for providers without TMDB/TVDB support
	Title             string
	AlternativeTitles []string // from Sonarr/Radarr alternateTitles
	EpisodeTitle      string   // Episode title from Sonarr (for identity validation across numbering schemes)
	ReleaseName       string
	VideoPath         string   // Absolute path to the video file (for embedded track detection and sync)
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
	MatchByHash  MatchMethod = "hash"
	MatchByIMDB  MatchMethod = "imdb"
	MatchByTitle MatchMethod = "title"
	MatchByTVDB  MatchMethod = "tvdb"
	MatchByTMDB  MatchMethod = "tmdb"
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

// LangOutcomeKind classifies what happened for one language group in a
// SearchTargets call. Exactly one kind applies per language.
type LangOutcomeKind string

// Language-group outcome kinds.
const (
	// LangSearched: providers were actually queried for this language.
	LangSearched LangOutcomeKind = "searched"
	// LangSkipped: nothing needed a search (covered on disk, manually
	// locked, or not eligible for upgrade).
	LangSkipped LangOutcomeKind = "skipped"
	// LangBackedOff: the language needed a search but every provider was in
	// adaptive backoff, so no query ran. Distinct from both other kinds; it
	// must never feed no-result evidence into the season tracker.
	LangBackedOff LangOutcomeKind = "backed_off"
)

// LangOutcome is the typed per-language result of a SearchTargets call. It
// replaces the former parallel FoundLangs/SearchedLangs slices and aggregate
// counters: each language carries its own classification and evidence, so
// consumers (season tracker, scan stats, coverage events) read one structure
// instead of reconstructing sets from slice pairs.
type LangOutcome struct {
	Lang     string          // ISO 639-1 language code
	Kind     LangOutcomeKind // what happened for this language group
	Paths    []string        // subtitle files downloaded for this language
	Searched int             // variant targets processed against provider results
	Skipped  int             // variant targets skipped within the group
	// Queried counts the provider queries the group's single sweep actually
	// issued (successful or erroring; providers skipped by the health
	// timeout never sent a request and don't count). Zero for skipped and
	// backed-off groups. Distinct from Searched, which counts VARIANT
	// targets processed against results and stays positive even when the
	// sweep issued no query (every eligible provider timed out).
	Queried int
}

// Found reports whether at least one subtitle was downloaded for the language.
func (o *LangOutcome) Found() bool { return len(o.Paths) > 0 }

// SearchResult holds the outcome of a SearchTargets call: one typed entry
// per language group plus the coverage-inventory flag.
type SearchResult struct {
	Langs           []LangOutcome // one entry per language group, in target order
	CoverageChanged bool          // true if RecordSubtitleFiles detected changes on disk
}

// Paths returns every subtitle file downloaded across all language groups.
func (r *SearchResult) Paths() []string {
	var paths []string
	for i := range r.Langs {
		paths = append(paths, r.Langs[i].Paths...)
	}
	return paths
}

// TargetsSearched returns the number of variant targets that ran against
// provider results across all language groups.
func (r *SearchResult) TargetsSearched() int {
	n := 0
	for i := range r.Langs {
		n += r.Langs[i].Searched
	}
	return n
}

// TargetsBackedOff returns the number of language groups that needed a
// search but had every provider in adaptive backoff.
func (r *SearchResult) TargetsBackedOff() int {
	n := 0
	for i := range r.Langs {
		if r.Langs[i].Kind == LangBackedOff {
			n++
		}
	}
	return n
}

// ProviderQueried reports whether at least one provider query was actually
// issued during the call, across all language groups. This is the inter-item
// pacing signal: the scan delay exists to space real provider traffic, so an
// item that touched no provider (fully covered, locked, in adaptive backoff,
// or with every eligible provider health-timed-out) must not pay it.
// Deliberately not keyed on TargetsSearched(): that counts variant targets
// and stays positive even when the sweep sent zero requests.
func (r *SearchResult) ProviderQueried() bool {
	for i := range r.Langs {
		if r.Langs[i].Queried > 0 {
			return true
		}
	}
	return false
}
