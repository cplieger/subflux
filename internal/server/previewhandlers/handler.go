// Package previewhandlers provides HTTP handlers for the video/subtitle
// preview and poster proxy endpoints.
package previewhandlers

import (
	"context"
	"net/http"

	"github.com/cplieger/subflux/internal/api"
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

// Deps holds the dependencies for the preview handler family.
type Deps struct {
	SubtitleProc SubtitleProcessor
	FFmpegSem    *semaphore.Weighted
	PosterClient *http.Client
	StateFunc    StateFunc
	ValidatePath func(w http.ResponseWriter, r *http.Request, path, label string) bool
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
