// Package api defines the internal contracts between subflux components.
// All cross-component calls go through these interfaces, enabling
// testability (mock any component) and swappability (e.g. SQLite → PostgreSQL).
//
// This package imports no subflux implementation packages — only stdlib
// and shared external libraries (cplieger/arrapi, cplieger/auth,
// cplieger/webhttp). Implementation packages import api, never the reverse.
//
// This file contains consumer contracts: interfaces that consuming code
// depends on (Store, AuthStore, ConfigProvider, SearchEngine).
// Implementation/provider contracts live in interfaces_provider.go.
package api

import (
	"context"
	"time"

	"github.com/cplieger/auth/v4"
)

// --- Persistence ---

// Store persists search state and subtitle state.
// All methods accept context.Context for cancellation and timeout propagation.
//
// Methods are grouped by domain concern into composable sub-interfaces
// defined in store_iface.go. Consumers should accept the narrowest
// sub-interface that satisfies their needs.
type Store interface {
	BackoffStore
	DownloadStore
	ManualLockStore
	QueryStore
	HistoryStore
	CoverageStore
	SyncOffsetStore
	MaintStore
	PollStore
	Close(ctx context.Context) error
}

// --- Configuration ---

// ConfigProvider is the whole read surface of the loaded configuration: all 28
// values the app reads out of config.yaml. It is WIDE on purpose, and the width
// is what restricts who may take it — a composition root, which by definition
// holds everything so that it can hand each consumer something narrower.
//
// A consumer that reads three values declares a three-method interface at its
// own site and accepts that; *config.Config satisfies both structurally, so
// nothing has to be adapted. Nine sub-interfaces used to be declared here for
// that purpose and not one consumer ever named them, so the width they were
// meant to hide was never actually hidden from anybody.
//
// Do not carve a subset out of this type for a caller. The subset belongs at
// the caller.
type ConfigProvider interface {
	// Scoring weights.
	Scores() Scores

	// Language resolution: which subtitle targets a media item earns, and
	// which providers and score floor apply to each.
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []SubtitleTarget
	LanguageCodes() []string
	ProvidersForTarget(t *SubtitleTarget, allProviders []ProviderID) []ProviderID
	MinScoreForTarget(t *SubtitleTarget, mediaType MediaType) int

	// Sonarr/Radarr connection details.
	Sonarr() ArrConfig
	Radarr() ArrConfig

	// Provider settings and trust order.
	Providers() map[ProviderID]ProviderCfg
	ProviderPriority(name ProviderID) int

	// Server runtime.
	ServerPort() int
	PollInterval() time.Duration
	LoggingLevel() LogLevel
	LoggingFormat() LogFormat

	// Media-path containment. Both answer against the configured media roots.
	ValidatePath(ctx context.Context, path string) error
	RemoveUnderRoot(ctx context.Context, path string) error

	// Search behaviour.
	Search() SearchConfig
	Adaptive() AdaptiveConfig
	PostProcess() PostProcessConfig
	Sync() SyncConfig
	// EmbeddedPolicy returns the typed embedded subtitle codec policy
	// from the top-level embedded_subtitles config section.
	EmbeddedPolicy() EmbeddedPolicy

	// Authentication.
	BasicAuthEnabled() bool
	OIDCEnabled() bool
	OIDC() auth.OIDCConfig
	SessionIdleTimeout() time.Duration
	SessionAbsoluteTimeout() time.Duration
	CheckBreachedPasswords() bool
	WebAuthnRPID() string

	// UI.
	LanguageRulesForUI() LanguageRulesJSON
}

// --- Search & Scoring ---

// SubtitleSearcher orchestrates subtitle search across providers.
type SubtitleSearcher interface {
	SearchTargets(ctx context.Context, req *SearchRequest, videoPath string, targets []SubtitleTarget) (SearchResult, error)
	// InventoryCoverage records the on-disk/embedded subtitle inventory for a
	// media item WITHOUT any provider work, stamping its scan state as
	// inventoried-not-searched. Used by scan skip paths (season early stop,
	// show-level skip) so coverage stays truthful for items the scanner
	// deliberately does not search.
	InventoryCoverage(ctx context.Context, req *SearchRequest, videoPath string) (coverageChanged bool)
}

// ScoreSimulator provides subtitle scoring capabilities.
type ScoreSimulator interface {
	SimulateScore(mediaType MediaType, videoRelease, subRelease string, matchedBy MatchMethod) ScoreResult
	ScoreSubtitles(req *SearchRequest, results []Subtitle) []ScoredResult
}

// ProviderTimeoutManager manages provider timeout state.
type ProviderTimeoutManager interface {
	ProviderTimeouts() (status map[ProviderID]ProviderStatus, enabled bool)
	ResetTimeouts()
}

// SubtitlePostProcessor handles post-download subtitle processing.
type SubtitlePostProcessor interface {
	SyncAndPostProcess(ctx context.Context, data []byte, videoPath, lang string, variant Variant) (synced []byte, offsetMs int64)
	HashFile(ctx context.Context, path string) (hash string, size int64, err error)
}

// SearchEngine composes all search sub-interfaces for composition roots.
type SearchEngine interface {
	SubtitleSearcher
	ScoreSimulator
	ProviderTimeoutManager
	SubtitlePostProcessor
}
