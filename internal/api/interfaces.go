// Package api holds subflux's wire and domain types.
//
// It imports no subflux implementation packages — only stdlib and shared
// external libraries (cplieger/arrapi, cplieger/auth, cplieger/webhttp).
// Implementation packages import api, never the reverse. That acyclicity is
// what the package is for; it is NOT a place to declare contracts, and the
// testability/swappability rationale this doc used to give was measured and
// found false — there is one store implementation, and every consumer already
// declares the surface it calls at its own site.
//
// One interface is left in this file — ConfigProvider — and its move is pending
// for a reason of its own: it needs internal/server to import internal/config,
// and it is entangled with six downcast sites.
package api

import (
	"context"
	"time"

	"github.com/cplieger/auth/v4"
)

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
