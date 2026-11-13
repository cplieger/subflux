package subx

import (
	"context"
	"regexp"

	"github.com/cplieger/subflux/internal/provider"
)

// subtitleID is a SubX subtitle id, a lower-case UUID as every search
// answers it.
var subtitleID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Fetch downloads a subtitle by its id from SubX's download path, which
// answers the uploaded file or archive as is.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) ([]byte, error) {
	tok := ref.Token()
	if !subtitleID.MatchString(tok) {
		return nil, provider.Fail(provider.Permanent, "not a SubX subtitle id")
	}
	resp, err := a.get(ctx, apiURL+"/"+tok+"/download")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, provider.Fail(provider.Permanent, "SubX answered an empty file")
	}
	return b, nil
}
