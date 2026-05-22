package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// --- Sonarr types ---

// Series represents a Sonarr series.
type Series struct {
	Seasons          []SeasonInfo      `json:"seasons,omitempty"`
	Statistics       *SeriesStatistics `json:"statistics,omitempty"`
	OriginalLanguage *LanguageInfo     `json:"originalLanguage,omitempty"`
	AlternateTitles  []AlternateTitle  `json:"alternateTitles,omitempty"`
	Title            string            `json:"title"`
	ImdbID           string            `json:"imdbId"`
	FirstAired       string            `json:"firstAired,omitempty"`
	Tags             []int             `json:"tags"`
	ID               int               `json:"id"`
	Year             int               `json:"year"`
	TvdbID           int               `json:"tvdbId"`
}

// SeasonInfo holds per-season metadata from Sonarr's series endpoint.
type SeasonInfo struct {
	Statistics   *SeasonStatistics `json:"statistics,omitempty"`
	SeasonNumber int               `json:"seasonNumber"`
}

// SeasonStatistics holds per-season episode counts from Sonarr.
type SeasonStatistics struct {
	EpisodeFileCount int `json:"episodeFileCount"`
}

// SeriesStatistics holds episode count data from Sonarr.
type SeriesStatistics struct {
	EpisodeFileCount int `json:"episodeFileCount"`
	SeasonCount      int `json:"seasonCount"`
}

// Episode represents a Sonarr episode with a file.
type Episode struct {
	EpisodeFile           *EpisodeFile `json:"episodeFile,omitempty"`
	Title                 string       `json:"title"`
	ID                    int          `json:"id"`
	SeasonNumber          int          `json:"seasonNumber"`
	EpisodeNumber         int          `json:"episodeNumber"`
	AbsoluteEpisodeNumber int          `json:"absoluteEpisodeNumber"`
	SceneSeasonNumber     int          `json:"sceneSeasonNumber"`
	SceneEpisodeNumber    int          `json:"sceneEpisodeNumber"`
	HasFile               bool         `json:"hasFile"`
}

// EpisodeFile holds file details from Sonarr.
type EpisodeFile struct {
	MediaInfo *MediaInfo `json:"mediaInfo,omitempty"`
	Path      string     `json:"path"`
	SceneName string     `json:"sceneName"`
}

// --- Radarr types ---

// Movie represents a Radarr movie.
type Movie struct {
	MovieFile        *MovieFile       `json:"movieFile,omitempty"`
	OriginalLanguage *LanguageInfo    `json:"originalLanguage,omitempty"`
	AlternateTitles  []AlternateTitle `json:"alternateTitles,omitempty"`
	Title            string           `json:"title"`
	ImdbID           string           `json:"imdbId"`
	InCinemas        string           `json:"inCinemas,omitempty"`
	DigitalRelease   string           `json:"digitalRelease,omitempty"`
	Tags             []int            `json:"tags"`
	ID               int              `json:"id"`
	Year             int              `json:"year"`
	TmdbID           int              `json:"tmdbId"`
	HasFile          bool             `json:"hasFile"`
}

// MovieFile holds file details from Radarr.
type MovieFile struct {
	MediaInfo *MediaInfo `json:"mediaInfo,omitempty"`
	Path      string     `json:"path"`
	SceneName string     `json:"sceneName"`
}

// --- Shared arr types ---

// AlternateTitle represents an alternative title from Sonarr/Radarr.
type AlternateTitle struct {
	Title string `json:"title"`
}

// LanguageInfo represents a language object from Sonarr/Radarr.
type LanguageInfo struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// MediaInfo holds media analysis data. Only AudioLanguages is used;
// the field feeds EpisodeFile.AudioLanguages() and MovieFile.AudioLanguages().
type MediaInfo struct {
	AudioLanguages string `json:"audioLanguages"`
}

// Languages parses the slash/comma-separated audio languages string
// into ISO 639-1 codes. Returns nil if MediaInfo is nil or empty.
func (m *MediaInfo) Languages() []string {
	if m == nil || m.AudioLanguages == "" {
		return nil
	}
	return ParseAudioLangs(m.AudioLanguages)
}

// Tag represents a tag from Sonarr/Radarr.
type Tag struct {
	Label string `json:"label"`
	ID    int    `json:"id"`
}

// --- History types ---

// HistoryEventType identifies the kind of history event.
// Sonarr returns this as an integer (e.g. 3), Radarr as a string
// (e.g. "downloadFolderImported"). UnmarshalJSON handles both.
type HistoryEventType int

const (
	// HistoryImported is a DownloadFolderImported event (new file imported).
	HistoryImported HistoryEventType = 3
	// historyFileDeleted is an EpisodeFileDeleted/MovieFileDeleted event.
	// Unexported: no production caller consumes this event today. Callers
	// only request HistoryImported. Keeping the mapping in
	// radarrEventNames so that an unknown "movieFileDeleted" string does
	// not silently resolve to 0 when a future caller asks for it.
	historyFileDeleted HistoryEventType = 5
)

// radarrEventNames maps Radarr string event types to their integer
// equivalents. Only events relevant to subtitle scanning are mapped.
// Unmapped Radarr events (grabbed, movieRenamed, downloadFailed,
// movieFolderImported, movieFileRenamed) resolve to 0 and are
// filtered by callers.
var radarrEventNames = map[string]HistoryEventType{
	"downloadFolderImported": HistoryImported,
	"movieFileDeleted":       historyFileDeleted,
}

// loggedUnknownEvents dedupes unknown Radarr event-type logs so each
// new value is only reported once per process, not once per history
// entry.
var loggedUnknownEvents = newLogOnce(256)

// UnmarshalJSON handles both integer (Sonarr) and string (Radarr) event types.
func (h *HistoryEventType) UnmarshalJSON(data []byte) error {
	// Try integer first (Sonarr). Also handles JSON null (unmarshals to 0).
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*h = HistoryEventType(n)
		return nil
	}
	// Try string (Radarr).
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("eventType: expected int or string, got %s", string(data))
	}
	if v, ok := radarrEventNames[s]; ok {
		*h = v
		return nil
	}
	// Unknown string event type; store as 0 (will be filtered out by callers).
	// Log once per value so a new Radarr event type (e.g. a rename in a
	// future arr release) surfaces at DEBUG without flooding logs.
	if s != "" {
		if loggedUnknownEvents.first(s) {
			slog.Debug("history: unknown event type (ignored)", "event_type", s)
		}
	}
	*h = 0
	return nil
}

// HistoryEntry is a single event from the arr history API.
type HistoryEntry struct {
	Date      time.Time         `json:"date"`
	Data      map[string]string `json:"data"`
	SeriesID  int               `json:"seriesId"`  // Sonarr
	EpisodeID int               `json:"episodeId"` // Sonarr
	MovieID   int               `json:"movieId"`   // Radarr
}

// ImportedPath returns the file path from an import event's data dictionary.
func (h *HistoryEntry) ImportedPath() string { return h.Data["importedPath"] }

// --- Language resolution ---

// OriginalLangCode returns the ISO 639-1 code for a series' original language.
// Returns empty string if not available.
func (s *Series) OriginalLangCode() string {
	if s.OriginalLanguage == nil {
		return ""
	}
	return LangNameToISO(s.OriginalLanguage.Name)
}

// SeasonEpisodeFileCount returns the number of episode files for a specific
// season, using Sonarr's per-season statistics. Returns 0 if the season is
// not found or statistics are unavailable.
func (s *Series) SeasonEpisodeFileCount(seasonNum int) int {
	for _, si := range s.Seasons {
		if si.SeasonNumber == seasonNum && si.Statistics != nil {
			return si.Statistics.EpisodeFileCount
		}
	}
	return 0
}

// OriginalLangCode returns the ISO 639-1 code for a movie's original language.
// Returns empty string if not available.
func (m *Movie) OriginalLangCode() string {
	if m.OriginalLanguage == nil {
		return ""
	}
	return LangNameToISO(m.OriginalLanguage.Name)
}

// AudioLanguages parses the slash/comma-separated audio languages string
// from MediaInfo into ISO 639-1 codes.
func (f *EpisodeFile) AudioLanguages() []string { return f.MediaInfo.Languages() }

// AudioLanguages parses the slash/comma-separated audio languages string
// from MediaInfo into ISO 639-1 codes.
func (f *MovieFile) AudioLanguages() []string { return f.MediaInfo.Languages() }

// HasExcludeTag reports whether any of the item's tags are in the exclude set.
func HasExcludeTag(tags []int, excludeIDs map[int]struct{}) bool {
	for _, id := range tags {
		if _, ok := excludeIDs[id]; ok {
			return true
		}
	}
	return false
}
