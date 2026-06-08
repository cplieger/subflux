// Package manualops implements the business logic for manual subtitle
// search and download operations. The HTTP handler glue remains in the
// parent server package; this package owns validation, query parsing,
// result building, and the background download pipeline.
package manualops

import (
	"context"
	"strings"

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
type SearchStore interface {
	IsManuallyLocked(ctx context.Context, mediaType api.MediaType, mediaID, language string) (bool, error)
	DownloadedRefs(ctx context.Context, mediaType api.MediaType, mediaID, language string) ([]api.DownloadedRef, error)
	ClearManualLock(ctx context.Context, mediaType api.MediaType, mediaID, language string) error
}

// ActivityTracker manages activity lifecycle.
type ActivityTracker interface {
	Start(action, detail string, source activity.ActivitySource) string
	End(id string)
	Fail(id string)
}

// Refresher is the narrow interface for triggering arr metadata refreshes
// after manual downloads. Only RefreshSeries and RefreshMovie are needed.
type Refresher interface {
	RefreshSeries(ctx context.Context, seriesID int) error
	RefreshMovie(ctx context.Context, movieID int) error
}

// Compile-time assertion: api.ArrClient satisfies Refresher.
var _ Refresher = api.ArrClient(nil)

// ManualArrClient is the narrow interface for arr operations needed by
// manual download handlers: media lookup + refresh after download.
type ManualArrClient interface {
	GetSeriesByID(ctx context.Context, seriesID int) (*api.Series, error)
	GetMovieByID(ctx context.Context, movieID int) (*api.Movie, error)
	RefreshSeries(ctx context.Context, seriesID int) error
	RefreshMovie(ctx context.Context, movieID int) error
}

// Compile-time assertion: api.ArrClient satisfies ManualArrClient.
var _ ManualArrClient = api.ArrClient(nil)

// EventPublisher publishes events to SSE clients.
type EventPublisher interface {
	PublishNotify(level events.NotifyLevel, text string)
	PublishCoverageUpdate(mediaType api.MediaType, mediaID, language, source, path string)
}

// LiveState holds the runtime state needed for a manual search pass.
type LiveState struct {
	Cfg       api.ConfigProvider
	Engine    api.SearchEngine
	Sonarr    ManualArrClient
	Radarr    ManualArrClient
	Providers []api.Provider
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

// RunClearLock clears the manual lock for a media+language combination.
func RunClearLock(ctx context.Context, deps *SearchDeps, mediaType, mediaID, language string) error {
	return deps.DB.ClearManualLock(ctx, api.MediaType(mediaType), mediaID, language)
}
