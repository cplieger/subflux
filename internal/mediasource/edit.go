package mediasource

import (
	"slices"

	"github.com/cplieger/subflux/internal/formschema"
)

// EntryEdit is how a sources entry differs from its earlier form with the
// same ID.
type EntryEdit uint8

const (
	// EntrySame is no field changed; Index is the entry's position in the
	// section, not its configuration, so a moved entry is the same.
	EntrySame EntryEdit = iota
	// EntryRekeyed is only fields the kind's schema marks Secret changed: a
	// credential names no other instance.
	EntryRekeyed
	// EntryChanged is the type or any other field changed.
	EntryChanged
)

// CompareEntry answers how cur differs from prev, the earlier entry with
// its ID; kinds supplies the schema marking the Secret fields, and a type
// kinds lacks is EntryChanged.
func CompareEntry(kinds []AdapterKind, prev, cur *Config) EntryEdit {
	k := slices.IndexFunc(kinds, func(k AdapterKind) bool { return k.Type == cur.Type })
	if k < 0 {
		return EntryChanged
	}
	return compareEntry(kinds[k].Schema, prev, cur)
}

func compareEntry(schema formschema.Schema, prev, cur *Config) EntryEdit {
	if prev.Type != cur.Type || !slices.Equal(prev.PathMappings, cur.PathMappings) || !slices.Equal(prev.ExcludeTags, cur.ExcludeTags) ||
		valuesDiffer(schema, prev.Values, cur.Values, func(f formschema.Field) bool { return f.Kind != formschema.Secret }) {
		return EntryChanged
	}
	if valuesDiffer(schema, prev.Values, cur.Values, func(f formschema.Field) bool { return f.Kind == formschema.Secret }) {
		return EntryRekeyed
	}
	return EntrySame
}
