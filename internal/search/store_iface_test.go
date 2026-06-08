package search_test

import (
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/store"
)

// Compile-time assertion: *store.DB satisfies search.SearchStore.
var _ search.SearchStore = (*store.DB)(nil)
