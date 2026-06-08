package search

import (
	"context"

	"github.com/cplieger/subflux/internal/api"
)

// TrackDetector detects embedded subtitle tracks in video containers.
// Implemented by the embedded provider's ffprobe-based parser.
type TrackDetector interface {
	// DetectTracks returns embedded subtitle tracks in the given video file.
	// Returns nil if detection fails (ffprobe error, corrupt file, timeout);
	// errors are logged internally. Callers treat nil/empty as "no tracks".
	DetectTracks(ctx context.Context, videoPath string) []api.EmbeddedTrack
}
