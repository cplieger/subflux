package animetosho

import (
	"context"

	"github.com/cplieger/subflux/internal/provider"
)

// Compile-time check on the CredentialChecker opt-in: discovered by type
// assertion in provider.Registry.CheckCredentials, so nothing else would catch
// a rename.
var _ provider.CredentialChecker = (*Provider)(nil)

// CheckCredentials reports whether AnimeTosho's one credential — the optional
// AniDB client key — is accepted. AnimeTosho's own feed needs no credential at
// all, so there is nothing else to validate.
func (p *Provider) CheckCredentials(ctx context.Context) error {
	return p.anidbMapper.CheckClientKey(ctx)
}
