package reconcile

import (
	"errors"
	"os"
	"testing"

	"subflux/internal/api"
)

func FuzzClassifyEntry(f *testing.F) {
	f.Add("/video/path.mkv", "/sub/path.srt", true, true)
	f.Add("", "", false, false)
	f.Add("/video.mkv", "", true, false)
	f.Add("/video.mkv", "/sub.srt", true, false)
	f.Fuzz(func(t *testing.T, videoPath, subPath string, videoExists, subExists bool) {
		stat := func(path string) (os.FileInfo, error) {
			if path == videoPath && !videoExists {
				return nil, os.ErrNotExist
			}
			if path == subPath && !subExists {
				return nil, os.ErrNotExist
			}
			if path != videoPath && path != subPath {
				return nil, errors.New("unknown path")
			}
			return nil, nil
		}
		e := &Entry{
			VideoPath: videoPath,
			SubPath:   subPath,
			MediaType: api.MediaTypeMovie,
			MediaID:   "tt1234",
			Language:  "en",
			ID:        1,
		}
		// Must not panic.
		action := ClassifyEntry(e, stat)
		// Validate action is a known value.
		switch action {
		case ActionSkip, ActionDelete, ActionSubMissing, ActionSubPresent:
		default:
			t.Fatalf("unexpected action: %q", action)
		}
	})
}
