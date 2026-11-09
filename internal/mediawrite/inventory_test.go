package mediawrite_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

func names(inv mediawrite.Inventory) []string {
	var out []string
	for _, f := range inv.Files {
		out = append(out, f.Name)
	}
	return out
}

func TestSearchListReadsNoUnmanagedBytes(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	langs := []string{"en", "fr", "de", "es", "it", "pt", "nl", "sv", "da", "fi"}
	videos := make([]string, 0, 500)
	for i := range 500 {
		v := filepath.Join(b, "Show", fmt.Sprintf("E%03d", i+1), fmt.Sprintf("E%03d.mkv", i+1))
		writeFile(t, v, "video")
		for _, l := range langs {
			writeFile(t, filepath.Join(filepath.Dir(v), fmt.Sprintf("E%03d.%s.srt", i+1, l)), srtOf(l))
		}
		videos = append(videos, v)
	}
	store := openStore(t)
	insts := seed(t, store, videos...)
	var reads atomic.Int32
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, func(f *mediawrite.Faults) {
		openRead := f.OpenRead
		f.OpenRead = func(dir *os.Root, name string) (*os.File, fs.FileInfo, error) {
			reads.Add(1)
			return openRead(dir, name)
		}
	})
	run(t, w)

	for pass := range 2 {
		for _, inst := range insts {
			inv, err := w.List(t.Context(), inst, mediawrite.ListOptions{})
			if err != nil {
				t.Fatalf("pass %d: List(%d): %v", pass, inst, err)
			}
			if len(inv.Files) != len(langs) {
				t.Fatalf("pass %d: List(%d) found %d files, want %d", pass, inst, len(inv.Files), len(langs))
			}
			for _, f := range inv.Files {
				if f.Managed || f.Expect != (mediawrite.Expectation{}) {
					t.Errorf("pass %d: %s: managed %v, expectation %v; want an unmanaged file with the zero expectation", pass, f.Name, f.Managed, f.Expect)
				}
			}
		}
	}
	if got := reads.Load(); got != 0 {
		t.Errorf("search passes opened %d unmanaged files, want none", got)
	}

	for pass := range 2 {
		inv, err := w.List(t.Context(), insts[0], mediawrite.ListOptions{UnmanagedExpect: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range inv.Files {
			want := mediawrite.Expectation{SHA256: sha256.Sum256([]byte(srtOf(strings.Split(f.Name, ".")[1])))}
			if f.Expect != want {
				t.Errorf("view pass %d: %s expectation %x, want generation 0 and its content hash", pass, f.Name, f.Expect.SHA256)
			}
		}
	}
	if got := reads.Load(); got != int32(len(langs)) {
		t.Errorf("two view passes opened %d files, want %d: one hash each, then the cache", got, len(langs))
	}

	inv, err := w.List(t.Context(), insts[1], mediawrite.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := inv.Files[0]
	if _, err := w.Retime(t.Context(), f.File, f.Expect, timing.Shift{Ms: 1000}, nil); !errors.Is(err, mediawrite.ErrExpectation) {
		t.Errorf("Retime with a search pass's zero expectation = %v, want ErrExpectation", err)
	}
	if err := w.Delete(t.Context(), f.File, f.Expect); !errors.Is(err, mediawrite.ErrExpectation) {
		t.Errorf("Delete with a search pass's zero expectation = %v, want ErrExpectation", err)
	}
	if _, err := w.Current(t.Context(), f.File, f.Expect); !errors.Is(err, mediawrite.ErrExpectation) {
		t.Errorf("Current with a search pass's zero expectation = %v, want ErrExpectation", err)
	}
	if got := reads.Load(); got != int32(len(langs)) {
		t.Errorf("a zero expectation read %d unmanaged files", got-int32(len(langs)))
	}
}

func TestListSeasonFolder(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	season, movie := filepath.Join(b, "Show", "Season 1"), filepath.Join(b, "Movie")
	files := []string{
		"Show S01E01.mkv", "Show S01E01.en.srt", "Show S01E01.fr.forced.2.srt",
		"Show S01E02.mkv", "Show S01E02.en.srt", "Show S01E02.pt-BR.sdh.ass",
		"Show S01E03.mkv",
	}
	for _, f := range files {
		writeFile(t, filepath.Join(season, f), srtOf(f))
	}
	for _, f := range []string{"Movie.mkv", "Movie.mp4", "Movie.en.srt", "Movie.Extended.mkv", "Movie.Extended.en.srt"} {
		writeFile(t, filepath.Join(movie, f), srtOf(f))
	}
	store := openStore(t)
	insts := seed(t, store,
		filepath.Join(season, "Show S01E01.mkv"), filepath.Join(season, "Show S01E02.mkv"), filepath.Join(season, "Show S01E03.mkv"),
		filepath.Join(movie, "Movie.mkv"), filepath.Join(movie, "Movie.mp4"), filepath.Join(movie, "Movie.Extended.mkv"),
	)
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, nil)
	run(t, w)
	list := func(i int) mediawrite.Inventory {
		inv, err := w.List(t.Context(), insts[i], mediawrite.ListOptions{})
		if err != nil {
			t.Fatalf("List(%d): %v", i, err)
		}
		return inv
	}

	e1 := list(0)
	if got, want := names(e1), []string{"Show S01E01.en.srt", "Show S01E01.fr.forced.2.srt"}; !slices.Equal(got, want) {
		t.Errorf("episode 1 lists %q, want %q", got, want)
	}
	if len(e1.Files) == 2 {
		f := e1.Files[1]
		if f.Tag != mustTag("fr") || f.Flags != (language.Flags{Forced: true}) || f.Ordinal != 2 || f.File.Inst() != insts[0] {
			t.Errorf("%s read as %v %+v ordinal %d of instance %d", f.Name, f.Tag, f.Flags, f.Ordinal, f.File.Inst())
		}
	}
	if got, want := names(list(1)), []string{"Show S01E02.en.srt", "Show S01E02.pt-BR.sdh.ass"}; !slices.Equal(got, want) {
		t.Errorf("episode 2 lists %q, want %q", got, want)
	}
	if e3 := list(2); len(e3.Files) != 0 || len(e3.Unparsed) != 0 {
		t.Errorf("episode 3 lists %q and %q, want nothing", names(e3), e3.Unparsed)
	}
	if got := names(list(5)); !slices.Equal(got, []string{"Movie.Extended.en.srt"}) {
		t.Errorf("Movie.Extended.mkv lists %q, want its own subtitle", got)
	}
	for _, i := range []int{3, 4} {
		inv := list(i)
		if len(inv.Files) != 0 || !slices.Equal(inv.Unparsed, []string{"Movie.en.srt"}) {
			t.Errorf("video %d sharing the stem Movie lists %q, unparsed %q; want only Movie.en.srt unparsed", i, names(inv), inv.Unparsed)
		}
	}
}

func TestReconcileChangedFile(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	dir := filepath.Join(b, "Show")
	videos := []string{filepath.Join(dir, "E01.mkv"), filepath.Join(dir, "E02.mkv"), filepath.Join(dir, "E03.mkv"), filepath.Join(dir, "E04.mkv")}
	for _, v := range videos {
		writeFile(t, v, "video")
	}
	store := openStore(t)
	insts, keys := seedKeys(t, store, videos...)
	edited := landRecord(t, store, insts[0], keys[0], filepath.Join(dir, "E01.en.srt"), srtOf("ours"))
	sameSize := landRecord(t, store, insts[3], keys[3], filepath.Join(dir, "E04.en.srt"), srtOf("ours"))
	writeFile(t, filepath.Join(dir, sameSize.Name), srtOf("OURS"))
	gone := landRecord(t, store, insts[1], keys[1], filepath.Join(dir, "E02.en.srt"), srtOf("ours"))
	touched := landRecord(t, store, insts[2], keys[2], filepath.Join(dir, "E03.en.srt"), srtOf("ours"))
	writeFile(t, filepath.Join(dir, edited.Name), srtOf("the user's"))
	if err := os.Remove(filepath.Join(dir, gone.Name)); err != nil {
		t.Fatal(err)
	}
	later := time.Unix(0, touched.Identity.MtimeNs).Add(time.Hour)
	for _, name := range []string{touched.Name, sameSize.Name} {
		if err := os.Chtimes(filepath.Join(dir, name), later, later); err != nil {
			t.Fatal(err)
		}
	}
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, nil)
	run(t, w)

	inv, err := w.List(t.Context(), insts[0], mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Files) != 1 || inv.Files[0].Managed || inv.Files[0].Expect != (mediawrite.Expectation{SHA256: sha256.Sum256([]byte(srtOf("the user's")))}) {
		t.Errorf("the edited file lists as %+v, want the user's unmanaged file", inv.Files)
	}
	if got := recordNames(t, store, insts[0]); len(got) != 0 {
		t.Errorf("the edited file kept its record: %q", got)
	}
	if _, found, err := store.Baseline(t.Context(), insts[0], edited.Name); err != nil || found {
		t.Errorf("the edited file kept its baseline (%v, %v)", found, err)
	}

	if inv, err := w.List(t.Context(), insts[1], mediawrite.ListOptions{}); err != nil || len(inv.Files) != 0 {
		t.Errorf("the removed file: List = %q, %v; want nothing", names(inv), err)
	}
	if got := recordNames(t, store, insts[1]); len(got) != 0 {
		t.Errorf("the removed file kept its record: %q", got)
	}

	inv, err = w.List(t.Context(), insts[2], mediawrite.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := mediawrite.Expectation{Generation: touched.Generation, SHA256: touched.Identity.SHA256}
	if len(inv.Files) != 1 || !inv.Files[0].Managed || inv.Files[0].Expect != want {
		t.Errorf("a file with only its mtime moved lists as %+v, want managed at %+v", inv.Files, want)
	}

	inv, err = w.List(t.Context(), insts[3], mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Files) != 1 || inv.Files[0].Managed || inv.Files[0].Expect != (mediawrite.Expectation{SHA256: sha256.Sum256([]byte(srtOf("OURS")))}) {
		t.Errorf("a same-size edit lists as %+v, want the user's unmanaged file", inv.Files)
	}
	if got := recordNames(t, store, insts[3]); len(got) != 0 {
		t.Errorf("the same-size edit kept its record: %q", got)
	}
}

func TestListKeepsRecordOnReadError(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	dir := filepath.Join(b, "Show")
	video := filepath.Join(dir, "S01E01.mkv")
	writeFile(t, video, "video")
	store := openStore(t)
	insts, keys := seedKeys(t, store, video)
	rec := landRecord(t, store, insts[0], keys[0], filepath.Join(dir, "S01E01.en.srt"), srtOf("ours"))
	writeFile(t, filepath.Join(dir, "S01E01.fr.srt"), srtOf("the user's"))
	later := time.Unix(0, rec.Identity.MtimeNs).Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, rec.Name), later, later); err != nil {
		t.Fatal(err)
	}
	logs := &logRecords{}
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b), Logger: slog.New(logs)}, func(f *mediawrite.Faults) {
		f.OpenRead = func(_ *os.Root, name string) (*os.File, fs.FileInfo, error) {
			return nil, nil, &fs.PathError{Op: "openat", Path: name, Err: syscall.EIO}
		}
	})
	run(t, w)

	inv, err := w.List(t.Context(), insts[0], mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]mediawrite.Expectation{
		rec.Name:        {Generation: rec.Generation, SHA256: rec.Identity.SHA256},
		"S01E01.fr.srt": {},
	}
	if len(inv.Files) != len(want) {
		t.Fatalf("List = %q, want %d files", names(inv), len(want))
	}
	for _, f := range inv.Files {
		if e, ok := want[f.Name]; !ok || f.Expect != e || f.Managed != (f.Name == rec.Name) {
			t.Errorf("%s lists managed=%v at %+v, want the expectation %+v", f.Name, f.Managed, f.Expect, e)
		}
	}
	if got := recordNames(t, store, insts[0]); !slices.Equal(got, []string{rec.Name}) {
		t.Errorf("records = %q after a failed read, want %q kept", got, rec.Name)
	}
	if _, found, err := store.Baseline(t.Context(), insts[0], rec.Name); err != nil || !found {
		t.Errorf("the baseline after a failed read: found %v (%v), want it kept", found, err)
	}
	rows, err := store.Coverage(t.Context(), keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Coverage != statedb.Exact || !rows[0].Locked {
		t.Errorf("coverage = %+v, want the pair still Exact and locked", rows)
	}
	if infos := logs.messages(slog.LevelInfo); len(infos) != 0 {
		t.Errorf("INFO logs %q, want no record dropped", infos)
	}
	if warns := logs.messages(slog.LevelWarn); len(warns) != 2 {
		t.Errorf("WARN logs %q, want one per file that could not be read", warns)
	}
}

func TestListOmitsFileGoneAtRead(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	dir := filepath.Join(b, "Show")
	video := filepath.Join(dir, "S01E01.mkv")
	writeFile(t, video, "video")
	store := openStore(t)
	insts, keys := seedKeys(t, store, video)
	rec := landRecord(t, store, insts[0], keys[0], filepath.Join(dir, "S01E01.en.srt"), srtOf("ours"))
	writeFile(t, filepath.Join(dir, "S01E01.fr.srt"), srtOf("the user's"))
	later := time.Unix(0, rec.Identity.MtimeNs).Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, rec.Name), later, later); err != nil {
		t.Fatal(err)
	}
	// Each file is removed between the directory read and its hash.
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, func(f *mediawrite.Faults) {
		f.OpenRead = func(_ *os.Root, name string) (*os.File, fs.FileInfo, error) {
			return nil, nil, &fs.PathError{Op: "openat", Path: name, Err: syscall.ENOENT}
		}
	})
	run(t, w)

	inv, err := w.List(t.Context(), insts[0], mediawrite.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(inv); !slices.Equal(got, []string{"S01E01.fr.srt"}) {
		t.Errorf("List = %q, want the managed file gone at its read left out and the unread unmanaged one listed", got)
	}
	if got := recordNames(t, store, insts[0]); len(got) != 0 {
		t.Errorf("records = %q, want the gone file's dropped", got)
	}
	inv, err = w.List(t.Context(), insts[0], mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Files) != 0 {
		t.Errorf("List with expectations = %+v, want no file gone at its read", inv.Files)
	}
}

func TestReconcileSkipsPendingIntent(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	dir := filepath.Join(b, "Show")
	video := filepath.Join(dir, "E01.mkv")
	writeFile(t, video, "video")
	store := openStore(t)
	insts, keys := seedKeys(t, store, video)
	rec := landRecord(t, store, insts[0], keys[0], filepath.Join(dir, "E01.en.srt"), srtOf("ours"))
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, nil)
	run(t, w)
	// Begun after the boot recovery, the intent waits for the next probe's.
	if err := store.BeginDelete(t.Context(), insts[0], rec.Name, rec.Identity.SHA256); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, rec.Name)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "E01.fr.srt"), srtOf("fr"))

	inv, err := w.List(t.Context(), insts[0], mediawrite.ListOptions{})
	if err != nil {
		t.Fatalf("List beside a pending intent: %v", err)
	}
	if got := names(inv); !slices.Equal(got, []string{"E01.fr.srt"}) {
		t.Errorf("List = %q, want the pending name skipped", got)
	}
	if got := recordNames(t, store, insts[0]); !slices.Equal(got, []string{rec.Name}) {
		t.Errorf("records = %q, want the pending name's kept for recovery", got)
	}
}

func TestUnreadableDirChangesNothing(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	missing, unreadable, videoless := filepath.Join(b, "Missing"), filepath.Join(b, "Unreadable"), filepath.Join(b, "Videoless")
	videos := []string{filepath.Join(missing, "E01.mkv"), filepath.Join(unreadable, "E02.mkv"), filepath.Join(videoless, "E03.mkv")}
	for _, v := range videos {
		writeFile(t, v, "video")
	}
	store := openStore(t)
	insts, keys := seedKeys(t, store, videos...)
	for i, v := range videos {
		landRecord(t, store, insts[i], keys[i], filepath.Join(filepath.Dir(v), fmt.Sprintf("E%02d.en.srt", i+1)), srtOf("ours"))
	}
	if err := os.RemoveAll(missing); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(videos[2]); err != nil {
		t.Fatal(err)
	}
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, func(f *mediawrite.Faults) {
		readDir := f.ReadDir
		f.ReadDir = func(dir *os.Root) ([]fs.DirEntry, error) {
			if filepath.Base(dir.Name()) == "Unreadable" {
				return nil, &fs.PathError{Op: "readdirent", Path: dir.Name(), Err: syscall.EIO}
			}
			return readDir(dir)
		}
	})
	run(t, w)

	for i, what := range []string{"a missing directory", "an unreadable directory", "a directory without the video"} {
		before := recordNames(t, store, insts[i])
		if _, err := w.List(t.Context(), insts[i], mediawrite.ListOptions{}); err == nil {
			t.Errorf("%s: List succeeded, want an error", what)
		} else if errors.Is(err, mediawrite.ErrRootUnavailable) {
			t.Errorf("%s: List = %v, want a path error", what, err)
		}
		if got := recordNames(t, store, insts[i]); !slices.Equal(got, before) || len(got) != 1 {
			t.Errorf("%s: records %q after List, want %q", what, got, before)
		}
	}
}

func TestSubListedVttNever(t *testing.T) {
	t.Parallel()
	b := filepath.Join(t.TempDir(), "b")
	dir := filepath.Join(b, "Movie")
	for _, f := range []string{"Movie.mkv", "Movie.en.sub", "Movie.en.idx", "Movie.en.vtt", "Movie.fr.srt", "Movie.en.srt.tmp"} {
		writeFile(t, filepath.Join(dir, f), "x")
	}
	store := openStore(t)
	inst := seed(t, store, filepath.Join(dir, "Movie.mkv"))[0]
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, nil)
	run(t, w)
	inv, err := w.List(t.Context(), inst, mediawrite.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(inv); !slices.Equal(got, []string{"Movie.en.sub", "Movie.fr.srt"}) {
		t.Errorf("List = %q, want the .sub and the .srt", got)
	}
	if len(inv.Unparsed) != 0 {
		t.Errorf("unparsed = %q, want none: .vtt and .idx are not on-disk subtitles", inv.Unparsed)
	}
}

func TestListRefusesSymlinkedName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := filepath.Join(dir, "b")
	writeFile(t, filepath.Join(b, "Movie", "Movie.mkv"), "video")
	writeFile(t, filepath.Join(dir, "outside.srt"), srtOf("outside"))
	if err := os.Symlink(filepath.Join(dir, "outside.srt"), filepath.Join(b, "Movie", "Movie.en.srt")); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	inst := seed(t, store, filepath.Join(b, "Movie", "Movie.mkv"))[0]
	var reads atomic.Int32
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, func(f *mediawrite.Faults) {
		openRead := f.OpenRead
		f.OpenRead = func(dir *os.Root, name string) (*os.File, fs.FileInfo, error) {
			reads.Add(1)
			return openRead(dir, name)
		}
	})
	run(t, w)
	inv, err := w.List(t.Context(), inst, mediawrite.ListOptions{UnmanagedExpect: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Files) != 0 || len(inv.Unparsed) != 0 || reads.Load() != 0 {
		t.Errorf("a symlinked subtitle listed as %q / %q with %d reads, want it never read or listed", names(inv), inv.Unparsed, reads.Load())
	}
}
