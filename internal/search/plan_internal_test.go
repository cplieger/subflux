package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/statedb"
)

func TestUnwritableSkipsAutomatic(t *testing.T) {
	items := []runItem{{item: mediasource.Item{}}}
	excluded := []runItem{{item: mediasource.Item{}}, {item: mediasource.Item{Excluded: true}}}
	cases := []struct {
		name       string
		items      []runItem
		cause      statedb.WorkCause
		unwritable bool
		want       bool
	}{
		{"a scan of an unwritable directory", items, statedb.Scan, true, true},
		{"an import of an unwritable directory", items, statedb.Import, true, true},
		{"a user's run of an unwritable directory", items, statedb.Item, true, false},
		{"a scan with an excluded item", excluded, statedb.Scan, false, true},
		{"a user's run with an excluded item", excluded, statedb.Item, false, false},
		{"a scan of a writable directory", items, statedb.Scan, false, false},
	}
	for _, c := range cases {
		if got := heldBack(c.cause, c.items, c.unwritable); got != c.want {
			t.Errorf("%s: held back %v, want %v", c.name, got, c.want)
		}
	}
}
