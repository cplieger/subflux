package search_test

import (
	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/search"
)

// Compile-time assertion: *boltstore.DB satisfies search.Store.
var _ search.Store = (*boltstore.DB)(nil)
