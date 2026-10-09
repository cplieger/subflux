package testsupport

import (
	"context"

	"github.com/cplieger/subflux/internal/subflux"
)

// NoDetector is a track detector that finds no embedded tracks, for engines
// built by tests that do not exercise embedded detection.
type NoDetector struct{}

// DetectTracks returns no tracks and no error.
func (NoDetector) DetectTracks(context.Context, string) ([]subflux.EmbeddedTrack, error) {
	return nil, nil
}
