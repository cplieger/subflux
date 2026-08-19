package coveragehandlers

import (
	"github.com/cplieger/subflux/internal/api"
)

// fakeCoverageCfg is the config double for this package's tests, sized to
// coverageCfg: THREE methods, the exact surface the coverage handlers read.
// It replaces the shared 28-method testsupport.NopConfig, of which these tests
// set two fields and the handlers dialed three methods.
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
