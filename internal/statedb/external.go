package statedb

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
)

// LocalFile is a subtitle file beside the video that subflux does not
// manage.
type LocalFile struct {
	Name  string
	Tag   language.Tag
	Flags language.Flags
}

// PutExternal stores inst's local-file set, empty included, and recomputes
// its items' coverage in the same write, also when the set is unchanged:
// with a probe stored the inventory is complete, so a lock nothing serves
// is deleted. Files are stored by name; an empty or repeated name, or an
// unknown instance, is refused, writing nothing.
func (s *Store) PutExternal(ctx context.Context, inst InstanceID, files []LocalFile) error {
	row := localRow{Files: make([]localFileRow, 0, len(files))}
	for _, f := range files {
		if f.Name == "" {
			return fmt.Errorf("statedb: put external: instance %d lists a file with no name", inst)
		}
		row.Files = append(row.Files, localFileRow{Name: f.Name, Tag: rowTag{t: f.Tag}, Flags: flagsByte(f.Flags)})
	}
	slices.SortFunc(row.Files, func(a, b localFileRow) int { return cmp.Compare(a.Name, b.Name) })
	for i := 1; i < len(row.Files); i++ {
		if row.Files[i-1].Name == row.Files[i].Name {
			return fmt.Errorf("statedb: put external: instance %d lists %q twice", inst, row.Files[i].Name)
		}
	}
	return s.putInventory(ctx, "put external", inst, true, func(tx *bolttable.Tx) error {
		return s.t.local.Put(tx, inst, &row)
	})
}
