package reconcile

import (
	"errors"
	"os"
	"testing"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

func TestClassifyEntry(t *testing.T) {
	errDisk := errors.New("disk error")
	notExist := os.ErrNotExist

	fakeStat := func(results map[string]error) StatFunc {
		return func(path string) (os.FileInfo, error) {
			if err, ok := results[path]; ok {
				if err != nil {
					return nil, err
				}
				return nil, nil
			}
			return nil, notExist
		}
	}

	tests := []struct {
		stat     map[string]error
		name     string
		expected Action
		entry    Entry
	}{
		{
			name:     "empty video path",
			entry:    Entry{VideoPath: "", SubPath: "/sub.srt"},
			stat:     map[string]error{},
			expected: ActionSkip,
		},
		{
			name:     "video not found",
			entry:    Entry{VideoPath: "/video.mkv", SubPath: "/sub.srt"},
			stat:     map[string]error{},
			expected: ActionDelete,
		},
		{
			name:     "video stat error",
			entry:    Entry{VideoPath: "/video.mkv", SubPath: "/sub.srt"},
			stat:     map[string]error{"/video.mkv": errDisk},
			expected: ActionSkip,
		},
		{
			name:     "video exists empty sub path",
			entry:    Entry{VideoPath: "/video.mkv", SubPath: ""},
			stat:     map[string]error{"/video.mkv": nil},
			expected: ActionSkip,
		},
		{
			name:     "video exists sub not found",
			entry:    Entry{VideoPath: "/video.mkv", SubPath: "/sub.srt"},
			stat:     map[string]error{"/video.mkv": nil},
			expected: ActionSubMissing,
		},
		{
			name:     "video exists sub exists",
			entry:    Entry{VideoPath: "/video.mkv", SubPath: "/sub.srt"},
			stat:     map[string]error{"/video.mkv": nil, "/sub.srt": nil},
			expected: ActionSubPresent,
		},
		{
			name:     "video exists sub stat error",
			entry:    Entry{VideoPath: "/video.mkv", SubPath: "/sub.srt"},
			stat:     map[string]error{"/video.mkv": nil, "/sub.srt": errDisk},
			expected: ActionSubPresent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyEntry(&tc.entry, fakeStat(tc.stat))
			if got != tc.expected {
				t.Errorf("ClassifyEntry() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestClassifyEntry_property(t *testing.T) {
	validActions := map[Action]bool{
		ActionSkip:       true,
		ActionDelete:     true,
		ActionSubMissing: true,
		ActionSubPresent: true,
	}

	rapid.Check(t, func(t *rapid.T) {
		videoPath := rapid.StringMatching(`[a-z/]{0,20}`).Draw(t, "videoPath")
		subPath := rapid.StringMatching(`[a-z/]{0,20}`).Draw(t, "subPath")
		mediaType := rapid.StringMatching(`[a-z]{0,5}`).Draw(t, "mediaType")
		mediaID := rapid.StringMatching(`[0-9]{0,5}`).Draw(t, "mediaID")
		language := rapid.StringMatching(`[a-z]{2}`).Draw(t, "language")
		manual := rapid.Bool().Draw(t, "manual")
		id := rapid.Int64().Draw(t, "id")

		statBehavior := rapid.IntRange(0, 2).Draw(t, "statBehavior")
		statFn := func(path string) (os.FileInfo, error) {
			switch statBehavior {
			case 1:
				return nil, os.ErrNotExist
			case 2:
				return nil, errors.New("io error")
			default:
				return nil, nil
			}
		}

		e := &Entry{
			VideoPath: videoPath,
			SubPath:   subPath,
			MediaType: api.MediaType(mediaType),
			MediaID:   mediaID,
			Language:  language,
			ID:        id,
			Manual:    manual,
		}

		result := ClassifyEntry(e, statFn)
		if !validActions[result] {
			t.Fatalf("invalid action: %q", result)
		}

		result2 := ClassifyEntry(e, statFn)
		if result != result2 {
			t.Fatalf("non-deterministic: %q != %q", result, result2)
		}

		if videoPath == "" && result != ActionSkip {
			t.Fatalf("empty videoPath should yield skip, got %q", result)
		}

		if videoPath != "" && statBehavior == 1 && result != ActionDelete {
			t.Fatalf("ErrNotExist on video should yield delete, got %q", result)
		}

		e2 := &Entry{
			VideoPath: videoPath,
			SubPath:   subPath,
			MediaType: "other",
			MediaID:   "999",
			Language:  "xx",
			ID:        id + 1,
			Manual:    !manual,
		}
		result3 := ClassifyEntry(e2, statFn)
		if result != result3 {
			t.Fatalf("result depends on non-path fields: %q != %q", result, result3)
		}
	})
}
