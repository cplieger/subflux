// Package wiring holds composition-root types that connect concrete
// implementations across the config, provider, scorer, search and embedded
// packages.
//
// This package exists to break import cycles that would otherwise arise from
// defining wiring types (Func) in the shared types package alongside
// cross-cutting concerns. wiring/ depends on boundary packages without
// polluting either with the other's symbols.
//
// wiring/ is imported by main.go (the composition root, which builds a Func
// over Build) and by server/ (which receives the Func via WithWire and calls
// it on each config reload). No other package should import wiring/.
//
// wiring/ is also the home for cross-package compile-time assertions where
// neither side can hold the assertion without creating a cycle.
package wiring

import (
	"context"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subflux"
)

// Func creates the search engine, scorer, loaded providers and their gate
// binding from config. Called during Start and hot reload.
//
// The engine and config are CONCRETE because both ends of this signature are
// composition roots: main.go supplies the func and internal/server holds the
// live *config.Config and calls it, so neither has anything to hide from the
// other.
type Func func(
	ctx context.Context,
	cfg *config.Config,
	db search.Store,
	m search.Metrics,
) (Result, error)

// Result is what one Func call builds. Binding is the gate's view of exactly
// the providers in Providers.
type Result struct {
	Engine    *search.Engine
	Scorer    *scorer.Engine
	Binding   *providergate.Binding
	Providers []provider.Provider
}

// Extras are the process-lifetime collaborators shared by every engine Build
// creates, across reloads.
type Extras struct {
	SyncExec syncing.SyncExec
	Tracks   search.TrackDetector
	Media    *mediawrite.Writer
}

// Build loads the enabled providers, binds the ones that loaded to gate under
// their normalized settings, and builds the engine over them. x.Media is
// required.
func Build(ctx context.Context, cfg *config.Config, db search.Store, m search.Metrics,
	reg *provider.Registry, gate *providergate.Gate, x Extras,
) (Result, error) {
	configured := cfg.Providers()
	loaded, err := reg.LoadAll(ctx, configured)
	if err != nil {
		return Result{}, err
	}
	settings := make(map[subflux.ProviderID]map[string]any, len(loaded))
	for _, p := range loaded {
		settings[p.Name()] = reg.Normalize(p.Name(), configured[p.Name()].Settings)
	}
	binding := gate.Bind(settings)

	providers := provider.WrapRetryAll(loaded, provider.DownloadRetryAttempts, provider.DownloadRetryInitBackoff)
	scores := cfg.Scores()
	sc := scorer.New(&scores)
	engine := search.New(providers,
		search.WithStore(db), search.WithConfig(cfg),
		search.WithMetrics(m), search.WithScorer(sc),
		search.WithSyncer(syncing.Syncer{MinConfidence: cfg.Sync().SyncMinConfidence, LangMapper: classify.Alpha2FromAlpha3, Exec: x.SyncExec}),
		search.WithSyncExec(x.SyncExec),
		search.WithTracks(x.Tracks),
		search.WithMediaWriter(x.Media),
		search.WithProviderGate(binding))
	return Result{Engine: engine, Scorer: sc, Binding: binding, Providers: providers}, nil
}

// embedded.Detector satisfies search.TrackDetector; asserted here because
// neither package may import the other.
var _ search.TrackDetector = embedded.Detector{}

// syncing.Syncer satisfies search.SubtitleSyncer; asserted here to keep search
// free of the syncing, subsync and ffmpeg chain.
var _ search.SubtitleSyncer = syncing.Syncer{}
