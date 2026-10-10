package subsource

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
)

// Compile-time check on the CredentialChecker opt-in: discovered by type
// assertion in provider.Registry.CheckCredentials, so nothing else would catch
// a rename.
var _ provider.CredentialChecker = (*source)(nil)

// unmatchedIMDB is a syntactically valid IMDb id no title carries, so the title
// lookup below is an indexed miss rather than a search.
//
// SubSource publishes eight endpoints and no account or profile one, so there is
// nothing cheaper to ask: its only key-free route is /health, which answers 200
// for a wrong key and would report a green test for the mistake this check
// exists to catch.
const unmatchedIMDB = "tt0000000"

// CheckCredentials reports whether SubSource accepts the configured API key.
//
// The verdict is the STATUS, and the body is never read. Measured 2026-09-19:
// the key gate runs ahead of routing (an unregistered path answers 401 with
// `{"error":"Invalid API key"}` just as a real one does), so any answer that is
// not 401 or 403 means the key was accepted whatever the router then made of the
// request. A 5xx is the one arm that cannot say either way, so it reports as an
// incomplete check rather than as a verdict.
func (p *source) CheckCredentials(ctx context.Context) error {
	params := url.Values{
		paramAPIKey:     {p.apiKey},
		paramSearchType: {string(matchedByIMDB)},
		"imdb":          {unmatchedIMDB},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/movies/search?"+params.Encode(), http.NoBody)
	if err != nil {
		return httpx.RedactSecret(err, p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return httpx.RedactTransportError(err, "subsource credential check", httpx.Secret(p.apiKey))
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return &subflux.AuthError{
			Msg: fmt.Sprintf("SubSource refused the API key (HTTP %d)", resp.StatusCode),
		}
	case resp.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("SubSource answered HTTP %d", resp.StatusCode)
	}
	return nil
}
