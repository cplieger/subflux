package boltstore

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/cplieger/subflux/internal/mediapresence"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// These drive ReconcileState with the production oracle, mediapresence, over a
// row whose video and subtitle are absent from disk.

func presenceChecker(t *testing.T, cfg mediapresence.Config, root string) *mediapresence.Checker {
	t.Helper()
	cfg.Metrics, cfg.Alerts = testsupport.NopPresenceMetrics{}, testsupport.NopAlerts{}
	c, err := mediapresence.New(cfg)
	if err != nil {
		t.Fatalf("Setup: mediapresence.New: %v", err)
	}
	c.Bind([]string{root})
	return c
}

func reconcileWith(t *testing.T, db *DB, c *mediapresence.Checker) (subflux.ReconcileResult, error) {
	t.Helper()
	return db.ReconcileState(t.Context(), c.Gone, c.Unavailable)
}

func seedAbsentRow(t *testing.T, db *DB, root string) string {
	t.Helper()
	video := filepath.Join(root, "show", "ep.mkv")
	if err := db.SaveDownload(t.Context(), dlRec(subflux.MediaTypeEpisode, "tvdb-1-s01e01", "fr",
		subflux.ProviderNameOpenSubtitles, "R", filepath.Join(root, "show", "ep.fr.srt"), video, 80, false)); err != nil {
		t.Fatalf("Setup: SaveDownload: %v", err)
	}
	return video
}

func TestReconcileState_deletes_nothing_under_an_unreadable_root(t *testing.T) {
	for _, tc := range []struct {
		root func(t *testing.T) string
		stat func(video string) func(string) (fs.FileInfo, error)
		name string
	}{
		{name: "root_empty", root: func(t *testing.T) string { return t.TempDir() }},
		{name: "root_missing", root: func(t *testing.T) string { return filepath.Join(t.TempDir(), "gone") }},
		{
			name: "video_EACCES",
			root: populatedDir,
			stat: func(video string) func(string) (fs.FileInfo, error) { return statErrFor(video, syscall.EACCES) },
		},
		{
			name: "video_EIO",
			root: populatedDir,
			stat: func(video string) func(string) (fs.FileInfo, error) { return statErrFor(video, syscall.EIO) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := openTemp(t)
			root := tc.root(t)
			video := seedAbsentRow(t, db, root)
			var cfg mediapresence.Config
			if tc.stat != nil {
				cfg.Stat = tc.stat(video)
			}

			res, err := reconcileWith(t, db, presenceChecker(t, cfg, root))
			if err != nil {
				t.Fatalf("ReconcileState: %v", err)
			}

			if len(res.Deleted.Paths) != 0 || res.ResetCount != 0 {
				t.Errorf("ReconcileState(%s) deleted=%v reset=%d, want nothing deleted or reset",
					tc.name, res.Deleted.Paths, res.ResetCount)
			}
			if rows := readTripleRows(t, db, subflux.MediaTypeEpisode, "tvdb-1-s01e01", "fr"); len(rows) != 1 {
				t.Errorf("rows after ReconcileState(%s) = %d, want 1", tc.name, len(rows))
			}
		})
	}
}

func TestReconcileState_deletes_a_missing_video_under_a_mounted_root(t *testing.T) {
	db, _ := openTemp(t)
	root := populatedDir(t)
	seedAbsentRow(t, db, root)

	res, err := reconcileWith(t, db, presenceChecker(t, mediapresence.Config{}, root))
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}

	if rows := readTripleRows(t, db, subflux.MediaTypeEpisode, "tvdb-1-s01e01", "fr"); len(rows) != 0 {
		t.Errorf("rows after reconciling a deleted video = %d, want 0 (deleted=%v)", len(rows), res.Deleted.Paths)
	}
}

func TestReconcileState_deletes_nothing_beside_a_faulted_video(t *testing.T) {
	db, _ := openTemp(t)
	root := populatedDir(t)
	faulted := seedAbsentRow(t, db, root)
	sibling := filepath.Join(root, "show", "ep2.mkv")
	if err := db.SaveDownload(t.Context(), dlRec(subflux.MediaTypeEpisode, "tvdb-1-s01e02", "fr",
		subflux.ProviderNameOpenSubtitles, "R", filepath.Join(root, "show", "ep2.fr.srt"), sibling, 80, false)); err != nil {
		t.Fatalf("Setup: SaveDownload: %v", err)
	}
	cfg := mediapresence.Config{Stat: statErrFor(faulted, syscall.EIO)}

	res, err := reconcileWith(t, db, presenceChecker(t, cfg, root))
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}

	if len(res.Deleted.Paths) != 0 || res.ResetCount != 0 {
		t.Errorf("ReconcileState(EIO on %s) deleted=%v reset=%d, want nothing under the faulted root deleted",
			faulted, res.Deleted.Paths, res.ResetCount)
	}
	if rows := readTripleRows(t, db, subflux.MediaTypeEpisode, "tvdb-1-s01e02", "fr"); len(rows) != 1 {
		t.Errorf("sibling rows after ReconcileState = %d, want 1", len(rows))
	}
}

func TestReconcileState_a_later_fault_withdraws_what_earlier_rows_queued(t *testing.T) {
	for _, tc := range []struct {
		name         string
		videoPresent bool
	}{
		{name: "video_gone"},
		{name: "subtitle_gone", videoPresent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := openTemp(t)
			root := populatedDir(t)
			first := seedAbsentRow(t, db, root)
			if tc.videoPresent {
				if err := os.MkdirAll(filepath.Dir(first), 0o750); err != nil {
					t.Fatalf("Setup: %v", err)
				}
				mkfile(t, first)
			}
			faulted := filepath.Join(root, "show", "ep2.mkv")
			if err := db.SaveDownload(t.Context(), dlRec(subflux.MediaTypeEpisode, "tvdb-1-s01e02", "fr",
				subflux.ProviderNameOpenSubtitles, "R", filepath.Join(root, "show", "ep2.fr.srt"), faulted, 80, false)); err != nil {
				t.Fatalf("Setup: SaveDownload: %v", err)
			}

			res, err := reconcileWith(t, db, presenceChecker(t, mediapresence.Config{Stat: statErrFor(faulted, syscall.EIO)}, root))
			if err != nil {
				t.Fatalf("ReconcileState: %v", err)
			}

			if len(res.Deleted.Paths) != 0 || res.ResetCount != 0 {
				t.Errorf("ReconcileState(%s queued before EIO on %s) deleted=%v reset=%d, want nothing deleted or reset",
					tc.name, faulted, res.Deleted.Paths, res.ResetCount)
			}
			if rows := readTripleRows(t, db, subflux.MediaTypeEpisode, "tvdb-1-s01e01", "fr"); len(rows) != 1 || rows[0].Path == "" {
				t.Errorf("first row after ReconcileState(%s) = %+v, want it kept with its subtitle path", tc.name, rows)
			}
		})
	}
}

func populatedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "other.mkv"))
	return dir
}

func statErrFor(path string, err error) func(string) (fs.FileInfo, error) {
	return func(p string) (fs.FileInfo, error) {
		if p == path {
			return nil, &fs.PathError{Op: "stat", Path: p, Err: err}
		}
		return os.Stat(p)
	}
}
