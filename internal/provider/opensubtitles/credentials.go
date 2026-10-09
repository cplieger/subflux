package opensubtitles

import (
	"context"

	"github.com/cplieger/subflux/internal/provider"
)

// Compile-time check on the CredentialChecker opt-in: discovered by type
// assertion in provider.Registry.CheckCredentials, so nothing else would catch
// a rename.
var _ provider.CredentialChecker = (*source)(nil)

// CheckCredentials reports whether OpenSubtitles accepts the configured
// credentials, through the /login round trip a search performs first. That one
// call validates all three at once — the gateway checks the Api-Key header (403)
// and the endpoint the username and password (401) — where a probe of any other
// endpoint would answer for the key alone. A refusal arrives as
// *subflux.AuthError; every other failure means the check did not complete.
func (p *source) CheckCredentials(ctx context.Context) error {
	return p.login(ctx)
}
