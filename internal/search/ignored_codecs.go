package search

import (
	"github.com/cplieger/subflux/internal/api"
)

// ignoredCodecTable maps each embedded-policy flag to the codec names it
// suppresses. Adding a new ignored codec requires only a new table entry.
var ignoredCodecTable = []struct {
	enabled func(api.EmbeddedPolicy) bool
	codecs  []string
}{
	{func(p api.EmbeddedPolicy) bool { return p.IgnorePGS }, []string{"pgs"}},
	{func(p api.EmbeddedPolicy) bool { return p.IgnoreVobSub }, []string{"vobsub"}},
	{func(p api.EmbeddedPolicy) bool { return p.IgnoreASS }, []string{"ass", "ssa"}},
}

// IgnoredCodecsFromConfig builds the set of embedded codecs that should be
// treated as "present but not usable" from the typed embedded_subtitles
// policy. This is the ONE resolver every consumer (engine + server handlers)
// goes through. Accepts any type that provides EmbeddedPolicy() (satisfied
// by both api.ConfigProvider and search.SearchCfg).
func IgnoredCodecsFromConfig(cfg interface{ EmbeddedPolicy() api.EmbeddedPolicy }) map[string]bool {
	p := cfg.EmbeddedPolicy()
	ignored := make(map[string]bool, 4)
	for _, entry := range ignoredCodecTable {
		if !entry.enabled(p) {
			continue
		}
		for _, codec := range entry.codecs {
			ignored[codec] = true
		}
	}
	if len(ignored) == 0 {
		return nil
	}
	return ignored
}
