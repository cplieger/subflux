// Package manualops implements the business logic for manual subtitle
// search and download operations. The HTTP handler glue remains in the
// parent server package; this package owns validation, query parsing,
// result building, and the background download pipeline.
package manualops

import (
	"context"
	"strings"

	"github.com/cplieger/arrapi/v2"
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
	// Tier is the score's quality-tier label, computed server-side by the
	// scorer (same table as /api/score): the remote CLI renders it and has
	// no scorer of its own.
	Tier       api.ScoreTier `json:"tier"`
	Score      int           `json:"score"`
	HearingImp bool          `json:"hearing_impaired"`
	Forced     bool          `json:"forced"`
	OnDisk     bool          `json:"on_disk"`
}

// WarnRecorder records an actionable warning for the UI's alert list.
// activity.AlertLog satisfies it structurally.
type WarnRecorder interface {
	RecordWarn(source, msg string)
}

// SearchDeps holds the narrow dependencies for manual search execution.
type SearchDeps struct {
	DB       SearchStore
	Activity ActivityTracker
	Alerts   WarnRecorder
	Events   EventPublisher
}

// SearchStore is the narrow store interface for manual search operations.
// The lock key's empty variant means "all variants of the language" (see
// api.ManualLockKey).
type SearchStore interface {
	DownloadedRefs(ctx context.Context, mediaType api.MediaType, mediaID, language string) ([]api.DownloadedRef, error)
	ClearManualLock(ctx context.Context, key api.ManualLockKey) error
}

// ActivityTracker manages activity lifecycle. Progress doubles as the
// detail mutator: download completion writes the saved subtitle path into
// the entry detail so activity consumers (the remote CLI's poll loop)
// can report it.
type ActivityTracker interface {
	Start(action, detail string, source activity.ActivitySource) string
	End(id string)
	Fail(id string)
	Progress(id string, current, total int, detail string)
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
// *events.EventBus satisfies it structurally.
type EventPublisher interface {
	PublishNotify(level events.NotifyLevel, text string)
	PublishCoverageUpdate(ev *events.CoverageEvent)
}

// LiveState holds the runtime state needed for a manual search pass.
// Sonarr/Radarr are the narrow by-ID surfaces manual downloads use;
// SonarrLib/RadarrLib are the library-listing surfaces the resolve
// endpoint uses (all nil when the corresponding arr is not configured).
type LiveState struct {
	Cfg       api.ConfigProvider
	Engine    api.SearchEngine
	Scorer    api.Scorer
	Sonarr    ManualSonarrClient
	Radarr    ManualRadarrClient
	SonarrLib ResolveSonarrClient
	RadarrLib ResolveRadarrClient
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

// alertSourceManual attributes an alert to the manual-download path. One
// constant because ErrorNotice made the four occurrences visible as the one
// value they always were.
const alertSourceManual = "manual"

// ErrorNotice is one error's two audiences: the operator reading the alert log
// and the user watching the UI.
//
// Named rather than positional because Alert and UI are both human-readable
// text of the same type, so a transposition compiles and reads plausibly at the
// call site while putting the internal diagnosis in front of the user and the
// user-facing summary in the operator's alert log. Two of the three call sites
// pass DIFFERENT strings for the two, which is exactly where such a swap leaves
// no trace — the one that passes the same string twice would not even change
// behaviour, so the reviewer gets no signal from the sites either.
type ErrorNotice struct {
	// Source attributes the alert to a subsystem.
	Source string
	// Alert is the operator-facing text; it lands in the alert log.
	Alert string
	// UI is the user-facing text; it lands on the notify bus.
	UI string
}

// NotifyError publishes an error notification and records an alert.
func NotifyError(deps *SearchDeps, n ErrorNotice) {
	deps.Alerts.RecordWarn(n.Source, n.Alert)
	deps.Events.PublishNotify(events.NotifyError, n.UI)
}
