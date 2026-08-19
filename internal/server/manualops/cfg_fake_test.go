package manualops

import (
	"context"

	"github.com/cplieger/subflux/internal/api"
)

// fakeManualCfg is the config double for this package's tests. TEN methods out
// of the 37 a *config.Config offers, and the width is the union of two demands:
// pathValidator's ONE — all this package itself asks a config for — plus the 9
// of search.SearchCfg, because these tests build a REAL search.Engine over the
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
	// The engine's surface, spelled here rather than named: search.SearchCfg
	// is another package's interface, and a fixture that references it would
	// stop recording what THIS package's tests depend on.
	_ interface {
		Scores() api.Scores
		Search() api.SearchConfig
		Adaptive() api.AdaptiveConfig
		Sync() api.SyncConfig
		PostProcess() api.PostProcessConfig
		ProvidersForTarget(*api.SubtitleTarget, []api.ProviderID) []api.ProviderID
		MinScoreForTarget(*api.SubtitleTarget, api.MediaType) int
		ProviderPriority(api.ProviderID) int
		EmbeddedPolicy() api.EmbeddedPolicy
	} = fakeManualCfg{}
)

func (fakeManualCfg) ValidatePath(context.Context, string) error { return nil }

func (fakeManualCfg) Scores() api.Scores                 { return api.DefaultScores }
func (fakeManualCfg) Search() api.SearchConfig           { return api.SearchConfig{} }
func (fakeManualCfg) Adaptive() api.AdaptiveConfig       { return api.AdaptiveConfig{} }
func (fakeManualCfg) Sync() api.SyncConfig               { return api.SyncConfig{SyncSubtitles: true} }
func (fakeManualCfg) PostProcess() api.PostProcessConfig { return api.PostProcessConfig{} }
func (fakeManualCfg) EmbeddedPolicy() api.EmbeddedPolicy { return api.EmbeddedPolicy{} }

func (fakeManualCfg) ProvidersForTarget(_ *api.SubtitleTarget, all []api.ProviderID) []api.ProviderID {
	return all
}

func (fakeManualCfg) MinScoreForTarget(*api.SubtitleTarget, api.MediaType) int { return 0 }
func (fakeManualCfg) ProviderPriority(api.ProviderID) int                      { return 0 }
