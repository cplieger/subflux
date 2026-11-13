package tsukihime

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/archive"
)

// Fetch downloads an attachment from TsukiHime's storage, whose path the
// attachment id alone gives, and returns it decompressed. The storage
// answers directly, so a redirect is refused unfollowed.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) ([]byte, error) {
	id, ok := attachmentID(ref.Token())
	if !ok {
		return nil, provider.Fail(provider.Permanent, "not a TsukiHime attachment id")
	}
	u := fmt.Sprintf("https://%s/attach/%08X/%d.xz", storageHost, id, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := a.c.DoNoRedirect(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return nil, err
	}
	out, err := archive.Decompress(b)
	if err != nil {
		return nil, provider.Fail(provider.Permanent, "TsukiHime attachment: "+err.Error())
	}
	if len(out) == 0 {
		return nil, provider.Fail(provider.Permanent, "TsukiHime answered an empty attachment")
	}
	return out, nil
}

// attachmentID is a Ref token's attachment id: the canonical decimal of a
// number from 1 to 2^32-1, which the storage path's 8 hex digits hold.
func attachmentID(tok string) (uint64, bool) {
	n, err := strconv.ParseUint(tok, 10, 32)
	return n, err == nil && n > 0 && strconv.FormatUint(n, 10) == tok
}
