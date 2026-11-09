package seriesorder

import (
	"slices"

	"github.com/cplieger/subflux/internal/language"
)

// english is the language every order is asked in, and the metadata language
// of a root whose own has no code, as Sonarr asks
// (IsoLanguages.Get(language) ?? IsoLanguages.Get(Language.English),
// NzbDrone.Core/MetadataSource/SkyHook/SkyHookProxy.cs:44).
const english = "eng"

// metadataCode is SkyHook's {lang} segment for a root's metadata language.
func metadataCode(t language.Tag) string {
	if code, ok := language.ISO3(t); ok {
		return code
	}
	return english
}

// titleLanguages are a root's title languages, ascending and distinct:
// English, its metadata language and each target that has a code.
func titleLanguages(meta language.Tag, targets []language.Tag) []string {
	langs := []string{english, metadataCode(meta)}
	for _, t := range targets {
		if code, ok := language.ISO3(t); ok {
			langs = append(langs, code)
		}
	}
	slices.Sort(langs)
	return slices.Compact(langs)
}

func targetTags(p language.Policy) []language.Tag {
	targets := p.Targets()
	tags := make([]language.Tag, len(targets))
	for i, t := range targets {
		tags[i] = t.Tag
	}
	return tags
}
