// Package api defines the internal contracts between subflux components.
// All cross-component calls go through these interfaces, enabling
// testability (mock any component) and swappability (e.g. SQLite → PostgreSQL).
//
// This package imports no subflux implementation packages — only stdlib
// and shared external libraries (cplieger/arrapi, cplieger/auth,
// cplieger/webhttp). Implementation packages import api, never the reverse.
//
// This file contains consumer contracts: interfaces that consuming code
// depends on (Store and ConfigProvider). Implementation/provider contracts
// live in interfaces_provider.go.
package api

import (
	"context"
	"time"

	"github.com/cplieger/auth/v4"
)

// --- Persistence ---

// Store is the whole persistence surface: 36 methods over search state,
// subtitle state, coverage, and the poll watermark. Like ConfigProvider it is
// WIDE on purpose and for composition roots only — main's wiring, the server's
// db field, and the handler Deps that carry it inward.
//
// Every consumer that reads or writes through it declares its own narrow
// interface at its own site and is handed this by structural typing:
// manualops.SearchStore takes 2 methods, resolve.FileStore 1,
// synchandlers.SyncStore, filehandlers.FileStore, polling.PollerStore,
// queryhandlers.QueryStore and coveragehandlers.CoverageStore each a handful.
// That is the pattern to follow. Do not carve a subset out of this type.
//
// Nine sub-interfaces used to be embedded here to offer those subsets
// pre-made. Eight had no consumer at all and are gone; the ninth is
// CoverageStore below, which three packages take directly, so it stays
// embedded rather than re-listed.
//
// All methods accept context.Context for cancellation and timeout propagation.
type Store interface {
	CoverageStore

	// --- Adaptive backoff ---

	RecordNoResult(ctx context.Context, mediaType MediaType, mediaID, language string, providerName ProviderID, bp BackoffParams) error
	BackedOffProviders(ctx context.Context, mediaType MediaType, mediaID, language string, maxAttempts int) ([]ProviderID, error)

	// --- Download records ---
	//
	// subtitle_state rows are keyed by the (media_type, media_id, language,
	// variant) quad, so CurrentScore is answered per variant: an fr/forced
	// download never shadows the fr/standard score. DownloadedRefs
	// deliberately stays language-scoped (all variants): it feeds the manual-
	// search popup's "on disk" markers, and the popup lists every variant of
	// the language.

	SaveDownload(ctx context.Context, rec *DownloadRecord) error
	DownloadedRefs(ctx context.Context, mediaType MediaType, mediaID, language string) ([]DownloadedRef, error)
	CurrentScore(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant) (score int, mediaImported time.Time, found bool, err error)

	// --- Manual override locks ---
	//
	// Locks live on the ManualLockKey quad, so a manual forced download locks
	// only the forced target and leaves standard/hi automation untouched.

	// IsManuallyLocked reports whether the quad has a manual row. An empty
	// key Variant asks whether ANY variant of the language is locked.
	IsManuallyLocked(ctx context.Context, key ManualLockKey) (bool, error)
	// ClearManualLock clears the quad's lock. An empty key Variant clears the
	// locks of ALL variants of the language.
	ClearManualLock(ctx context.Context, key ManualLockKey) error
	// ManualDownloadCount counts the quad's manual rows (exact variant).
	ManualDownloadCount(ctx context.Context, key ManualLockKey) (int, error)
	// ManualSubtitlePaths returns the manual rows' file paths. An empty key
	// Variant returns the paths of ALL variants of the language.
	ManualSubtitlePaths(ctx context.Context, key ManualLockKey) ([]string, error)
	// NextManualNumber returns the next manual ordinal for the quad (exact
	// variant): movie.fr.1.srt and movie.fr.forced.1.srt count independently.
	NextManualNumber(ctx context.Context, key ManualLockKey) int

	// --- Read-only state inspection ---

	GetState(ctx context.Context, q *StateQuery) ([]StateEntry, error)
	GetBackoffItems(ctx context.Context) ([]BackoffEntry, error)
	GetBackoffByPrefix(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]BackoffEntry, error)
	GetManualLocks(ctx context.Context) ([]ManualLockEntry, error)
	Stats(ctx context.Context) (downloads, attempts int, err error)

	// --- Download history ---

	HistoryMediaIDs(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]string, error)

	// --- Subtitle timing adjustments ---

	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
	GetSyncOffset(ctx context.Context, path string) (int64, error)

	// --- Maintenance and cleanup ---

	DeleteStateByPaths(ctx context.Context, paths []string) (CleanupResult, error)
	CleanupDrift(ctx context.Context, drift ConfigDrift) error
	ReconcileState(ctx context.Context) (ReconcileResult, error)

	// --- Arr poll watermark ---

	GetPollTimestamp(ctx context.Context, key PollKey) (time.Time, error)
	SetPollTimestamp(ctx context.Context, key PollKey, t time.Time) error

	Close(ctx context.Context) error
}

// CoverageStore is the subtitle-file inventory and scan-state surface. Unlike
// the eight facets deleted alongside it, this one is taken directly by three
// packages — internal/server, server/coverage and server/queryhandlers all
// declare a parameter or field of this type — so it is a real interface with
// real consumers rather than a subset offered on speculation. Store embeds it.
//
// It is 12 methods because the coverage question is answered from two row
// families that must agree: what subtitle files exist for a media item, and
// when that item was last scanned. A consumer reading only one family should
// declare the two or three methods it calls at its own site instead.
type CoverageStore interface {
	RecordSubtitleFiles(ctx context.Context, mediaType MediaType, mediaID string, files []SubtitleFile) (bool, error)
	UpsertSubtitleFile(ctx context.Context, mediaType MediaType, mediaID string, f *SubtitleFile) error
	GetSubtitleFiles(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]SubtitleEntry, error)
	DeleteSubtitleFile(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant, source SubtitleSource, path string) error
	RecordScanState(ctx context.Context, rec *ScanRecord) error
	GetScanStates(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]ScanStateRow, error)
	RecentlyScanned(ctx context.Context, cutoff time.Time) (map[string]bool, error)
	TotalSubtitleFiles(ctx context.Context) (int, error)
	LastScanTime(ctx context.Context) (string, error)
	// Scan-cycle mark (duration-aware resume): set when a full scan begins,
	// cleared on normal completion. A dangling mark means the previous cycle
	// was interrupted; ScanCycleStart returns the zero time when absent.
	ScanCycleStart(ctx context.Context) (time.Time, error)
	SetScanCycleStart(ctx context.Context, t time.Time) error
	ClearScanCycleStart(ctx context.Context) error
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
