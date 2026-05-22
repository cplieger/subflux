package store

import "subflux/internal/api"

// Compile-time assertion: *DB satisfies api.SyncOffsetStore.
var _ api.SyncOffsetStore = (*DB)(nil)
