// Package wiring holds composition-root types that connect concrete
// implementations across the config, provider, scorer, search and embedded
// packages.
//
// This package exists to break import cycles that would otherwise arise from
// defining wiring types (Func) in the shared types package alongside
// cross-cutting concerns. wiring/ depends on boundary packages without
// polluting either with the other's symbols.
//
// wiring/ is import-only by main.go (the composition root) and server/
// (which receives a Func via WithWire and calls it on each config reload).
// No package other than these two should import wiring/.
//
// wiring/ is also a convenient home for cross-package compile-time
// assertions where neither side can hold the assertion without creating
// a cycle (e.g. embedded.Detector satisfies search.TrackDetector,
// but search/ can't import embedded/ and embedded/ shouldn't import search/).
package wiring

import (
	"context"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/syncing"
)

// Func creates the search engine, scorer, and loaded providers from config.
// Called during Start and hot reload. The context allows cancellation of
// provider initialization (e.g. network calls during provider setup).
//
// The engine is returned CONCRETE. This is the composition root's signature,
// and a root is the one place that is entitled to know the implementation: the
// consumers it feeds each declare the two or three engine methods they call
// (scanning.ScanEngine, and the unexported ones in polling, manualops and
// queryhandlers), so widening it back into a single eight-method interface here
// would only re-create the hub those declarations replaced.
//
// The config is taken CONCRETE for the mirror-image reason. Both ends of this
// signature are composition roots — main.go supplies the func, internal/server
// holds the live *config.Config and calls it — so neither end has anything to
// hide from the other. What the wiring reads (Providers, Scores, Sync) plus what
// it hands to search.WithConfig (Cfg's 9) is 12 of the type's 37 methods,
// but a wiring-owned interface stating those 12 would be a THIRD name for a
// surface search already declares and config already implements.
type Func func(
	ctx context.Context,
	cfg *config.Config,
	db search.Store,
	m search.Metrics,
) (*search.Engine, *scorer.Engine, []provider.Provider, error)

// Compile-time assertion: embedded.Detector satisfies
// search.TrackDetector. This lives here (rather than in embedded/ or
// search/) to keep both of those packages decoupled from each other.
var _ search.TrackDetector = embedded.Detector{}

// Compile-time assertion: syncing.Syncer satisfies search.SubtitleSyncer.
// Moved here from search/ to decouple search from the syncing→subsync→ffmpeg chain.
var _ search.SubtitleSyncer = syncing.Syncer{}
