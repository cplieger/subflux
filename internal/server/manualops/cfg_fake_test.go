package manualops

import (
	"context"

	"github.com/cplieger/subflux/internal/subflux"
)

// fakeManualCfg is the config double for this package's tests. TEN methods out
// of the 37 a *config.Config offers, and the width is the union of two demands:
// pathValidator's ONE — all this package itself asks a config for — plus the 9
// of search.Cfg, because these tests build a REAL search.Engine over the
// same value to exercise scoring and the download pipeline end to end.
//
// Every method returns a zero value, which is what the suite needs: it asserts
// on the download pipeline's effects, and the scoring inputs come from the
// engine's own fixtures. ValidatePath accepts everything on purpose — the arr
// fakes here resolve to /media paths that no test-owned media root contains, so
// a real *config.Config would reject the fixture rather than exercise it.
type fakeManualCfg struct{}

var (
	_ pathValidator = fakeManualCfg{}
	// The engine's surface, spelled here rather than named: search.Cfg
	// is another package's interface, and a fixture that references it would
	// stop recording what THIS package's tests depend on.
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
	} = fakeManualCfg{}
)

func (fakeManualCfg) ValidatePath(context.Context, string) error { return nil }

func (fakeManualCfg) Scores() subflux.Scores                 { return subflux.DefaultScores }
func (fakeManualCfg) Search() subflux.SearchConfig           { return subflux.SearchConfig{} }
func (fakeManualCfg) Adaptive() subflux.AdaptiveConfig       { return subflux.AdaptiveConfig{} }
func (fakeManualCfg) Sync() subflux.SyncConfig               { return subflux.SyncConfig{SyncSubtitles: true} }
func (fakeManualCfg) PostProcess() subflux.PostProcessConfig { return subflux.PostProcessConfig{} }
func (fakeManualCfg) EmbeddedPolicy() subflux.EmbeddedPolicy { return subflux.EmbeddedPolicy{} }

func (fakeManualCfg) ProvidersForTarget(_ *subflux.SubtitleTarget, all []subflux.ProviderID) []subflux.ProviderID {
	return all
}

func (fakeManualCfg) MinScoreForTarget(*subflux.SubtitleTarget, subflux.MediaType) int { return 0 }
func (fakeManualCfg) ProviderPriority(subflux.ProviderID) int                          { return 0 }
