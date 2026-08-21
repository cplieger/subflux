package queryhandlers

import (
	"github.com/cplieger/subflux/internal/subflux"
)

// fakeQueryCfg is the config double for this package's tests. Its width is the
// UNION of two demands and nothing more: queryCfg's 11 methods, because
// reporting the configuration is this family's job, plus the 4 extra that
// search.Cfg adds (Sync, ProvidersForTarget, MinScoreForTarget,
// ProviderPriority) because the score-simulation and provider-timeout tests
// build a REAL search engine over the same value. 15 methods out of the 37 a
// *config.Config offers.
//
// A real *config.Config would need none, but these tests assert on what the
// handlers PROJECT from a config, so the fixture has to return exactly what the
// test set — a loaded config derives its targets from language rules, which
// would move the assertion onto the resolver instead of the handler.
type fakeQueryCfg struct {
	providers   map[subflux.ProviderID]subflux.ProviderCfg
	sonarrCfg   subflux.ArrConfig
	radarrCfg   subflux.ArrConfig
	langRules   subflux.LanguageRulesJSON
	languages   []string
	targets     []subflux.SubtitleTarget
	searchCfg   subflux.SearchConfig
	adaptiveCfg subflux.AdaptiveConfig
	syncCfg     subflux.SyncConfig
	postProcess subflux.PostProcessConfig
	embedded    subflux.EmbeddedPolicy
	minScore    int
	priority    int
}

// Both interfaces this fixture answers to, asserted so a width change here is a
// compile error rather than a confusing failure at the construction site.
var (
	_ queryCfg = (*fakeQueryCfg)(nil)
	_ interface {
		Scores() subflux.Scores
		Search() subflux.SearchConfig
		Adaptive() subflux.AdaptiveConfig
		Sync() subflux.SyncConfig
		PostProcess() subflux.PostProcessConfig
		ProvidersForTarget(*subflux.SubtitleTarget, []subflux.ProviderID) []subflux.ProviderID
		MinScoreForTarget(*subflux.SubtitleTarget, subflux.MediaType) int
		ProviderPriority(subflux.ProviderID) int
		EmbeddedPolicy() subflux.EmbeddedPolicy
	} = (*fakeQueryCfg)(nil)
)

// --- queryCfg ---

func (c *fakeQueryCfg) Providers() map[subflux.ProviderID]subflux.ProviderCfg { return c.providers }
func (c *fakeQueryCfg) LanguageCodes() []string                               { return c.languages }
func (c *fakeQueryCfg) LanguageRulesForUI() subflux.LanguageRulesJSON         { return c.langRules }

func (c *fakeQueryCfg) ResolveTargetsWithFallback(_ string, _ []string) []subflux.SubtitleTarget {
	return c.targets
}

func (c *fakeQueryCfg) EmbeddedPolicy() subflux.EmbeddedPolicy { return c.embedded }
func (c *fakeQueryCfg) Scores() subflux.Scores                 { return subflux.DefaultScores }
func (c *fakeQueryCfg) Search() subflux.SearchConfig           { return c.searchCfg }
func (c *fakeQueryCfg) Adaptive() subflux.AdaptiveConfig       { return c.adaptiveCfg }
func (c *fakeQueryCfg) PostProcess() subflux.PostProcessConfig { return c.postProcess }
func (c *fakeQueryCfg) Sonarr() subflux.ArrConfig              { return c.sonarrCfg }
func (c *fakeQueryCfg) Radarr() subflux.ArrConfig              { return c.radarrCfg }

// --- the four search.Cfg adds ---

func (c *fakeQueryCfg) Sync() subflux.SyncConfig { return c.syncCfg }

func (c *fakeQueryCfg) ProvidersForTarget(_ *subflux.SubtitleTarget, all []subflux.ProviderID) []subflux.ProviderID {
	return all
}

func (c *fakeQueryCfg) MinScoreForTarget(_ *subflux.SubtitleTarget, _ subflux.MediaType) int {
	return c.minScore
}

func (c *fakeQueryCfg) ProviderPriority(_ subflux.ProviderID) int { return c.priority }
