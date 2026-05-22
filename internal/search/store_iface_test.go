package search_test

import (
	"subflux/internal/search"
	"subflux/internal/store"
)

// Compile-time assertion: *store.DB satisfies search.SearchStore.
var _ search.SearchStore = (*store.DB)(nil)
