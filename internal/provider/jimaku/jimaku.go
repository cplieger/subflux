// Package jimaku is the Jimaku provider: the Japanese subtitles its API at
// jimaku.cc lists in entries keyed by TMDB id, downloaded from jimaku.cc. It
// needs an API key.
package jimaku

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
)

// ID is Jimaku's provider id.
const ID provider.ProviderID = "jimaku"

const (
	host   = "jimaku.cc"
	apiURL = "https://" + host + "/api"

	keyAPIKey = "api_key"

	// testID is the TMDB id Test searches: a series Jimaku lists.
	testID = "tv:6484"

	// maxResetAfter bounds the x-ratelimit-reset-after a 429 is read with,
	// so a malformed header cannot overflow a time.Duration.
	maxResetAfter = 24 * time.Hour
)

// NewEntry is Jimaku's row of the provider table.
func NewEntry() provider.Entry {
	return provider.Entry{
		ID:      ID,
		Summary: "Japanese subtitles from Jimaku by TMDB id, mostly Japanese drama and film; needs an API key.",
		Hosts:   []string{host},
		Schema: formschema.Schema{Fields: []formschema.Field{
			{Key: keyAPIKey, Label: "API key", Help: "From your jimaku.cc account page", Kind: formschema.Secret, Required: true},
		}},
		Languages: languages,
		Quirk:     quirk,
		New: func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
			return &adapter{c: c, key: v.Secret(keyAPIKey)}, nil
		},
	}
}

// quirk reads a 429 without Retry-After by Jimaku's
// x-ratelimit-reset-after, seconds with a fraction, rounded up
// (https://jimaku.cc/api/docs, "Rate Limits").
func quirk(resp *http.Response, _ []byte) error {
	if resp.StatusCode != http.StatusTooManyRequests || httpclient.ParseRetryAfterResponse(resp) > 0 {
		return nil
	}
	s, err := strconv.ParseFloat(resp.Header.Get("x-ratelimit-reset-after"), 64)
	if err != nil || !(s > 0) {
		return nil
	}
	wait := time.Duration(math.Ceil(min(s, maxResetAfter.Seconds()))) * time.Second
	return &provider.Failure{Kind: provider.RateLimited, RetryAfter: wait, Msg: "Jimaku's rate limit"}
}

type adapter struct {
	c   provider.Client
	key string
}

// Test searches one fixed TMDB id, which proves the key.
func (a *adapter) Test(ctx context.Context) error {
	_, err := a.entries(ctx, testID, false)
	return err
}
