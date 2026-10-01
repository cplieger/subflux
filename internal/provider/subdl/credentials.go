package subdl

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
var _ provider.CredentialChecker = (*Provider)(nil)

// CheckCredentials reports whether SubDL accepts the configured API key.
//
// /me is SubDL's documented account-status endpoint (plan, per-key usage,
// quotas), so it is the one call that proves the key without touching the
// subtitle index. Measured 2026-09-19: a rejected key answers HTTP 403 with
// `{"status":false,"error":"Not Authorized"}`. The verdict is the status and the
// body is never read, because every field in it is upstream-controlled and none
// of it changes the answer.
func (p *Provider) CheckCredentials(ctx context.Context) error {
	params := url.Values{"api_key": {p.apiKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		apiURL+"/me?"+params.Encode(), http.NoBody)
	if err != nil {
		return httpx.RedactSecret(err, p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return httpx.RedactTransportError(err, "subdl credential check", httpx.Secret(p.apiKey))
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return &subflux.AuthError{
			Msg: fmt.Sprintf("SubDL refused the API key (HTTP %d)", resp.StatusCode),
		}
	}
	return fmt.Errorf("SubDL answered HTTP %d", resp.StatusCode)
}
