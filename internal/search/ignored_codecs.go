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
// goes through.
//
// The parameter is an anonymous one-method interface because that is the whole
// of what this resolver reads: 1 of the 28 values the configuration offers.
// Every caller's own config surface already carries EmbeddedPolicy, so each
// satisfies this structurally without naming anything.
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
