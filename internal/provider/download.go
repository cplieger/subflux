package provider

import (
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider/archive"
)

// ExtractAndValidate attempts to extract a subtitle from an archive (zip/rar),
// falling back to the raw data if extraction yields nothing. The result is
// validated for binary content. Returns the usable subtitle bytes or an error.
func ExtractAndValidate(data []byte, season, episode int) ([]byte, error) {
	if extracted := archive.Extract(data, season, episode); extracted != nil {
		if err := api.ValidateSubtitleData(extracted); err != nil {
			return nil, err
		}
		return extracted, nil
	}
	if err := api.ValidateSubtitleData(data); err != nil {
		return nil, err
	}
	return data, nil
}
