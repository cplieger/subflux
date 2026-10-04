package schema

import (
	"strconv"

	"github.com/cplieger/subflux/internal/config/defaults"
	"github.com/cplieger/subflux/internal/subflux"
)

// embeddedSection builds the schema for the top-level embedded_subtitles
// section: the typed codec policy for embedded subtitle tracks. Field
// defaults render from the same defaults package constants the config
// loader pre-populates, so the declaration exists exactly once.
func embeddedSection() subflux.SchemaSection {
	return subflux.SchemaSection{
		Key: "embedded_subtitles", Title: "Embedded Subtitles", Type: fieldFields,
		Help: "Embedded tracks are always detected for coverage. These toggles only decide which codecs count as usable subtitles.",
		Fields: []subflux.SchemaField{
			{
				Key: "ignore_pgs", Label: "Ignore PGS", Type: fieldBool,
				Default: strconv.FormatBool(defaults.EmbeddedIgnorePGS),
				Help:    "Treat Blu-ray PGS bitmap subtitles as not usable. They stay in coverage, but external subtitles are searched.",
			},
			{
				Key: "ignore_vobsub", Label: "Ignore VobSub", Type: fieldBool,
				Default: strconv.FormatBool(defaults.EmbeddedIgnoreVobSub),
				Help:    "Treat DVD VobSub bitmap subtitles as not usable. They stay in coverage, but external subtitles are searched.",
			},
			{
				Key: "ignore_ass", Label: "Ignore ASS", Type: fieldBool,
				Default: strconv.FormatBool(defaults.EmbeddedIgnoreASS),
				Help:    "Treat ASS/SSA styled subtitles, common in anime, as not usable. They stay in coverage, but external subtitles are searched.",
			},
		},
	}
}
