package wirespec_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
	"github.com/cplieger/subflux/internal/wirespec"
)

// mirror pairs a DTO with the owner struct it copies field for field, minus
// Skip and with Rename's owner-to-DTO names; the mirror test compares field
// names by reflect, so an owner field added later fails it.
type mirror struct {
	Doc, Owner any
	Rename     map[string]string
	Skip       []string
}

// mirrors lists every DTO that copies an owner struct.
var mirrors = []mirror{
	{Doc: wirespec.FactsDoc{}, Owner: rank.Facts{}},
	{Doc: wirespec.OutcomeDoc{}, Owner: timing.Outcome{}},
	{Doc: wirespec.DecisionDoc{}, Owner: timing.Decision{}},
	{Doc: wirespec.ProvenanceDoc{}, Owner: statedb.Provenance{}},
	{Doc: wirespec.FlagsDoc{}, Owner: language.Flags{}},
	{Doc: wirespec.ExternalIDsDoc{}, Owner: media.ExternalIDs{}},
	{Doc: wirespec.NumberingDoc{}, Owner: media.Numbering{}},
	{Doc: wirespec.FieldDoc{}, Owner: formschema.Field{}},
	{Doc: wirespec.JobDoc{}, Owner: status.Job{}, Skip: []string{"Key"}, Rename: map[string]string{"Owner": "OwnerID"}},
}

// mirrorFaults lists how m's DTO fields differ from its owner's, after the
// owner's skips and renames.
func mirrorFaults(m mirror) []string {
	want := map[string]bool{}
	owner := reflect.TypeOf(m.Owner)
	for f := range owner.Fields() {
		if !f.IsExported() || slices.Contains(m.Skip, f.Name) {
			continue
		}
		name := f.Name
		if r, ok := m.Rename[name]; ok {
			name = r
		}
		want[name] = true
	}
	var faults []string
	doc := reflect.TypeOf(m.Doc)
	for f := range doc.Fields() {
		name := f.Name
		if !want[name] {
			faults = append(faults, doc.Name()+"."+name+" has no owner field")
		}
		delete(want, name)
	}
	for name := range want {
		faults = append(faults, owner.Name()+"."+name+" is missing from "+doc.Name())
	}
	for _, s := range m.Skip {
		if _, ok := owner.FieldByName(s); !ok {
			faults = append(faults, "skip "+s+" names no field of "+owner.Name())
		}
	}
	for from := range m.Rename {
		if _, ok := owner.FieldByName(from); !ok {
			faults = append(faults, "rename "+from+" names no field of "+owner.Name())
		}
	}
	slices.Sort(faults)
	return faults
}

func TestMirrorsTrackOwners(t *testing.T) {
	for _, m := range mirrors {
		t.Run(reflect.TypeOf(m.Doc).Name(), func(t *testing.T) {
			if faults := mirrorFaults(m); len(faults) > 0 {
				t.Errorf("mirror drifted: %v", faults)
			}
		})
	}
	t.Run("an owner field the DTO lacks fails", func(t *testing.T) {
		type owner struct{ HI, Forced, Unseen bool }
		if faults := mirrorFaults(mirror{Doc: wirespec.FlagsDoc{}, Owner: owner{}}); len(faults) != 1 {
			t.Errorf("faults = %v, want one for Unseen", faults)
		}
	})
	t.Run("a stale skip fails", func(t *testing.T) {
		type owner struct{ HI, Forced bool }
		if faults := mirrorFaults(mirror{Doc: wirespec.FlagsDoc{}, Owner: owner{}, Skip: []string{"Gone"}}); len(faults) != 1 {
			t.Errorf("faults = %v, want one for the skip", faults)
		}
	})
}
