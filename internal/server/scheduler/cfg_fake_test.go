package scheduler_test

import (
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/scanning"
)

// fakeScanCfg is the zero-value config double for the scheduler tests, sized to
// scanning.ScanCfg — the type LiveState.Cfg names, because the scheduler reads
// Search() for its own interval and carries the same value into the scan pass.
// FOUR methods, replacing the shared 28-method testsupport.NopConfig these
// tests used to hand in, of which they read one.
type fakeScanCfg struct {
	targets     []api.SubtitleTarget
	languages   []string
	searchCfg   api.SearchConfig
	adaptiveCfg api.AdaptiveConfig
}

var _ scanning.ScanCfg = (*fakeScanCfg)(nil)

func (c *fakeScanCfg) Search() api.SearchConfig { return c.searchCfg }

func (c *fakeScanCfg) ResolveTargetsWithFallback(_ string, _ []string) []api.SubtitleTarget {
	return c.targets
}

func (c *fakeScanCfg) LanguageCodes() []string      { return c.languages }
func (c *fakeScanCfg) Adaptive() api.AdaptiveConfig { return c.adaptiveCfg }
