package statedb

import (
	"slices"
	"testing"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
)

func TestInstanceDeleteListsRootInWritten(t *testing.T) {
	s := newStore(t)
	e1, e2 := mustKey(t, "tvdb-5-s01e01"), mustKey(t, "tvdb-5-s01e02")
	en := rowTag{t: mustTag(t, "en")}
	update(t, s, putInstance(s, 1, e1, e2))
	update(t, s, func(tx *bolttable.Tx) error {
		for _, name := range []string{"a.en.srt", "b.en.srt"} {
			if err := s.t.files.Put(tx, fileKey{Inst: 1, Name: name}, &fileRow{Tag: en, Timing: []byte{0}}); err != nil {
				return err
			}
		}
		return nil
	})
	before := headRev(t, s)
	update(t, s, func(tx *bolttable.Tx) error {
		row, found, err := s.deleteInstance(tx, 1)
		if err == nil && (!found || len(row.Items) != 2) {
			t.Errorf("deleteInstance: found %v, %d items; want the stored row", found, len(row.Items))
		}
		return err
	})
	root := []media.RootKey{e1.Root()}
	if d := since(t, s, before); !slices.Equal(d.Written, root) || !slices.Equal(d.Roots, root) {
		t.Errorf("got Written %v Roots %v, want %v in both", d.Written, d.Roots, root)
	}
	if n := coreRows(t, s); n != 0 {
		t.Errorf("%d core rows left, want the instance and its files gone", n)
	}
	update(t, s, func(tx *bolttable.Tx) error {
		if _, found, err := s.deleteInstance(tx, 1); err != nil || found {
			t.Errorf("deleting it again: found %v, %v; want false, nil", found, err)
		}
		return nil
	})
}
