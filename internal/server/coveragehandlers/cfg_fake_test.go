package coveragehandlers

import (
	"github.com/cplieger/subflux/internal/api"
)

// fakeCoverageCfg is the config double for this package's tests, sized to
// coverageCfg: THREE methods out of the 37 a *config.Config offers, and the
// exact surface the coverage handlers read. The tests set two of the three.
type fakeCoverageCfg struct {
	targets   []api.SubtitleTarget
	embedded  api.EmbeddedPolicy
	searchCfg api.SearchConfig
}

var _ coverageCfg = (*fakeCoverageCfg)(nil)

func (c *fakeCoverageCfg) ResolveTargetsWithFallback(_ string, _ []string) []api.SubtitleTarget {
	return c.targets
}

func (c *fakeCoverageCfg) EmbeddedPolicy() api.EmbeddedPolicy { return c.embedded }
func (c *fakeCoverageCfg) Search() api.SearchConfig           { return c.searchCfg }
