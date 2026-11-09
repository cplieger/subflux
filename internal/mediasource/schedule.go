package mediasource

import (
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v4"
)

// Schedule is the scan section: how often scan checks each source for
// changes, and how long after a whole pass ends the next is due. It lives
// here, not in scan, so settings can hold it without importing scan, which
// imports status.
type Schedule struct{ Change, Whole time.Duration }

// ScheduleSchema is the scan section's form; its defaults and minima are
// the poll_interval and search.scan_interval settings it replaces.
var ScheduleSchema = formschema.Schema{Fields: []formschema.Field{
	{
		Key: "change_interval", Label: "Change check interval", Help: "How often subflux checks each source for imports, renames and deletes.",
		Kind: formschema.DurationMs, Default: "30s", Min: (10 * time.Second).Milliseconds(), Max: time.Hour.Milliseconds(),
	},
	{
		Key: "whole_interval", Label: "Full scan interval", Help: "Time between the end of one full library scan and the start of the next.",
		Kind: formschema.DurationMs, Default: "24h", Min: time.Hour.Milliseconds(), Max: (30 * 24 * time.Hour).Milliseconds(),
	},
}}

// ParseSchedule decodes the scan section, n being its node or nil when
// absent, reporting each error at scan.<key>; Decode marks both scalars
// plain.
func ParseSchedule(n *yaml.Node) (Schedule, []formschema.FieldError) {
	vals, errs := ScheduleSchema.Decode(n)
	if len(errs) > 0 {
		return Schedule{}, formschema.Prefix("scan", errs)
	}
	change := vals.Duration("change_interval")
	whole := vals.Duration("whole_interval")
	return Schedule{Change: change, Whole: whole}, nil
}
