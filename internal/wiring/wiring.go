// Package wiring holds composition-root types that connect concrete
// implementations across the api, metrics, search, and provider packages.
//
// This package exists to break import cycles that would otherwise arise from
// defining wiring types (Func) in api/ alongside cross-cutting concerns.
// wiring/ depends on boundary packages without polluting either with the
// other's symbols.
//
// wiring/ is import-only by main.go (the composition root) and server/
// (which receives a Func via WithWire and calls it on each config reload).
// No package other than these two should import wiring/.
//
// wiring/ is also a convenient home for cross-package compile-time
// assertions where neither side can hold the assertion without creating
// a cycle (e.g. embedded.ProviderDirect satisfies search.TrackDetector,
// but search/ can't import embedded/ and embedded/ shouldn't import search/).
package wiring

import (
	"context"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider/embedded"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/syncing"
)

// Func creates the search engine, scorer, and loaded providers from config.
// Called during Start and hot reload. The context allows cancellation of
// provider initialization (e.g. network calls during provider setup).
type Func func(
	ctx context.Context,
	cfg api.ConfigProvider,
	db api.Store,
	m search.SearchMetrics,
) (api.SearchEngine, api.Scorer, []api.Provider, error)

// Compile-time assertion: embedded.ProviderDirect satisfies
// search.TrackDetector. This lives here (rather than in embedded/ or
// search/) to keep both of those packages decoupled from each other.
var _ search.TrackDetector = embedded.ProviderDirect{}

// Compile-time assertion: syncing.Syncer satisfies search.SubtitleSyncer.
// Moved here from search/ to decouple search from the syncing→subsync→ffmpeg chain.
var _ search.SubtitleSyncer = syncing.Syncer{}
