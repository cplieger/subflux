package subflux

// --- Scan outcome ---

// ScanOutcome is a typed string for scan result classification.
// Canonical source; scanning/ and server/ should import from here.
type ScanOutcome string

// Scan outcome constants.
const (
	ScanFound    ScanOutcome = "found"
	ScanSkipped  ScanOutcome = "skipped"
	ScanNoResult ScanOutcome = "none"
	// ScanBackedOff means every language target that needed a search had all
	// of its providers in adaptive backoff, so no provider was queried at
	// all. Distinct from ScanNoResult ("we looked, nothing found") and
	// ScanSkipped ("nothing needed a search"): "we already know there is
	// probably nothing, so we didn't look". Never recorded in the season
	// early-termination tracker and counted in its own stats bucket.
	ScanBackedOff ScanOutcome = "backed_off"
)

// --- Sync job outcome ---

// JobOutcome names how a done sync job ended, mirroring the typed core's
// vocabulary. It lives here rather than beside the dispatcher because it
// crosses a one-way seam: syncjobs.Job serves it on GET /api/sync/jobs and
// events.SyncDoneEvent carries it on sync:done, and syncjobs imports events
// (the publish seam), so neither of those two can own it.
type JobOutcome string

// Sync job outcomes (meaningful only in the done state).
const (
	JobResult    JobOutcome = "result"
	JobTimeout   JobOutcome = "timeout"
	JobCancelled JobOutcome = "cancelled"
	JobCrash     JobOutcome = "crash"
)

// --- API response types ---

// KeyStatus is the canonical JSON key for operation result status responses
// (StatusResponse is the typed carrier). Used by server, scanning, and
// confighandlers for the handful of status bodies still built as a map.
const KeyStatus = "status"

// StatusResponse is the canonical {"status": "..."} operation-result body
// used by action endpoints (scan triggers, resets, logout).
type StatusResponse struct {
	Status string `json:"status"`
}

// Stats is the JSON response for GET /api/stats.
type Stats struct {
	LastScan            string `json:"last_scan"`
	Downloads           int    `json:"downloads"`
	Attempts            int64  `json:"attempts"`
	ScanIntervalSeconds int    `json:"scan_interval_seconds"`
	TotalSubs           int    `json:"total_subs"`
	TotalSeries         int    `json:"total_series"`
	TotalMovies         int    `json:"total_movies"`
	MissingSubs         int    `json:"missing_subs"`
	Partial             bool   `json:"partial"`
}

// ScorePreview is the JSON response for POST /api/score/preview.
type ScorePreview struct {
	Tier        ScoreTier `json:"tier"`
	Score       int       `json:"score"`
	ScoreNoHash int       `json:"score_no_hash"`
}

// SearchTarget describes a single subtitle search target in the API response.
type SearchTarget struct {
	MinScore  *int     `json:"min_score,omitempty"`
	Code      string   `json:"code"`
	Variant   string   `json:"variant"`
	Providers []string `json:"providers,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
}

// SearchTargets is the JSON response for GET /api/search/targets.
type SearchTargets struct {
	OrigLang   string         `json:"orig_lang"`
	AudioLangs []string       `json:"audio_langs"`
	Targets    []SearchTarget `json:"targets"`
}

// ProvidersResponse is the JSON response for GET /api/providers/timeout.
type ProvidersResponse struct {
	Providers map[ProviderID]ProviderStatus `json:"providers"`
	Enabled   bool                          `json:"enabled"`
}
