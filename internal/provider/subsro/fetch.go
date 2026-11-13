package subsro

import (
	"context"
	"strconv"

	"github.com/cplieger/subflux/internal/provider"
)

// Fetch downloads a subtitle's archive from api.subs.ro by its id; the
// answer's downloadLink is never followed, so no answer names the host the
// key goes to.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) ([]byte, error) {
	tok := ref.Token()
	if n, err := strconv.ParseUint(tok, 10, 63); err != nil || n == 0 || strconv.FormatUint(n, 10) != tok {
		return nil, provider.Fail(provider.Permanent, "not a Subs.ro subtitle id")
	}
	resp, err := a.get(ctx, apiURL+"/subtitle/"+tok+"/download")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, provider.Fail(provider.Permanent, "Subs.ro answered an empty archive")
	}
	return b, nil
}
