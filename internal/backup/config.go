// Package backup is the backup settings section and the runner that keeps
// scheduled copies of the state store: each copy's file name is its time, so
// the newest name is the last success and a restart never skips one.
package backup

import (
	"path/filepath"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v4"
)

// SectionKey is the backup section's key in config.yaml.
const SectionKey = "backup"

const (
	fieldEnabled   = "enabled"
	fieldFrequency = "frequency"
	fieldRetention = "retention"
	fieldPath      = "path"
)

// Schema is the backup section's form.
var Schema = formschema.Schema{Fields: []formschema.Field{
	{
		Key: fieldEnabled, Label: "Scheduled backups", Kind: formschema.Bool, Default: "false",
		Help: "Copy the database to the backup folder on a schedule.",
	},
	{
		Key: fieldFrequency, Label: "Frequency", Kind: formschema.DurationMs, Default: "24h",
		Min: time.Hour.Milliseconds(), Max: (30 * 24 * time.Hour).Milliseconds(),
		Help: "Time between copies, from 1h to 30 days.",
	},
	{
		Key: fieldRetention, Label: "Copies kept", Kind: formschema.Int, Default: "7", Min: 1, Max: 365,
		Help: "How many copies to keep; the oldest beyond it are deleted.",
	},
	{
		Key: fieldPath, Label: "Backup folder", Kind: formschema.String, Default: "/config/backups",
		Help: "An absolute folder other than the database's own, so a tool backing up this folder never reads the live database.",
	},
}}

// Config is the backup section. Its zero value is disabled.
type Config struct {
	path      string
	frequency time.Duration
	retention int
	enabled   bool
}

// ParseConfig decodes the backup section, n being its node or nil when
// absent, reporting each error at backup.<key>. storeDir is the state
// store's directory, which path may not name.
func ParseConfig(n *yaml.Node, storeDir string) (Config, []formschema.FieldError) {
	vals, errs := Schema.Decode(n)
	if len(errs) > 0 {
		return Config{}, formschema.Prefix(SectionKey, errs)
	}
	c := Config{
		enabled:   vals.Bool(fieldEnabled),
		frequency: vals.Duration(fieldFrequency),
		retention: int(vals.Int(fieldRetention)),
		path:      vals.String(fieldPath),
	}
	switch {
	case !filepath.IsAbs(c.path) || filepath.Clean(c.path) != c.path:
		errs = append(errs, formschema.FieldError{Path: fieldPath, Message: "must be an absolute folder with no trailing slash, . or .. part"})
	case c.path == filepath.Clean(storeDir):
		errs = append(errs, formschema.FieldError{Path: fieldPath, Message: "must not be the database's own folder, " + storeDir})
	}
	if len(errs) > 0 {
		return Config{}, formschema.Prefix(SectionKey, errs)
	}
	return c, nil
}
