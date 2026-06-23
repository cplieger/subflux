package testsupport

import (
	"github.com/cplieger/subflux/internal/api"
)

// NopConfig is a zero-value implementation of search.SearchCfg for tests.
// Embed it in test-specific mocks and override only the methods you need.
// Note: the compile-time assertion against search.SearchCfg lives in
// search/nopconfig_assert_test.go to avoid testsupport→search→testsupport
// import cycles (search test code imports testsupport).
type NopConfig struct {
	ProviderCfgs map[api.ProviderID]api.ProviderCfg
	SearchConfig api.SearchConfig
	AdaptiveCfg  api.AdaptiveConfig
	Sync         api.SyncConfig
	PostProcess  api.PostProcessConfig
	MinScore     int
	ProviderPrio int
}

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
