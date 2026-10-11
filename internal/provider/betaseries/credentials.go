package betaseries

import (
	"context"

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/subflux/internal/provider"
)

// Compile-time check on the CredentialChecker opt-in: discovered by type
// assertion in provider.Registry.CheckCredentials, so nothing else would catch
// a rename.
var _ provider.CredentialChecker = (*source)(nil)

// statusURL is the cheapest key-gated route BetaSeries publishes: it takes no
// parameters, so nothing is looked up. Measured 2026-09-19 — an unregistered
// single-segment path answers 404 while this one answers the key gate, and a
// rejected key is HTTP 400 with error code 1001.
const statusURL = baseURL + "status"

// CheckCredentials reports whether BetaSeries accepts the configured token.
// A refusal arrives as *subflux.AuthError from classifyBadRequest (code 1001);
// every other failure means the check did not complete.
func (p *source) CheckCredentials(ctx context.Context) error {
	body, err := p.doGet(ctx, statusURL)
	if err != nil {
		return err
	}
	httpx.DrainClose(body)
	return nil
}
