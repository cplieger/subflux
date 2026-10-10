package search

import (
	"context"

	"github.com/cplieger/subflux/internal/subflux"
)

// TrackDetector detects embedded subtitle tracks in video containers.
// Implemented by internal/embedded's ffprobe-backed Detector.
type TrackDetector interface {
	// DetectTracks returns ALL normalized embedded subtitle tracks in the
	// given video file (including bitmap formats), or an error when the
	// probe fails (ffprobe error, corrupt file, timeout). The engine owns
	// the error policy: log/metric observability, fail-open search, and
	// skipping the coverage replacement so a failed probe never deletes
	// persisted rows. (nil, nil) means "no tracks", distinct from an error.
	DetectTracks(ctx context.Context, videoPath string) ([]subflux.EmbeddedTrack, error)
}
