package queryhandlers

import (
	"github.com/cplieger/subflux/internal/api"
)

// fakeQueryCfg is the config double for this package's tests. Its width is the
// UNION of two demands and nothing more: queryCfg's 11 methods, because
// reporting the configuration is this family's job, plus the 4 extra that
// search.SearchCfg adds (Sync, ProvidersForTarget, MinScoreForTarget,
// ProviderPriority) because the score-simulation and provider-timeout tests
// build a REAL search engine over the same value. 15 methods out of the 37 a
// *config.Config offers.
//
// A real *config.Config would need none, but these tests assert on what the
// handlers PROJECT from a config, so the fixture has to return exactly what the
// test set — a loaded config derives its targets from language rules, which
// would move the assertion onto the resolver instead of the handler.
type fakeQueryCfg struct {
	providers   map[api.ProviderID]api.ProviderCfg
	sonarrCfg   api.ArrConfig
	radarrCfg   api.ArrConfig
	langRules   api.LanguageRulesJSON
	languages   []string
	targets     []api.SubtitleTarget
	searchCfg   api.SearchConfig
	adaptiveCfg api.AdaptiveConfig
	syncCfg     api.SyncConfig
	postProcess api.PostProcessConfig
	embedded    api.EmbeddedPolicy
	minScore    int
	priority    int
}

// Both interfaces this fixture answers to, asserted so a width change here is a
// compile error rather than a confusing failure at the construction site.
var (
	_ queryCfg = (*fakeQueryCfg)(nil)
	_ interface {
		Scores() api.Scores
		Search() api.SearchConfig
		Adaptive() api.AdaptiveConfig
		Sync() api.SyncConfig
		PostProcess() api.PostProcessConfig
		ProvidersForTarget(*api.SubtitleTarget, []api.ProviderID) []api.ProviderID
		MinScoreForTarget(*api.SubtitleTarget, api.MediaType) int
		ProviderPriority(api.ProviderID) int
		EmbeddedPolicy() api.EmbeddedPolicy
	} = (*fakeQueryCfg)(nil)
)

// --- queryCfg ---

func (c *fakeQueryCfg) Providers() map[api.ProviderID]api.ProviderCfg { return c.providers }
func (c *fakeQueryCfg) LanguageCodes() []string                       { return c.languages }
func (c *fakeQueryCfg) LanguageRulesForUI() api.LanguageRulesJSON     { return c.langRules }

func (c *fakeQueryCfg) ResolveTargetsWithFallback(_ string, _ []string) []api.SubtitleTarget {
	return c.targets
}

func (c *fakeQueryCfg) EmbeddedPolicy() api.EmbeddedPolicy { return c.embedded }
func (c *fakeQueryCfg) Scores() api.Scores                 { return api.DefaultScores }
func (c *fakeQueryCfg) Search() api.SearchConfig           { return c.searchCfg }
func (c *fakeQueryCfg) Adaptive() api.AdaptiveConfig       { return c.adaptiveCfg }
func (c *fakeQueryCfg) PostProcess() api.PostProcessConfig { return c.postProcess }
func (c *fakeQueryCfg) Sonarr() api.ArrConfig              { return c.sonarrCfg }
func (c *fakeQueryCfg) Radarr() api.ArrConfig              { return c.radarrCfg }

// --- the four search.SearchCfg adds ---

func (c *fakeQueryCfg) Sync() api.SyncConfig { return c.syncCfg }

func (c *fakeQueryCfg) ProvidersForTarget(_ *api.SubtitleTarget, all []api.ProviderID) []api.ProviderID {
	return all
}

func (c *fakeQueryCfg) MinScoreForTarget(_ *api.SubtitleTarget, _ api.MediaType) int {
	return c.minScore
}

func (c *fakeQueryCfg) ProviderPriority(_ api.ProviderID) int { return c.priority }
