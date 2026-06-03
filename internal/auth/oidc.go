package auth

import (
	"context"
	"time"

	"subflux/internal/api"

	authoidc "github.com/cplieger/auth/oidc"
)

// Sentinel errors for OIDC operations.
var (
	ErrOIDCDiscovery     = authoidc.ErrOIDCDiscovery
	ErrOIDCExchange      = authoidc.ErrOIDCExchange
	ErrOIDCTokenInvalid  = authoidc.ErrOIDCTokenInvalid
	ErrOIDCNonceMismatch = authoidc.ErrOIDCNonceMismatch
	ErrOIDCConfigInvalid = authoidc.ErrOIDCConfigInvalid
)

// OIDCClaims holds the verified claims extracted from an OIDC ID token.
type OIDCClaims = authoidc.Claims

// OIDCProvider wraps the lib's oidc.Provider.
type OIDCProvider struct {
	provider *authoidc.Provider
	config   api.OIDCConfig
}

// GeneratePKCE generates a PKCE code verifier and its S256 challenge.
func GeneratePKCE() (verifier, challenge string, err error) {
	return authoidc.GeneratePKCE()
}

// GenerateOIDCState generates a random state string for OIDC flows.
func GenerateOIDCState() (string, error) {
	return authoidc.GenerateState()
}

// ValidateOIDCConfig checks that the required fields of an OIDCConfig are set.
func ValidateOIDCConfig(cfg api.OIDCConfig) error {
	return authoidc.ValidateConfig(toLibOIDCConfig(cfg))
}

// NewOIDCProvider creates an OIDC provider from config.
func NewOIDCProvider(ctx context.Context, cfg api.OIDCConfig) (*OIDCProvider, error) {
	if err := ValidateOIDCConfig(cfg); err != nil {
		return nil, err
	}
	p, err := authoidc.NewProvider(ctx, toLibOIDCConfig(cfg))
	if err != nil {
		return nil, err
	}
	return &OIDCProvider{provider: p, config: cfg}, nil
}

// AuthorizationURL generates the OIDC authorization URL with PKCE and state.
func (p *OIDCProvider) AuthorizationURL(state, nonce, codeChallenge string) string {
	return p.provider.AuthorizationURL(state, nonce, codeChallenge)
}

// Exchange exchanges an authorization code for tokens and validates the ID token.
func (p *OIDCProvider) Exchange(ctx context.Context, code, codeVerifier, nonce string) (*OIDCClaims, *time.Time, error) {
	return p.provider.Exchange(ctx, code, codeVerifier, nonce)
}

// ResolveOIDCUser maps an OIDC identity to a user by (issuer, sub) only.
func ResolveOIDCUser(claims *OIDCClaims, existingBySub *api.User) (user *api.User, isNew bool) {
	if existingBySub != nil {
		return existingBySub, false
	}

	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}

	return &api.User{
		Username:    username,
		Email:       claims.Email,
		DisplayName: claims.Name,
		Role:        api.RoleUser,
		OIDCSub:     claims.Subject,
		OIDCIssuer:  claims.Issuer,
		Enabled:     true,
	}, true
}

// toLibOIDCConfig converts the subflux OIDCConfig to the lib's oidc.Config.
func toLibOIDCConfig(cfg api.OIDCConfig) authoidc.Config {
	return authoidc.Config{
		IssuerURL:    cfg.IssuerURL,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURI:  cfg.RedirectURI,
		AutoRedirect: cfg.AutoRedirect,
	}
}
