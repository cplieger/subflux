package server

import (
	"os"
	"path/filepath"
	"testing"
)

// deleteSubtitleFiles is the reconcile sweep's disk half: it unlinks the
// subtitle paths whose rows reconcile has just dropped. Its containment must be
// the confined one — resolve and unlink through the same *os.Root handle that
// authorizes the path — because the sibling subtitle-delete path in
// filehandlers already is, and two delete paths with different guarantees is
// the disagreement this pins shut.
//
// The symlink case is the witness. A validate-then-os.Remove pair asks the
// filesystem twice: the first question is "does this resolve to a file inside
// the root", which an entry pointing outside answers no, so the pair refuses
// the entry it was asked to delete and the stale name survives next to the
// media file for the next scan to read as coverage. The confined form unlinks
// the ENTRY, and the file the link pointed at — outside every root — is
// untouched, which is the containment half.
func TestDeleteSubtitleFiles_removesThroughTheConfinedRoot(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "media")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	write := func(path string) string {
		t.Helper()
		if err := os.WriteFile(path, []byte("subtitle"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return path
	}
	inRoot := write(filepath.Join(root, "movie.en.srt"))
	stranger := write(filepath.Join(outside, "stranger.srt"))
	linkTarget := write(filepath.Join(outside, "linked.srt"))

	escapingEntry := filepath.Join(root, "movie.fr.srt")
	if err := os.Symlink(linkTarget, escapingEntry); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	s := &Server{}
	s.live.Store(&liveState{cfg: testConfigInRoot(t, root)})

	s.deleteSubtitleFiles([]string{inRoot, stranger, escapingEntry}, "reconcile")

	if _, err := os.Lstat(inRoot); !os.IsNotExist(err) {
		t.Errorf("a subtitle inside the media root survived the sweep: %v", err)
	}
	if _, err := os.Lstat(stranger); err != nil {
		t.Errorf("a path outside every media root was deleted: %v", err)
	}
	if _, err := os.Lstat(escapingEntry); !os.IsNotExist(err) {
		t.Errorf("the entry survived: a validate-then-remove pair refuses the very path it was asked to delete, leaving the stale name on disk (%v)", err)
	}
	if _, err := os.Lstat(linkTarget); err != nil {
		t.Errorf("the delete followed a symlink out of the media root: %v", err)
	}
}
