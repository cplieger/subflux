package search_test

import (
	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/search"
)

// Compile-time assertion: *boltstore.DB satisfies search.SearchStore.
var _ search.SearchStore = (*boltstore.DB)(nil)
