package testsupport_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/testsupport"
)

// TestNopConfigContract verifies NopConfig satisfies search.SearchCfg at runtime
// and returns sensible zero values.
func TestNopConfigContract(t *testing.T) {
	var cfg search.SearchCfg = &testsupport.NopConfig{}

	if s := cfg.Scores(); s == (api.Scores{}) {
		t.Fatal("Scores() returned zero Scores; expected DefaultScores")
	}
	_ = cfg.Search()
	_ = cfg.Adaptive()
	_ = cfg.SyncConfig()
	_ = cfg.PostProcessConfig()

	all := []api.ProviderID{"a", "b"}
	got := cfg.ProvidersForTarget(nil, all)
	if len(got) != len(all) {
		t.Fatalf("ProvidersForTarget: got %d, want %d", len(got), len(all))
	}
	if cfg.MinScoreForTarget(nil, api.MediaTypeMovie) != 0 {
		t.Fatal("MinScoreForTarget: expected 0")
	}
	if cfg.ProviderPriority("x") != 0 {
		t.Fatal("ProviderPriority: expected 0")
	}
	if cfg.ProviderConfigs() != nil {
		t.Fatal("ProviderConfigs: expected nil for zero NopConfig")
	}
}
