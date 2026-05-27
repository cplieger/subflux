package manualops

import (
	"context"
	"errors"

	"subflux/internal/api"
)

// DownloadRequest holds the parsed fields for a manual download.
type DownloadRequest struct {
	Provider    api.ProviderID `json:"provider"`
	SubtitleID  string         `json:"subtitle_id"`
	FilePath    string         `json:"file_path"`
	Language    string         `json:"language"`
	ReleaseName string         `json:"release_name,omitempty"`
	MediaType   api.MediaType  `json:"media_type,omitempty"`
	Score       int            `json:"score,omitempty"`
	Season      int            `json:"season,omitempty"`
	Episode     int            `json:"episode,omitempty"`
	ArrID       int            `json:"media_id,omitempty"`
	TopPick     bool           `json:"top_pick,omitempty"`
	HearingImp  bool           `json:"hearing_impaired,omitempty"`
	Forced      bool           `json:"forced,omitempty"`
}

// Validation errors returned by ValidateDownloadRequest.
var (
	ErrMissingRequired  = errors.New("provider, subtitle_id, file_path, and language are required")
	ErrInvalidLangCode  = errors.New("invalid language code")
	ErrInvalidMediaType = errors.New("invalid media_type")
)

// ValidateDownloadRequest checks that the download request has all required
// fields and valid values. It normalises MediaType to "movie" when empty.
// Returns nil on success.
func ValidateDownloadRequest(req *DownloadRequest) error {
	if req.Provider == "" || req.SubtitleID == "" || req.FilePath == "" || req.Language == "" {
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
	return nil
}



// DownloadStore is the narrow store interface for manual download operations.
type DownloadStore interface {
	SearchStore
	NextManualNumber(ctx context.Context, mediaType api.MediaType, mediaID, language string) int
	UpsertSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID string, sf *api.SubtitleFile) error
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
	SaveDownload(ctx context.Context, rec *api.DownloadRecord) error
}
