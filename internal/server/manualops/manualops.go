// Package manualops implements the business logic for manual subtitle
// search and download operations. The HTTP handler glue remains in the
// parent server package; this package owns validation, query parsing,
// result building, and the background download pipeline.
package manualops

import (
	"context"
	"strings"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
)

// MaxResults caps the number of results returned by manual search.
const MaxResults = 50

// MaxLangCodeLen caps language code length. BCP 47 codes are typically ≤11
// chars (e.g. "pt-BR"); 20 provides headroom for unusual subtags.
const MaxLangCodeLen = 20

// SearchResult is a single result returned by the manual search API.
type SearchResult struct {
	Matches     map[string]int `json:"matches,omitempty"`
	Provider    api.ProviderID `json:"provider"`
	Language    string         `json:"language"`
	ReleaseName string         `json:"release_name"`
	MatchedBy   string         `json:"matched_by"`
	SubtitleID  string         `json:"subtitle_id"`
	Score       int            `json:"score"`
	HearingImp  bool           `json:"hearing_impaired"`
	Forced      bool           `json:"forced"`
	OnDisk      bool           `json:"on_disk"`
}

// SearchDeps holds the narrow dependencies for manual search execution.
type SearchDeps struct {
	DB       SearchStore
	Activity ActivityTracker
	Alerts   activity.WarnRecorder
	Events   EventPublisher
}

// SearchStore is the narrow store interface for manual search operations.
// ClearManualLock takes a variant; an empty variant means "all variants of
// the language" (see api.ManualLockStore).
type SearchStore interface {
	DownloadedRefs(ctx context.Context, mediaType api.MediaType, mediaID, language string) ([]api.DownloadedRef, error)
	ClearManualLock(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) error
}

// ActivityTracker manages activity lifecycle.
type ActivityTracker interface {
	Start(action, detail string, source activity.ActivitySource) string
	End(id string)
	Fail(id string)
}

// ManualSonarrClient is the Sonarr surface manual downloads use: series lookup
// (for media-ID and title resolution) and a post-download rescan.
type ManualSonarrClient interface {
	GetSeriesByID(ctx context.Context, seriesID int) (arrapi.Series, error)
	RescanSeries(ctx context.Context, seriesID int) error
}

// ManualRadarrClient is the Radarr surface manual downloads use.
type ManualRadarrClient interface {
	GetMovieByID(ctx context.Context, movieID int) (arrapi.Movie, error)
	RescanMovie(ctx context.Context, movieID int) error
}

// Compile-time assertions: the arrapi-backed role clients satisfy the manual
// surfaces.
var (
	_ ManualSonarrClient = api.SonarrClient(nil)
	_ ManualRadarrClient = api.RadarrClient(nil)
)

// EventPublisher publishes events to SSE clients.
type EventPublisher interface {
	PublishNotify(level events.NotifyLevel, text string)
	PublishCoverageUpdate(mediaType api.MediaType, mediaID, language, source, path string)
}

// LiveState holds the runtime state needed for a manual search pass.
type LiveState struct {
	Cfg       api.ConfigProvider
	Engine    api.SearchEngine
	Sonarr    ManualSonarrClient
	Radarr    ManualRadarrClient
	Providers []api.Provider
}

// isValidLockVariant accepts the canonical variants plus empty (empty means
// "all variants" on clear-lock). Anything else is rejected so a typo never
// silently no-ops against a variant that cannot exist.
func isValidLockVariant(v api.Variant) bool {
	switch v {
	case "", api.VariantStandard, api.VariantHI, api.VariantForced:
		return true
	default:
		return false
	}
}

// IsValidLangCode rejects language codes that are too long, contain path
// separators, traversal sequences, or control characters (including null
// bytes that cause path truncation).
func IsValidLangCode(lang string) bool {
	if lang == "" || len(lang) > MaxLangCodeLen {
		return false
	}
	if strings.ContainsAny(lang, "/\\") || strings.Contains(lang, "..") {
		return false
	}
	return !strings.ContainsFunc(lang, func(r rune) bool { return r < 0x20 })
}

// NotifyError publishes an error notification and records an alert.
func NotifyError(deps *SearchDeps, source, alertMsg, uiMsg string) {
	deps.Alerts.RecordWarn(source, alertMsg)
	deps.Events.PublishNotify(events.NotifyError, uiMsg)
}

// RunClearLock clears the manual lock for a media+language combination. An
// empty variant clears the locks of every variant of the language; a specific
// variant clears only that quad's lock.
func RunClearLock(ctx context.Context, deps *SearchDeps, mediaType, mediaID, language string, variant api.Variant) error {
	return deps.DB.ClearManualLock(ctx, api.MediaType(mediaType), mediaID, language, variant)
}
