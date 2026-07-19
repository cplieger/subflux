package manualops

import (
	"context"
	"errors"

	"github.com/cplieger/subflux/internal/api"
)

// DownloadRequest holds the parsed fields for a manual download. The video
// is addressed by MediaRef only (media_type + media_id [arr ID] +
// season/episode); the server resolves the video file path from the arr —
// the wire carries no file path (S7). videoPath is the server-resolved
// value, set by the handler after resolution, never decoded from JSON.
type DownloadRequest struct {
	Provider    api.ProviderID `json:"provider"`
	SubtitleID  string         `json:"subtitle_id"`
	Language    string         `json:"language"`
	ReleaseName string         `json:"release_name,omitempty"`
	MediaType   api.MediaType  `json:"media_type,omitempty"`
	videoPath   string
	Score       int  `json:"score,omitempty"`
	Season      int  `json:"season,omitempty"`
	Episode     int  `json:"episode,omitempty"`
	ArrID       int  `json:"media_id,omitempty"`
	TopPick     bool `json:"top_pick,omitempty"`
	HearingImp  bool `json:"hearing_impaired,omitempty"`
	Forced      bool `json:"forced,omitempty"`
}

// SetVideoPath records the server-resolved video path on the request.
// Called by the HTTP handler after MediaRef resolution (and by tests); the
// download pipeline reads it via VideoPath.
func (req *DownloadRequest) SetVideoPath(path string) { req.videoPath = path }

// VideoPath returns the server-resolved video file path.
func (req *DownloadRequest) VideoPath() string { return req.videoPath }

// Validation errors returned by ValidateDownloadRequest.
var (
	ErrMissingRequired  = errors.New("provider, subtitle_id, media_id, and language are required")
	ErrInvalidLangCode  = errors.New("invalid language code")
	ErrInvalidMediaType = errors.New("invalid media_type")
	ErrMissingEpisode   = errors.New("season and episode are required for episode downloads")
)

// ValidateDownloadRequest checks that the download request has all required
// fields and valid values. It normalises MediaType to "movie" when empty.
// Returns nil on success.
func ValidateDownloadRequest(req *DownloadRequest) error {
	if req.Provider == "" || req.SubtitleID == "" || req.ArrID <= 0 || req.Language == "" {
		return ErrMissingRequired
	}
	if !IsValidLangCode(req.Language) {
		return ErrInvalidLangCode
	}
	if req.MediaType != "" && !req.MediaType.Valid() {
		return ErrInvalidMediaType
	}
	if req.MediaType == "" {
		req.MediaType = api.MediaTypeMovie
	}
	if req.MediaType == api.MediaTypeEpisode && req.Episode <= 0 {
		return ErrMissingEpisode
	}
	return nil
}

// DownloadStore is the narrow store interface for manual download operations.
type DownloadStore interface {
	SearchStore
	NextManualNumber(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) int
	UpsertSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID string, sf *api.SubtitleFile) error
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
	SaveDownload(ctx context.Context, rec *api.DownloadRecord) error
}
