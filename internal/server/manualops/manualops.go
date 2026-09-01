// Package manualops implements the business logic for manual subtitle
// search and download operations. The HTTP handler glue remains in the
// parent server package; this package owns validation, query parsing,
// result building, and the background download pipeline.
package manualops

import (
	"context"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/subflux"
)

const MaxResults = 50

type SearchResult struct {
	Matches     map[string]int     `json:"matches,omitempty"`
	Provider    subflux.ProviderID `json:"provider"`
	Language    string             `json:"language"`
	ReleaseName string             `json:"release_name"`
	MatchedBy   string             `json:"matched_by"`
	SubtitleID  string             `json:"subtitle_id"`
	// Tier is the score's quality-tier label, computed server-side by the
	// scorer (same table as /api/score): the remote CLI renders it and has
	// no scorer of its own.
	Tier       subflux.ScoreTier `json:"tier"`
	Score      int               `json:"score"`
	HearingImp bool              `json:"hearing_impaired"`
	Forced     bool              `json:"forced"`
	OnDisk     bool              `json:"on_disk"`
}

// WarnRecorder records an actionable warning for the UI's alert list.
// Satisfied structurally by activity.AlertLog.
type WarnRecorder interface {
	RecordWarn(source, msg string)
}

type SearchDeps struct {
	DB       Store
	Activity ActivityTracker
	Alerts   WarnRecorder
	Events   EventPublisher
}

// Store is the two rows a manual search touches: what is already on disk
// for the language and releasing a lock. The lock key's empty variant means
// "all variants of the language" (see subflux.ManualLockKey).
type Store interface {
	DownloadedRefs(ctx context.Context, mediaType subflux.MediaType, mediaID, language string) ([]subflux.DownloadedRef, error)
	ClearManualLock(ctx context.Context, key subflux.ManualLockKey) error
}

// ActivityTracker manages activity lifecycle. Progress also mutates the
// entry detail: download completion writes the saved subtitle path there so
// the remote CLI's poll loop can report it.
type ActivityTracker interface {
	Start(action, detail string, source activity.Source) string
	End(id string)
	Fail(id string)
	Progress(id string, current, total int, detail string)
}

// ManualSonarrClient is the Sonarr surface manual downloads use.
type ManualSonarrClient interface {
	SeriesByID(ctx context.Context, seriesID int) (arrapi.Series, error)
	RescanSeries(ctx context.Context, seriesID int) error
}

// ManualRadarrClient is the Radarr surface manual downloads use.
type ManualRadarrClient interface {
	MovieByID(ctx context.Context, movieID int) (arrapi.Movie, error)
	RescanMovie(ctx context.Context, movieID int) error
}

// EventPublisher publishes events to SSE clients. Satisfied structurally
// by *events.EventBus.
type EventPublisher interface {
	PublishNotify(level events.NotifyLevel, text string)
	PublishCoverageUpdate(ev *events.CoverageEvent)
}

// manualEngine is the narrow slice of the search engine the manual path
// uses; SearchTargets and the query path's timeout controls belong to the
// automated scan, not here.
type manualEngine interface {
	HashFile(ctx context.Context, path string) (hash string, size int64, err error)
	ScoreSubtitles(req *subflux.SearchRequest, results []subflux.Subtitle) []subflux.ScoredResult
	SyncAndPostProcess(ctx context.Context, data []byte, videoPath, lang string, variant subflux.Variant) (synced []byte, offsetMs int64)
}

// tierLabeller maps a numeric score onto the tier label the manual-search
// popup renders; the engine has already scored these candidates by the time
// they reach this package, so the manual path never scores anything itself.
type tierLabeller interface {
	ScoreToTier(score int) subflux.ScoreTier
}

// pathValidator is the containment check a manual download runs before it
// writes: the resolved subtitle path must sit under a configured media root.
type pathValidator interface {
	ValidatePath(ctx context.Context, path string) error
}

// LiveState holds the runtime state needed for a manual search pass.
// Sonarr/Radarr are the narrow by-ID surfaces manual downloads use;
// SonarrLib/RadarrLib are the library-listing surfaces the resolve
// endpoint uses (all nil when the corresponding arr is not configured).
type LiveState struct {
	Cfg       pathValidator
	Engine    manualEngine
	Scorer    tierLabeller
	Sonarr    ManualSonarrClient
	Radarr    ManualRadarrClient
	SonarrLib ResolveSonarrClient
	RadarrLib ResolveRadarrClient
	Providers []provider.Provider
}

// isValidLockVariant accepts the canonical variants plus empty (empty means
// "all variants" on clear-lock). Anything else is rejected so a typo never
// silently no-ops against a variant that cannot exist.
func isValidLockVariant(v subflux.Variant) bool {
	switch v {
	case "", subflux.VariantStandard, subflux.VariantHI, subflux.VariantForced:
		return true
	default:
		return false
	}
}

// alertSourceManual attributes an alert to the manual-download path.
const alertSourceManual = "manual"

// ErrorNotice is one error's two audiences: the operator reading the alert
// log and the user watching the UI. Named fields rather than positional,
// because Alert and UI are both human-readable strings and a transposition
// would compile and read plausibly at most call sites.
type ErrorNotice struct {
	// Source attributes the alert to a subsystem.
	Source string
	// Alert is the operator-facing text; it lands in the alert log.
	Alert string
	// UI is the user-facing text; it lands on the notify bus.
	UI string
}

func NotifyError(deps *SearchDeps, n ErrorNotice) {
	deps.Alerts.RecordWarn(n.Source, n.Alert)
	deps.Events.PublishNotify(events.NotifyError, n.UI)
}
