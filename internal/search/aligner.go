package search

import (
	"context"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

// Evidence is a series root's bound reading as its source's join stored it;
// *seriesorder.Service implements it. False is no reading.
type Evidence interface {
	Evidence(ctx context.Context, src media.SourceID, root mediasource.NativeRef) (mediasource.SeriesEvidence, bool, error)
}

// Aligner is the one timing-sync operation the engine consumes;
// *syncsched.Aligner implements it, and the engine tests script it
// (searchtest.Aligner).
type Aligner interface {
	Align(ctx context.Context, inst statedb.InstanceID, payload []byte, tag language.Tag, d timing.Decision) (syncsched.Aligned, error)
}
