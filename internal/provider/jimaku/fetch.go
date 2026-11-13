package jimaku

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/cplieger/subflux/internal/provider"
)

// Fetch downloads a file from its entry's download path on jimaku.cc. The
// download needs no key, so none is sent.
func (a *adapter) Fetch(ctx context.Context, ref provider.Ref) ([]byte, error) {
	u, ok := fileURL(ref.Token())
	if !ok {
		return nil, provider.Fail(provider.Permanent, "not a Jimaku download URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := a.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, provider.Fail(provider.Permanent, "Jimaku answered an empty file")
	}
	return b, nil
}

// fileURL is s as a download URL: https on jimaku.cc under /entry/, the
// form every file list answers.
func fileURL(s string) (*url.URL, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host != host || u.User != nil || !strings.HasPrefix(u.Path, "/entry/") {
		return nil, false
	}
	return u, true
}
