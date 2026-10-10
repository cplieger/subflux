// Package previewhandlers provides HTTP handlers for the video/subtitle
// preview and poster proxy endpoints.
package previewhandlers

import (
	"context"
	"net/http"

	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/semaphore"
)

// subtitleProcessor is the subtitle operations preview handlers need.
type subtitleProcessor interface {
	NormalizeEncoding(data []byte) []byte
	ParseSRT(data []byte) ([]subflux.SubtitleCue, error)
}

// ArrConfig holds the URL and API key for an arr instance.
type ArrConfig struct {
	URL    string
	APIKey string
}

// stateFunc returns the current arr configuration for poster proxy.
type stateFunc func() *LiveState

// LiveState holds the runtime state needed by preview handlers.
type LiveState struct {
	SonarrConfig ArrConfig
	RadarrConfig ArrConfig
	HasSonarr    bool
	HasRadarr    bool
}

// posterFetcher performs the poster request. The server implementation owns
// the private-arr to public-CDN trust-boundary transition.
type posterFetcher interface {
	Do(req *http.Request) (*http.Response, error)
}

// Deps holds the dependencies for the preview handler family. Resolve is
// the S7 typed-reference resolver: preview endpoints accept FileRef /
// MediaRef parameters and never a client-supplied path.
type Deps struct {
	SubtitleProc subtitleProcessor
	FFmpegSem    *semaphore.Weighted
	PosterClient posterFetcher
	StateFunc    stateFunc
	Resolve      *resolve.Resolver
	ReadBounded  func(ctx context.Context, path string, maxBytes int64) ([]byte, error)
}

// Handler provides HTTP handlers for /api/preview/* endpoints.
type Handler struct {
	deps Deps
}

// NewHandler creates a preview Handler with the given dependencies.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}
