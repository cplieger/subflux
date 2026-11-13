package subsro

import (
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
)

// languages are the item languages Subs.ro's OpenAPI document lists
// (testdata/openapi.yaml), less "alt", which names none.
var languages = language.MustProviderMap(provider.WirePairs(
	"ro", "ro", "en", "en", "ita", "it", "fra", "fr", "ger", "de",
	"ung", "hu", "gre", "el", "por", "pt", "spa", "es",
))
