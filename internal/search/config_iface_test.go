package search_test

import (
	"subflux/internal/config"
	"subflux/internal/search"
)

// Compile-time assertion: *config.Config satisfies search.SearchCfg.
var _ search.SearchCfg = (*config.Config)(nil)
