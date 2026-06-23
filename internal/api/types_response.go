package api

// --- Scan outcome ---

// ScanOutcome is a typed string for scan result classification.
// Canonical source; scanning/ and server/ should import from here.
type ScanOutcome string

// Scan outcome constants.
const (
	ScanFound    ScanOutcome = "found"
	ScanSkipped  ScanOutcome = "skipped"
	ScanNoResult ScanOutcome = "none"
)

// --- API response types ---

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
