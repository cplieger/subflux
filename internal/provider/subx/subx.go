// Package subx is the SubX provider: Spanish subtitles from Subdivx's
// catalogue, which SubX's API at subx-api.duckdns.org re-serves, searched by
// IMDb id or title and downloaded there as the uploaded file or archive. It
// needs an API key. Subdivx publishes no API of its own, so SubX is a named
// exception the Client holds to robots.txt, few requests and an hour's
// back-off.
package subx

import (
	"context"
	"net/http"
	"net/url"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
)

// ID is SubX's provider id.
const ID provider.ProviderID = "subx"

const (
	host   = "subx-api.duckdns.org"
	apiURL = "https://" + host + "/api/subtitles"

	keyAPIKey = "api_key"

	maxListBody = 2 << 20
)

// NewEntry is SubX's row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:      ID,
		Summary: "Spanish subtitles from Subdivx's catalogue through SubX by IMDb id or title; needs an API key.",
		Hosts:   []string{host},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyAPIKey, Label: "API key", Help: "From your SubX profile's API keys", Kind: formschema.Secret, Required: true},
		}},
		Languages: languages,
		Exception: true,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, key: v.Secret(keyAPIKey)}, nil
		},
	}
}

type adapter struct {
	c   provider.Client
	key string
}

// testIMDb is the film the connection test searches for (Inception).
const testIMDb = 1375666

// Test searches one fixed IMDb id for one item, which a refused key
// answers with 401.
func (a *adapter) Test(ctx context.Context) error {
	_, err := a.search(ctx, url.Values{"imdb_id": {imdbText(testIMDb)}, "video_type": {"movie"}, "limit": {"1"}})
	return err
}

// get sends a GET to u with the key as a Bearer token, never in the URL.
func (a *adapter) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.key)
	return a.c.Do(req)
}
