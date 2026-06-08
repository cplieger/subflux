package search_test

import (
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/search"
)

// Compile-time assertion: *config.Config satisfies search.SearchCfg.
var _ search.SearchCfg = (*config.Config)(nil)
