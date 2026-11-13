// Package subsro is the Subs.ro provider: the Romanian site's subtitles, in
// Romanian and a few other languages, found by IMDb id through its API at
// api.subs.ro and downloaded there as zip or rar archives. It needs an API
// key, whose quota is daily.
package subsro

import (
	"context"
	"net/http"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
)

// ID is Subs.ro's provider id.
const ID provider.ProviderID = "subsro"

const (
	host   = "api.subs.ro"
	apiURL = "https://" + host + "/v1.0"

	keyAPIKey = "api_key"
	keyHeader = "X-Subs-Api-Key"

	// quotaPause is how long a 429 without Retry-After pauses Subs.ro: its
	// quota is daily and it publishes no reset time.
	quotaPause = time.Hour

	maxListBody = 2 << 20
)

// NewEntry is Subs.ro's row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:      ID,
		Summary: "Romanian and other subtitles from Subs.ro by IMDb id; needs an API key.",
		Hosts:   []string{host},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyAPIKey, Label: "API key", Help: "From your subs.ro account", Kind: formschema.Secret, Required: true},
		}},
		Languages: languages,
		Quirk:     quirk,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, key: v.Secret(keyAPIKey)}, nil
		},
	}
}

func quirk(resp *http.Response, _ []byte) error {
	if resp.StatusCode != http.StatusTooManyRequests || httpclient.ParseRetryAfterResponse(resp) > 0 {
		return nil
	}
	return &provider.Failure{Kind: provider.RateLimited, RetryAfter: quotaPause, Msg: "Subs.ro's daily quota is spent"}
}

type adapter struct {
	c   provider.Client
	key string
}

// Test asks the key's quota, which refuses a wrong key and spends none of
// the daily quota.
func (a *adapter) Test(ctx context.Context) error {
	resp, err := a.get(ctx, apiURL+"/quota")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = provider.ReadAll(resp, maxListBody)
	return err
}

// get sends a GET to u with the key in its header, never in the URL.
func (a *adapter) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set(keyHeader, a.key)
	req.Header.Set("Accept", "application/json")
	return a.c.Do(req)
}
