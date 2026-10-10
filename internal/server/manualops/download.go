package manualops

import (
	"context"
	"errors"

	"github.com/cplieger/subflux/internal/langcode"
	"github.com/cplieger/subflux/internal/subflux"
)

// DownloadRequest holds the parsed fields for a manual download. The video
// is addressed by MediaRef only (media_type + media_id [arr ID] +
// season/episode); the server resolves the video file path from the arr,
// so the wire carries no file path. videoPath is the server-resolved
// value, set by the handler after resolution, never decoded from JSON.
type DownloadRequest struct {
	Provider    subflux.ProviderID `json:"provider"`
	SubtitleID  string             `json:"subtitle_id"`
	Language    string             `json:"language"`
	ReleaseName string             `json:"release_name,omitempty"`
	MediaType   subflux.MediaType  `json:"media_type,omitempty"`
	videoPath   string
	Score       int  `json:"score,omitempty"`
	Season      int  `json:"season,omitempty"`
	Episode     int  `json:"episode,omitempty"`
	ArrID       int  `json:"media_id,omitempty"`
	TopPick     bool `json:"top_pick,omitempty"`
	HearingImp  bool `json:"hearing_impaired,omitempty"`
	Forced      bool `json:"forced,omitempty"`
}

// setVideoPath records the server-resolved video path on the request,
// called by the HTTP handler after MediaRef resolution.
func (req *DownloadRequest) setVideoPath(path string) { req.videoPath = path }

// Validation errors returned by validateDownloadRequest.
var (
	errMissingRequired = errors.New("provider, subtitle_id, media_id, and language are required")
	// errInvalidLangCode gates on subflux's internal language-code space
	// (ISO 639-1 plus "pb") rather than a list of characters to bar, because
	// the code becomes a dot segment of the subtitle filename written next
	// to the media file, and a vocabulary cannot be widened by an input.
	errInvalidLangCode  = errors.New("invalid language code")
	errInvalidMediaType = errors.New("invalid media_type")
	errMissingEpisode   = errors.New("season and episode are required for episode downloads")
)

// validateDownloadRequest checks that the download request has all
// required fields and valid values, normalising MediaType to "movie" when
// empty.
func validateDownloadRequest(req *DownloadRequest) error {
	if req.Provider == "" || req.SubtitleID == "" || req.ArrID <= 0 || req.Language == "" {
		return errMissingRequired
	}
	if !langcode.Valid(req.Language) {
		return errInvalidLangCode
	}
	if req.MediaType != "" && !req.MediaType.Valid() {
		return errInvalidMediaType
	}
	if req.MediaType == "" {
		req.MediaType = subflux.MediaTypeMovie
	}
	if req.MediaType == subflux.MediaTypeEpisode && req.Episode <= 0 {
		return errMissingEpisode
	}
	return nil
}

// DownloadStore is what saving a manual download writes: the next ordinal
// for the numbered file, the coverage row, the timing offset, and the
// download record — plus store, embedded so the search and download legs
// cannot disagree about the read half.
type DownloadStore interface {
	store
	NextManualNumber(ctx context.Context, key subflux.ManualLockKey) int
	UpsertSubtitleFile(ctx context.Context, mediaType subflux.MediaType, mediaID string, sf *subflux.SubtitleFile) error
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
	SaveDownload(ctx context.Context, rec *subflux.DownloadRecord) error
}
