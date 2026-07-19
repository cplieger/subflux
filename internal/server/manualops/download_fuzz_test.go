package manualops

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzValidateDownloadRequest verifies that validation either returns an error
// or normalizes MediaType to a valid value. The invariant is: if err == nil,
// req.MediaType.Valid() must be true.
//
// Bug class: missing normalization or validation bypass could allow invalid
// MediaType values to propagate to the store layer, causing constraint
// violations or incorrect file naming.
func FuzzValidateDownloadRequest(f *testing.F) {
	f.Add("opensubtitles", "12345", 42, "en", "")
	f.Add("subdl", "abc", 1, "fr", "movie")
	f.Add("", "", 0, "", "")
	f.Add("provider", "id", -3, "xx", "invalid")
	f.Add("os", "sub1", 7, "eng", "episode")

	f.Fuzz(func(t *testing.T, provider, subtitleID string, arrID int, language, mediaType string) {
		req := &DownloadRequest{
			Provider:   api.ProviderID(provider),
			SubtitleID: subtitleID,
			ArrID:      arrID,
			Language:   language,
			MediaType:  api.MediaType(mediaType),
		}
		err := ValidateDownloadRequest(req)
		if err == nil && !req.MediaType.Valid() {
			t.Fatalf("ValidateDownloadRequest returned nil but MediaType=%q is invalid", req.MediaType)
		}
		if err == nil && req.ArrID <= 0 {
			t.Fatalf("ValidateDownloadRequest returned nil but ArrID=%d (MediaRef requires a positive arr id)", req.ArrID)
		}
	})
}
