package search_test

import (
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/search"
)

// Compile-time assertion: *config.Config satisfies search.Cfg.
var _ search.Cfg = (*config.Config)(nil)
