package reconcile

import (
	"errors"
	"os"
	"testing"

	"subflux/internal/api"
)

func FuzzClassifyEntry(f *testing.F) {
	f.Add("/video/movie.mkv", "/subs/movie.srt", "movie", "tt123", "eng", false, byte(0))
	f.Add("", "", "", "", "", false, byte(0))
	f.Add("/video.mkv", "", "show", "tt1", "fr", true, byte(1))
	f.Add("/video.mkv", "/sub.srt", "movie", "id", "de", false, byte(2))

	f.Fuzz(func(t *testing.T, videoPath, subPath, mediaType, mediaID, lang string, manual bool, statMode byte) {
		e := &Entry{
			VideoPath: videoPath,
			SubPath:   subPath,
			MediaType: api.MediaType(mediaType),
			MediaID:   mediaID,
			Language:  lang,
			Manual:    manual,
		}

		// statMode simulates: 0=exists, 1=not-exist, 2=error
		stat := func(_ string) (os.FileInfo, error) {
			switch statMode % 3 {
			case 1:
				return nil, os.ErrNotExist
			case 2:
				return nil, errors.New("io error")
			default:
				return nil, nil
			}
		}

		action := ClassifyEntry(e, stat)
		switch action {
		case ActionSkip, ActionDelete, ActionSubMissing, ActionSubPresent:
			// valid
		default:
			t.Errorf("unexpected action: %q", action)
		}
	})
}
