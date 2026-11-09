package mediawrite_test

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
)

func TestDeleteRemovesFileAndRecord(t *testing.T) {
	t.Parallel()
	t.Run("managed", func(t *testing.T) {
		t.Parallel()
		l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
		rec, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello"))
		if err != nil {
			t.Fatal(err)
		}
		if err := l.w.Delete(t.Context(), mediawrite.RefOf(rec), expectOf(rec)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(l.path(rec.Name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the deleted file is still there: %v", err)
		}
		if names := recordNames(t, l.store, l.inst); len(names) != 0 {
			t.Errorf("records after the delete = %v, want none", names)
		}
		if n := pendingCount(t, l.store); n != 0 {
			t.Errorf("%d intents pending after a landed delete", n)
		}
	})
	t.Run("unmanaged", func(t *testing.T) {
		t.Parallel()
		l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
		writeFile(t, l.path("S01E01.en.srt"), srtOf("the user's own"))
		f := l.listOne(t)
		if err := l.store.PutExternal(t.Context(), l.inst, []statedb.LocalFile{f.LocalFile}); err != nil {
			t.Fatal(err)
		}
		if err := l.w.Delete(t.Context(), f.File, f.Expect); err != nil {
			t.Fatal(err)
		}
		if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.mkv"}) {
			t.Errorf("directory holds %v after deleting the unmanaged file", got)
		}
		rows, err := l.store.Coverage(t.Context(), l.key)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Coverage != statedb.Missing {
			t.Errorf("coverage after the delete = %+v, want English missing", rows)
		}
	})
}
