package scheduler_test

import (
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/subflux"
)

// fakeScanCfg is the zero-value config double for the scheduler tests, sized to
// scanning.ScanCfg — the type LiveState.Cfg names, because the scheduler reads
// Search() for its own interval and carries the same value into the scan pass.
// FOUR methods out of the 37 a *config.Config offers, of which these tests
// read one — Search(), for the scan interval.
type fakeScanCfg struct {
	targets     []subflux.SubtitleTarget
	languages   []string
	searchCfg   subflux.SearchConfig
	adaptiveCfg subflux.AdaptiveConfig
}

var _ scanning.ScanCfg = (*fakeScanCfg)(nil)

func (c *fakeScanCfg) Search() subflux.SearchConfig { return c.searchCfg }

func (c *fakeScanCfg) ResolveTargetsWithFallback(_ string, _ []string) []subflux.SubtitleTarget {
	return c.targets
}

func (c *fakeScanCfg) LanguageCodes() []string          { return c.languages }
func (c *fakeScanCfg) Adaptive() subflux.AdaptiveConfig { return c.adaptiveCfg }
