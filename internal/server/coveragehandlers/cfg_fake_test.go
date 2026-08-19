package coveragehandlers

import (
	"github.com/cplieger/subflux/internal/subflux"
)

// fakeCoverageCfg is the config double for this package's tests, sized to
// coverageCfg: THREE methods out of the 37 a *config.Config offers, and the
// exact surface the coverage handlers read. The tests set two of the three.
type fakeCoverageCfg struct {
	targets   []subflux.SubtitleTarget
	embedded  subflux.EmbeddedPolicy
	searchCfg subflux.SearchConfig
}

var _ coverageCfg = (*fakeCoverageCfg)(nil)

func (c *fakeCoverageCfg) ResolveTargetsWithFallback(_ string, _ []string) []subflux.SubtitleTarget {
	return c.targets
}

func (c *fakeCoverageCfg) EmbeddedPolicy() subflux.EmbeddedPolicy { return c.embedded }
func (c *fakeCoverageCfg) Search() subflux.SearchConfig           { return c.searchCfg }
