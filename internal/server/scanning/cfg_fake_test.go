package scanning

import (
	"github.com/cplieger/subflux/internal/api"
)

// fakeScanCfg is the zero-value config double for this package's tests, sized
// to ScanCfg and nothing else: FOUR methods, out of the 37 a *config.Config
// offers, because four is the whole surface a scan pass reads. Every one is
// reachable from the tests; a fixture wider than its interface is a fixture
// nobody can size against the subject.
type fakeScanCfg struct {
	targets     []api.SubtitleTarget
	languages   []string
	searchCfg   api.SearchConfig
	adaptiveCfg api.AdaptiveConfig
}

var _ ScanCfg = (*fakeScanCfg)(nil)

func (c *fakeScanCfg) Search() api.SearchConfig { return c.searchCfg }

func (c *fakeScanCfg) ResolveTargetsWithFallback(_ string, _ []string) []api.SubtitleTarget {
	return c.targets
}

func (c *fakeScanCfg) LanguageCodes() []string      { return c.languages }
func (c *fakeScanCfg) Adaptive() api.AdaptiveConfig { return c.adaptiveCfg }
