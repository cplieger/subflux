package server

import (
	"context"

	"github.com/cplieger/subflux/internal/server/sessionbatch"
)

// sessionBatchUpdater is the interface for batch session activity updates.
type sessionBatchUpdater = sessionbatch.BatchUpdater

// sessionActivityBatcher is a type alias for sessionbatch.Batcher,
// preserving backward compatibility within the server package.
type sessionActivityBatcher = sessionbatch.Batcher

// newSessionActivityBatcher creates and starts a batcher.
func newSessionActivityBatcher(ctx context.Context, db sessionBatchUpdater) *sessionActivityBatcher {
	return sessionbatch.New(ctx, db)
}
