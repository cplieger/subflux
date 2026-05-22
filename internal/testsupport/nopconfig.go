package testsupport

import (
	"subflux/internal/api"
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

func (n *NopConfig) Scores() api.Scores                       { return api.DefaultScores }
func (n *NopConfig) Search() api.SearchConfig                 { return n.SearchConfig }
func (n *NopConfig) Adaptive() api.AdaptiveConfig             { return n.AdaptiveCfg }
func (n *NopConfig) SyncConfig() api.SyncConfig               { return n.Sync }
func (n *NopConfig) PostProcessConfig() api.PostProcessConfig { return n.PostProcess }
func (n *NopConfig) ProvidersForTarget(_ *api.SubtitleTarget, all []api.ProviderID) []api.ProviderID {
	return all
}
func (n *NopConfig) MinScoreForTarget(_ *api.SubtitleTarget, _ api.MediaType) int { return n.MinScore }
func (n *NopConfig) ProviderPriority(_ api.ProviderID) int                        { return n.ProviderPrio }
func (n *NopConfig) ProviderConfigs() map[api.ProviderID]api.ProviderCfg          { return n.ProviderCfgs }
