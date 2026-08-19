package scanning

import (
	"github.com/cplieger/subflux/internal/subflux"
)

// fakeScanCfg is the zero-value config double for this package's tests, sized
// to ScanCfg and nothing else: FOUR methods, out of the 37 a *config.Config
// offers, because four is the whole surface a scan pass reads. Every one is
// reachable from the tests; a fixture wider than its interface is a fixture
// nobody can size against the subject.
type fakeScanCfg struct {
	targets     []subflux.SubtitleTarget
	languages   []string
	searchCfg   subflux.SearchConfig
	adaptiveCfg subflux.AdaptiveConfig
}

var _ ScanCfg = (*fakeScanCfg)(nil)

func (c *fakeScanCfg) Search() subflux.SearchConfig { return c.searchCfg }

func (c *fakeScanCfg) ResolveTargetsWithFallback(_ string, _ []string) []subflux.SubtitleTarget {
	return c.targets
}

func (c *fakeScanCfg) LanguageCodes() []string          { return c.languages }
func (c *fakeScanCfg) Adaptive() subflux.AdaptiveConfig { return c.adaptiveCfg }
