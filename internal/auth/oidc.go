package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	authoidc "github.com/cplieger/auth/oidc"
	"github.com/cplieger/subflux/internal/api"
	"golang.org/x/oauth2"
)

// Sentinel errors for OIDC operations.
var (
	ErrOIDCDiscovery     = errors.New("oidc: provider discovery failed")
	ErrOIDCExchange      = errors.New("oidc: code exchange failed")
	ErrOIDCTokenInvalid  = errors.New("oidc: ID token verification failed")
	ErrOIDCNonceMismatch = errors.New("oidc: nonce mismatch")
	ErrOIDCConfigInvalid = errors.New("oidc: invalid configuration")
)

// OIDCClaims holds the verified claims extracted from an OIDC ID token.
type OIDCClaims struct {
	Subject           string `json:"sub"`
	Issuer            string `json:"iss"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	EmailVerified     bool   `json:"email_verified"`
}

// OIDCProvider wraps the coreos/go-oidc provider with PKCE support.
type OIDCProvider struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth2   oauth2.Config
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

// oidcHTTPTimeout is the maximum time allowed for outbound OIDC HTTP calls.
const oidcHTTPTimeout = 10 * time.Second

// ValidateOIDCConfig checks that the required fields of an OIDCConfig are set.
func ValidateOIDCConfig(cfg api.OIDCConfig) error {
	if cfg.IssuerURL == "" {
		return fmt.Errorf("%w: issuer_url is required", ErrOIDCConfigInvalid)
	}
	if cfg.ClientID == "" {
		return fmt.Errorf("%w: client_id is required", ErrOIDCConfigInvalid)
	}
	if cfg.RedirectURI == "" {
		return fmt.Errorf("%w: redirect_uri is required", ErrOIDCConfigInvalid)
	}
	return nil
}

// NewOIDCProvider creates an OIDC provider from config.
func NewOIDCProvider(ctx context.Context, cfg api.OIDCConfig) (*OIDCProvider, error) {
	if err := ValidateOIDCConfig(cfg); err != nil {
		return nil, err
	}

	discoverCtx, cancel := context.WithTimeout(ctx, oidcHTTPTimeout)
	defer cancel()

	provider, err := oidc.NewProvider(discoverCtx, cfg.IssuerURL)
	if err != nil {
		return nil, errors.Join(ErrOIDCDiscovery, err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: cfg.ClientID,
	})

	oauth2Cfg := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURI,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	return &OIDCProvider{
		provider: provider,
		verifier: verifier,
		config:   cfg,
		oauth2:   oauth2Cfg,
	}, nil
}

// AuthorizationURL generates the OIDC authorization URL with PKCE and state.
func (p *OIDCProvider) AuthorizationURL(state, nonce, codeChallenge string) string {
	return p.oauth2.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// Exchange exchanges an authorization code for tokens and validates the ID token.
func (p *OIDCProvider) Exchange(ctx context.Context, code, codeVerifier, nonce string) (*OIDCClaims, *time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, oidcHTTPTimeout)
	defer cancel()

	token, err := p.oauth2.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, nil, errors.Join(ErrOIDCExchange, err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, nil, fmt.Errorf("%w: id_token not present in token response", ErrOIDCTokenInvalid)
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, nil, errors.Join(ErrOIDCTokenInvalid, err)
	}

	if idToken.Nonce != nonce {
		return nil, nil, ErrOIDCNonceMismatch
	}

	var claims OIDCClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, nil, errors.Join(ErrOIDCTokenInvalid, err)
	}

	var expiry *time.Time
	if !token.Expiry.IsZero() {
		expiry = &token.Expiry
	}

	return &claims, expiry, nil
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
