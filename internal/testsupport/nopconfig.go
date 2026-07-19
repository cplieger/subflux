package testsupport

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
)

// NopConfig is a zero-value implementation of api.ConfigProvider (a superset
// of search.SearchCfg) for tests. Embed it in test-specific mocks and
// override only the methods you need, or set the exported fields for the
// common knobs. Note: the compile-time assertion against search.SearchCfg
// lives in search test code to avoid testsupport→search→testsupport import
// cycles (search test code imports testsupport).
type NopConfig struct {
	ProviderCfgs map[api.ProviderID]api.ProviderCfg
	SearchConfig api.SearchConfig
	AdaptiveCfg  api.AdaptiveConfig
	Sync         api.SyncConfig
	PostProcess  api.PostProcessConfig
	// PathErr, when set, is returned by ValidatePath and RemoveUnderRoot,
	// simulating a path outside the configured media roots.
	PathErr      error
	Languages    []string
	Targets      []api.SubtitleTarget
	SonarrCfg    api.ArrConfig
	RadarrCfg    api.ArrConfig
	LangRules    api.LanguageRulesJSON
	MinScore     int
	ProviderPrio int
	Embedded     api.EmbeddedPolicy
}

// Compile-time assertion: NopConfig satisfies the full config surface.
var _ api.ConfigProvider = (*NopConfig)(nil)

// Scores returns the default scoring weights.
func (n *NopConfig) Scores() api.Scores { return api.DefaultScores }

// Search returns the configured search settings.
func (n *NopConfig) Search() api.SearchConfig { return n.SearchConfig }

// Adaptive returns the configured adaptive backoff settings.
func (n *NopConfig) Adaptive() api.AdaptiveConfig { return n.AdaptiveCfg }

// SyncConfig returns the configured subtitle sync settings.
func (n *NopConfig) SyncConfig() api.SyncConfig { return n.Sync }

// PostProcessConfig returns the configured post-processing settings.
func (n *NopConfig) PostProcessConfig() api.PostProcessConfig { return n.PostProcess }

// ProvidersForTarget returns all providers unchanged (no include/exclude filtering).
func (n *NopConfig) ProvidersForTarget(_ *api.SubtitleTarget, all []api.ProviderID) []api.ProviderID {
	return all
}

// MinScoreForTarget returns the configured minimum score.
func (n *NopConfig) MinScoreForTarget(_ *api.SubtitleTarget, _ api.MediaType) int { return n.MinScore }

// ProviderPriority returns the configured provider priority value.
func (n *NopConfig) ProviderPriority(_ api.ProviderID) int { return n.ProviderPrio }

// ProviderConfigs returns the configured provider configuration map.
func (n *NopConfig) ProviderConfigs() map[api.ProviderID]api.ProviderCfg { return n.ProviderCfgs }

// EmbeddedPolicy returns the configured embedded subtitle codec policy.
func (n *NopConfig) EmbeddedPolicy() api.EmbeddedPolicy { return n.Embedded }

// ResolveTargetsWithFallback returns the configured subtitle targets.
func (n *NopConfig) ResolveTargetsWithFallback(_ string, _ []string) []api.SubtitleTarget {
	return n.Targets
}

// LanguageCodes returns the configured language codes.
func (n *NopConfig) LanguageCodes() []string { return n.Languages }

// SonarrConfig returns the configured Sonarr connection settings.
func (n *NopConfig) SonarrConfig() api.ArrConfig { return n.SonarrCfg }

// RadarrConfig returns the configured Radarr connection settings.
func (n *NopConfig) RadarrConfig() api.ArrConfig { return n.RadarrCfg }

// ServerPort returns the default subflux port.
func (n *NopConfig) ServerPort() int { return 8374 }

// PollInterval returns a fixed 30s poll interval.
func (n *NopConfig) PollInterval() time.Duration { return 30 * time.Second }

// LoggingLevel returns the info log level.
func (n *NopConfig) LoggingLevel() api.LogLevel { return "info" }

// LoggingFormat returns the json log format.
func (n *NopConfig) LoggingFormat() api.LogFormat { return "json" }

// MediaRoots returns no media roots.
func (n *NopConfig) MediaRoots() []string { return nil }

// ValidatePath returns PathErr (nil by default, accepting every path).
func (n *NopConfig) ValidatePath(_ context.Context, _ string) error { return n.PathErr }

// RemoveUnderRoot returns PathErr when set; otherwise it removes the file,
// tolerating already-missing paths (mirrors the production loose semantics
// tests rely on for nonexistent-path delete flows).
func (n *NopConfig) RemoveUnderRoot(_ context.Context, path string) error {
	if n.PathErr != nil {
		return n.PathErr
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
		return err
	}
	return nil
}

// LanguageRulesForUI returns the configured language rules.
func (n *NopConfig) LanguageRulesForUI() api.LanguageRulesJSON { return n.LangRules }

// AuthEnabled reports auth as disabled.
func (n *NopConfig) AuthEnabled() bool { return false }

// BasicAuthEnabled reports basic auth as enabled.
func (n *NopConfig) BasicAuthEnabled() bool { return true }

// OIDCEnabled reports OIDC as disabled.
func (n *NopConfig) OIDCEnabled() bool { return false }

// OIDCConfig returns an empty OIDC configuration.
func (n *NopConfig) OIDCConfig() auth.OIDCConfig { return auth.OIDCConfig{} }

// SessionIdleTimeout returns a 24h idle timeout.
func (n *NopConfig) SessionIdleTimeout() time.Duration { return 24 * time.Hour }

// SessionAbsoluteTimeout returns a 7-day absolute timeout.
func (n *NopConfig) SessionAbsoluteTimeout() time.Duration { return 7 * 24 * time.Hour }

// CheckBreachedPasswords reports breach checking as disabled.
func (n *NopConfig) CheckBreachedPasswords() bool { return false }

// WebAuthnRPID returns an empty relying-party ID.
func (n *NopConfig) WebAuthnRPID() string { return "" }
