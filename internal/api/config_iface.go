package api

import (
	"context"
	"time"

	"github.com/cplieger/auth/v4"
)

// ConfigProvider sub-interfaces. Consumers should accept the narrowest
// sub-interface that satisfies their needs, following the same pattern
// as Store (composed from BackoffStore, DownloadStore, etc.).

// LogLevel is a typed string for log verbosity levels.
type LogLevel string

// LogFormat is a typed string for log output formats.
type LogFormat string

// ScoringConfig provides scoring weight configuration.
type ScoringConfig interface {
	Scores() Scores
}

// LanguageResolver resolves subtitle targets from audio language context.
type LanguageResolver interface {
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []SubtitleTarget
	LanguageCodes() []string
	ProvidersForTarget(t *SubtitleTarget, allProviders []ProviderID) []ProviderID
	MinScoreForTarget(t *SubtitleTarget, mediaType MediaType) int
}

// ArrConfigProvider provides Sonarr/Radarr connection configuration.
type ArrConfigProvider interface {
	Sonarr() ArrConfig
	Radarr() ArrConfig
}

// ProviderConfigProvider provides provider settings.
type ProviderConfigProvider interface {
	ProviderConfigs() map[ProviderID]ProviderCfg
	ProviderPriority(name ProviderID) int
}

// ServerConfig provides server runtime configuration.
type ServerConfig interface {
	ServerPort() int
	PollInterval() time.Duration
	LoggingLevel() LogLevel
	LoggingFormat() LogFormat
}

// PathValidator provides media path validation.
type PathValidator interface {
	ValidatePath(ctx context.Context, path string) error
	RemoveUnderRoot(ctx context.Context, path string) error
}

// SearchConfigProvider provides search behavior configuration.
type SearchConfigProvider interface {
	Search() SearchConfig
	Adaptive() AdaptiveConfig
	PostProcess() PostProcessConfig
	Sync() SyncConfig
	// EmbeddedPolicy returns the typed embedded subtitle codec policy
	// from the top-level embedded_subtitles config section.
	EmbeddedPolicy() EmbeddedPolicy
}

// AuthConfigProvider provides authentication configuration.
type AuthConfigProvider interface {
	BasicAuthEnabled() bool
	OIDCEnabled() bool
	OIDC() auth.OIDCConfig
	SessionIdleTimeout() time.Duration
	SessionAbsoluteTimeout() time.Duration
	CheckBreachedPasswords() bool
	WebAuthnRPID() string
}

// UIConfigProvider provides UI-specific configuration.
type UIConfigProvider interface {
	LanguageRulesForUI() LanguageRulesJSON
}
