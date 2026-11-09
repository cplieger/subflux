package mediawrite_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/cplieger/subflux/internal/mediawrite"
)

// mountModes are the publish chains a mount may offer: the seams refuse
// RENAME_NOREPLACE, hard links, or both, with the errnos a mount answers.
var mountModes = []struct {
	refuse func(*mediawrite.Faults)
	name   string
	logged string
}{
	{name: "renameat2", logged: "renameat2", refuse: func(*mediawrite.Faults) {}},
	{name: "no RENAME_NOREPLACE", logged: "hard link", refuse: func(f *mediawrite.Faults) {
		f.RenameNoReplace = func(*os.File, string, string) error { return &os.LinkError{Op: "renameat2", Err: syscall.EINVAL} }
	}},
	{name: "neither, links refused EPERM", logged: "checked rename", refuse: func(f *mediawrite.Faults) {
		f.RenameNoReplace = func(*os.File, string, string) error { return &os.LinkError{Op: "renameat2", Err: syscall.ENOSYS} }
		f.Link = func(*os.Root, string, string) error { return &os.LinkError{Op: "link", Err: syscall.EPERM} }
	}},
	{name: "neither, links refused EXDEV", logged: "checked rename", refuse: func(f *mediawrite.Faults) {
		f.RenameNoReplace = func(*os.File, string, string) error { return &os.LinkError{Op: "renameat2", Err: syscall.EOPNOTSUPP} }
		f.Link = func(*os.Root, string, string) error { return &os.LinkError{Op: "link", Err: syscall.EXDEV} }
	}},
}

func TestPublishNeverOverwrites(t *testing.T) {
	t.Parallel()
	for _, m := range mountModes {
		t.Run(m.name+" with a file at the name", func(t *testing.T) {
			t.Parallel()
			logs := &logRecords{}
			l := newLibrary(t, enOnly, mediawrite.Deps{Logger: slog.New(logs)}, m.refuse)
			writeFile(t, l.path("S01E01.en.srt"), "the user's own")
			rec, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello"))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Name != "S01E01.en.1.srt" {
				t.Errorf("delivered to %q, want the numbered sibling", rec.Name)
			}
			if got := readFile(t, l.path("S01E01.en.srt")); got != "the user's own" {
				t.Errorf("the unmanaged file now reads %q", got)
			}
			if got := readFile(t, l.path(rec.Name)); got != srtOf("hello") {
				t.Errorf("the delivery reads %q", got)
			}
			logged := slices.ContainsFunc(append(logs.messages(slog.LevelInfo), logs.messages(slog.LevelWarn)...), func(s string) bool { return strings.Contains(s, m.logged) })
			if !logged {
				t.Errorf("no log names the %s publish mode", m.logged)
			}
		})
		t.Run(m.name+" with a writer racing the publish", func(t *testing.T) {
			t.Parallel()
			var l *library
			l = newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
				m.refuse(f)
				f.Step = func(step string) {
					if step == "stage" {
						writeFile(t, l.path("S01E01.en.srt"), "written meanwhile")
					}
				}
			})
			if _, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello")); !errors.Is(err, mediawrite.ErrNameContended) {
				t.Errorf("Deliver racing an outside writer = %v, want ErrNameContended", err)
			}
			if got := readFile(t, l.path("S01E01.en.srt")); got != "written meanwhile" {
				t.Errorf("the outside file now reads %q; the publish replaced it", got)
			}
			if got := dirNames(t, l.dir); !slices.Equal(got, []string{"S01E01.en.srt", "S01E01.mkv"}) {
				t.Errorf("directory holds %v, want the stage removed", got)
			}
			if n := pendingCount(t, l.store); n != 0 || len(recordNames(t, l.store, l.inst)) != 0 {
				t.Errorf("%d intents and records %v, want the delivery settled not landed", n, recordNames(t, l.store, l.inst))
			}
		})
	}
	t.Run("case-insensitive share", func(t *testing.T) {
		t.Parallel()
		var l *library
		l = newLibrary(t, enOnly, mediawrite.Deps{}, func(f *mediawrite.Faults) {
			lstat := f.Lstat
			f.Lstat = func(dir *os.Root, name string) (fs.FileInfo, error) {
				for _, e := range dirNames(t, l.dir) {
					if strings.EqualFold(e, name) {
						return lstat(dir, e)
					}
				}
				return lstat(dir, name)
			}
		})
		writeFile(t, l.path("S01E01.EN.srt"), "the user's own")
		rec, err := l.w.Deliver(t.Context(), delivery(t, l.inst, "hello"))
		if err != nil {
			t.Fatal(err)
		}
		if rec.Name != "S01E01.en.1.srt" {
			t.Errorf("delivered to %q beside S01E01.EN.srt on a case-insensitive share, want the numbered sibling", rec.Name)
		}
		if got := readFile(t, l.path("S01E01.EN.srt")); got != "the user's own" {
			t.Errorf("the unmanaged file now reads %q", got)
		}
	})
}
