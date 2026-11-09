package mediawrite_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/mediawrite"
)

func TestOpenVideoSymlinkSwap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := filepath.Join(dir, "b")
	video := filepath.Join(b, "Show", "S01E01.mkv")
	writeFile(t, video, "original")
	outside := filepath.Join(dir, "outside", "secret")
	writeFile(t, outside, "secret")
	store := openStore(t)
	inst := seed(t, store, video)[0]
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, nil)
	run(t, w)

	f, err := w.OpenVideo(t.Context(), inst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Remove(video); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, video); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, f); got != "original" {
		t.Errorf("the opened video reads %q after the swap, want the original", got)
	}
	if g, err := w.OpenVideo(t.Context(), inst); err == nil {
		_ = g.Close()
		t.Error("OpenVideo followed a symlink out of the root")
	}
}

func TestSymlinkedDirAndNameRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := filepath.Join(dir, "b")
	outside := filepath.Join(dir, "outside")
	writeFile(t, filepath.Join(outside, "S01E01.mkv"), "outside")
	writeFile(t, filepath.Join(b, "Show", "real.mkv"), "inside")
	if err := os.Symlink(outside, filepath.Join(b, "Linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "S01E01.mkv"), filepath.Join(b, "Show", "S01E02.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.mkv", filepath.Join(b, "Show", "S01E03.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(b, "Show", "S01E04.mkv"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	insts := seed(t, store,
		filepath.Join(b, "Linked", "S01E01.mkv"),
		filepath.Join(b, "Show", "S01E02.mkv"),
		filepath.Join(b, "Show", "S01E03.mkv"),
		filepath.Join(b, "Show", "S01E04.mkv"),
		filepath.Join(b, "..", "outside", "S01E01.mkv"),
	)
	w := newWriter(t, mediawrite.Deps{Store: store, Roots: rootsOf(b)}, nil)
	run(t, w)

	for _, tc := range []struct {
		name string
		i    int
	}{
		{"a video directory linking out of the root", 0},
		{"a video name linking out of the root", 1},
		{"a directory at the video's name", 3},
	} {
		f, err := w.OpenVideo(t.Context(), insts[tc.i])
		if err == nil {
			t.Errorf("%s: OpenVideo opened %s", tc.name, f.Name())
			_ = f.Close()
			continue
		}
		if errors.Is(err, mediawrite.ErrRootUnavailable) || errors.Is(err, mediawrite.ErrOutsideRoot) {
			t.Errorf("%s: OpenVideo = %v, want a path error", tc.name, err)
		}
	}
	if _, err := w.OpenVideo(t.Context(), insts[4]); !errors.Is(err, mediawrite.ErrOutsideRoot) {
		t.Errorf("a video path climbing out with ..: OpenVideo = %v, want ErrOutsideRoot", err)
	}
	f, err := w.OpenVideo(t.Context(), insts[2])
	if err != nil {
		t.Fatalf("a symlink resolving inside the root: %v", err)
	}
	defer f.Close()
	if got := readAll(t, f); got != "inside" {
		t.Errorf("the in-root symlink read %q, want its target", got)
	}
}
