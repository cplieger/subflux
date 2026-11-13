package assrt

import (
	"path"
	"strings"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
)

// labels are the langlist keys the map holds, in the order a hit's tags
// are offered; langdou (bilingual) names no one language.
var labels = []string{"langchs", "langcht", "langeng"}

var languages = language.MustProviderMap(provider.WirePairs(
	"langchs", "zh-Hans", "langcht", "zh-Hant", "langeng", "en",
))

// hitTags are the mapped tags of a hit's langlist.
func hitTags(list map[string]bool) []language.Tag {
	var out []language.Tag
	for _, l := range labels {
		if list[l] {
			tag, _ := languages.Tag(l)
			out = append(out, tag)
		}
	}
	return out
}

// segmentTag is the mapped tag a file name's last segment before its
// extension names (.chs, .gb, .cht, .big5, .eng), as language.ReadFilename
// reads it.
func segmentTag(name string) (language.Tag, bool) {
	base := path.Base(strings.ReplaceAll(name, `\`, "/"))
	stem := strings.TrimSuffix(base, path.Ext(base))
	_, seg, found := strings.CutLast(stem, ".")
	if !found {
		return language.Tag{}, false
	}
	tag, ok := language.ReadFilename(seg)
	if !ok || len(languages.Wires(tag)) == 0 {
		return language.Tag{}, false
	}
	return tag, true
}
