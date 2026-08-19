package scanning

import (
	"github.com/cplieger/subflux/internal/api"
)

// fakeScanCfg is the zero-value config double for this package's tests, sized
// to ScanCfg and nothing else: FOUR methods, because that is the whole surface
// a scan pass reads. It replaces the shared 28-method testsupport.NopConfig
// these tests used to embed, of which they exercised none — the fake was there
// to satisfy a composite, and the composite is gone.
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
