// Package api defines the internal contracts between subflux components.
// All cross-component calls go through these interfaces, enabling
// testability (mock any component) and swappability (e.g. SQLite → PostgreSQL).
//
// This package imports only stdlib. Implementation packages import api,
// never the reverse.
//
// This file contains consumer contracts: interfaces that consuming code
// depends on (Store, AuthStore, ConfigProvider, SearchEngine).
// Implementation/provider contracts live in interfaces_provider.go.
package api

import (
	"context"
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

// (AuthStore composite moved to internal/auth/store_iface.go for
// consumer-placement; sub-interfaces remain here because they're
// referenced by server/ narrow interfaces individually.)

// --- Configuration ---

// ConfigProvider gives read access to configuration.
// It composes the focused sub-interfaces defined in config_iface.go.
// Consumers should accept the narrowest sub-interface that satisfies
// their needs; ConfigProvider is for composition roots that need everything.
type ConfigProvider interface {
	ScoringConfig
	LanguageResolver
	ArrConfigProvider
	ProviderConfigProvider
	ServerConfig
	PathValidator
	SearchConfigProvider
	AuthConfigProvider
	UIConfigProvider
}

// --- Search & Scoring ---

// SubtitleSearcher orchestrates subtitle search across providers.
type SubtitleSearcher interface {
	SearchTargets(ctx context.Context, req *SearchRequest, videoPath string, targets []SubtitleTarget) (SearchResult, error)
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
