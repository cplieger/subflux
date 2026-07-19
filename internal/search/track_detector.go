package search

import (
	"context"

	"github.com/cplieger/subflux/internal/api"
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
	DetectTracks(ctx context.Context, videoPath string) ([]api.EmbeddedTrack, error)
}

// NoopDetector is an explicit no-detection TrackDetector for callers that
// have no use for embedded track inspection (search.New requires a detector,
// so "none" must be said out loud rather than left nil).
type NoopDetector struct{}

// DetectTracks reports no embedded tracks.
func (NoopDetector) DetectTracks(_ context.Context, _ string) ([]api.EmbeddedTrack, error) {
	return nil, nil
}
