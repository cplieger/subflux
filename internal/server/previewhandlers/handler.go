// Package previewhandlers provides HTTP handlers for the video/subtitle
// preview and poster proxy endpoints.
package previewhandlers

import (
	"context"
	"net/http"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/resolve"
	"golang.org/x/sync/semaphore"
)

// SubtitleProcessor is the narrow interface for subtitle operations
// needed by preview handlers.
type SubtitleProcessor interface {
	NormalizeEncoding(data []byte) []byte
	ParseSRT(data []byte) ([]api.SubtitleCue, error)
}

// ArrConfig holds the URL and API key for an arr instance.
type ArrConfig struct {
	URL    string
	APIKey string
}

// StateFunc returns the current arr configuration for poster proxy.
type StateFunc func() *LiveState

// LiveState holds the runtime state needed by preview handlers.
type LiveState struct {
	Cfg          ConfigProvider
	SonarrConfig ArrConfig
	RadarrConfig ArrConfig
	HasSonarr    bool
	HasRadarr    bool
}

// ConfigProvider is the narrow config interface for path validation.
type ConfigProvider interface {
	ValidatePath(ctx context.Context, path string) error
}

// PosterFetcher performs the poster request. The server implementation owns
// the private-arr to public-CDN trust-boundary transition.
type PosterFetcher interface {
	Do(req *http.Request) (*http.Response, error)
}

// Deps holds the dependencies for the preview handler family. Resolve is
// the S7 typed-reference resolver: preview endpoints accept FileRef /
// MediaRef parameters and never a client-supplied path.
type Deps struct {
	SubtitleProc SubtitleProcessor
	FFmpegSem    *semaphore.Weighted
	PosterClient PosterFetcher
	StateFunc    StateFunc
	Resolve      *resolve.Resolver
	ReadBounded  func(ctx context.Context, path string, maxBytes int64) ([]byte, error)
	ServerCtx    func() context.Context
}

// Handler provides HTTP handlers for /api/preview/* endpoints.
type Handler struct {
	deps Deps
}

// NewHandler creates a preview Handler with the given dependencies.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}
