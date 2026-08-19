package filehandlers

import (
	"context"
	"errors"
	"testing"
)

// recordingRemover records the paths delegated to the containment delete.
type recordingRemover struct {
	paths []string
	err   error
}

func (r *recordingRemover) RemoveUnderRoot(_ context.Context, path string) error {
	r.paths = append(r.paths, path)
	return r.err
}

func TestRemoveSubtitleUnderRoot(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		path        string
		wantRefused bool
	}{
		{"srt allowed", "/media/movies/movie.fr.srt", false},
		{"ass allowed", "/media/movies/movie.fr.ass", false},
		{"vtt allowed (archive-input capability implies delete)", "/media/movies/movie.en.vtt", false},
		{"uppercase allowed", "/media/movies/MOVIE.FR.SRT", false},
		{"mkv refused", "/media/movies/movie.mkv", true},
		{"no extension refused", "/media/movies/movie", true},
		{"idx refused (not seeded)", "/media/movies/movie.idx", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rem := &recordingRemover{}
			err := removeSubtitleUnderRoot(t.Context(), rem, tc.path)
			if tc.wantRefused {
				if !errors.Is(err, errSubtitleExtensionNotAllowed) {
					t.Fatalf("want errSubtitleExtensionNotAllowed, got %v", err)
				}
				if len(rem.paths) != 0 {
					t.Fatalf("refused path must never reach the containment delete, got %v", rem.paths)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rem.paths) != 1 || rem.paths[0] != tc.path {
				t.Fatalf("containment delete got %v, want [%s]", rem.paths, tc.path)
			}
		})
	}
}

// TestRemoveSubtitleUnderRootPropagatesContainmentError verifies the wrapper
// does not swallow the generic containment delete's error.
func TestRemoveSubtitleUnderRootPropagatesContainmentError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("containment failure")
	rem := &recordingRemover{err: sentinel}
	err := removeSubtitleUnderRoot(t.Context(), rem, "/media/movie.srt")
	if !errors.Is(err, sentinel) {
		t.Fatalf("want containment error propagated, got %v", err)
	}
}
