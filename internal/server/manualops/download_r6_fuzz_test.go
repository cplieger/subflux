package manualops

import (
	"testing"

	"subflux/internal/api"
)

// FuzzValidateDownloadRequest verifies that validation either returns an error
// or normalizes MediaType to a valid value. The invariant is: if err == nil,
// req.MediaType.Valid() must be true.
//
// Bug class: missing normalization or validation bypass could allow invalid
// MediaType values to propagate to the store layer, causing constraint
// violations or incorrect file naming.
func FuzzValidateDownloadRequest(f *testing.F) {
	f.Add("opensubtitles", "12345", "/media/movie.mkv", "en", "")
	f.Add("subdl", "abc", "/path/file.srt", "fr", "movie")
	f.Add("", "", "", "", "")
	f.Add("provider", "id", "/file", "xx", "invalid")
	f.Add("os", "sub1", "/a/b/c.mkv", "eng", "episode")

	f.Fuzz(func(t *testing.T, provider, subtitleID, filePath, language, mediaType string) {
		req := &DownloadRequest{
			Provider:   api.ProviderID(provider),
			SubtitleID: subtitleID,
			FilePath:   filePath,
			Language:   language,
			MediaType:  api.MediaType(mediaType),
		}
		err := ValidateDownloadRequest(req)
		if err == nil && !req.MediaType.Valid() {
			t.Fatalf("ValidateDownloadRequest returned nil but MediaType=%q is invalid", req.MediaType)
		}
	})
}
