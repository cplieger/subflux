package jimaku

import (
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
)

// languages holds Jimaku's one language: it lists Japanese subtitles only
// and labels none.
var languages = language.MustProviderMap(provider.WirePairs("ja", "ja"))

// japanese is the tag every candidate carries.
var japanese = languages.Tags()[0]
