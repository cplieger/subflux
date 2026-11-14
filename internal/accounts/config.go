// Package accounts is the one place subflux composes the access library: the
// auth section, the service app builds once, and the hasher the provider
// funnel shares.
package accounts

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v4"
)

const (
	keyPasswordLogin   = "password_login"
	keyIdleTimeout     = "session_idle_timeout"
	keyAbsoluteTimeout = "session_absolute_timeout"
	keyBreached        = "check_breached_passwords"
	keyDisableAuth     = "disable_auth"
	keyPasskeyOrigins  = "passkey_origins"
	keyOIDCEnabled     = "oidc_enabled"
	keyOIDCIssuer      = "oidc_issuer"
	keyOIDCClientID    = "oidc_client_id"
	keyOIDCSecret      = "oidc_client_secret" //nolint:gosec // G101: a settings key name, not a credential
	keyOIDCRedirect    = "oidc_redirect_url"  //nolint:gosec // G101: a settings key name, not a credential
	keyOIDCLabel       = "oidc_label"
	keyAutoProvision   = "oidc_auto_provision"
	keyAllowedGroups   = "oidc_allowed_groups"
)

// callbackPath is the one OIDC redirect path the routes serve.
const callbackPath = "/api/auth/oidc/callback"

// maxLabelRunes bounds the sign-in button's text.
const maxLabelRunes = 64

const (
	yes = "true"
	no  = "false"
)

// Schema is the auth section's form.
var Schema = formschema.Schema{Fields: []formschema.Field{
	{Key: keyPasswordLogin, Label: "Password sign-in", Kind: formschema.Bool, Default: yes},
	{
		Key: keyIdleTimeout, Label: "Session idle timeout", Kind: formschema.DurationMs, Default: "24h",
		Min: (5 * time.Minute).Milliseconds(), Max: (30 * 24 * time.Hour).Milliseconds(),
	},
	{
		Key: keyAbsoluteTimeout, Label: "Session absolute timeout", Kind: formschema.DurationMs, Default: "168h",
		Min: (5 * time.Minute).Milliseconds(), Max: (90 * 24 * time.Hour).Milliseconds(),
		Help: "At least the idle timeout.",
	},
	{Key: keyBreached, Label: "Refuse breached passwords", Kind: formschema.Bool, Default: no},
	{
		Key: keyDisableAuth, Label: "Disable authentication", Kind: formschema.Bool, Default: no, BootOnly: true,
		Help: "Takes effect only at startup: edit config.yaml and restart.",
	},
	{
		Key: keyPasskeyOrigins, Label: "Passkey origins", Kind: formschema.StringList,
		Help: "The https origins passkeys sign in from, such as https://subflux.example; empty turns passkeys off.",
	},
	{Key: keyOIDCEnabled, Label: "Single sign-on", Kind: formschema.Bool, Default: no},
	{Key: keyOIDCIssuer, Label: "OIDC issuer", Kind: formschema.String},
	{Key: keyOIDCClientID, Label: "OIDC client ID", Kind: formschema.String},
	{Key: keyOIDCSecret, Label: "OIDC client secret", Kind: formschema.Secret},
	{
		Key: keyOIDCRedirect, Label: "OIDC redirect URL", Kind: formschema.String,
		Help: "This server's address followed by " + callbackPath + ".",
	},
	{Key: keyOIDCLabel, Label: "Sign-in button text", Kind: formschema.String, Default: "Single sign-on"},
	{Key: keyAutoProvision, Label: "Create accounts on first sign-in", Kind: formschema.Bool, Default: no},
	{
		Key: keyAllowedGroups, Label: "Allowed groups", Kind: formschema.StringList,
		Help: "The groups claim values that may create an account.",
	},
}}

// SectionKey is the auth section's key in config.yaml; every error path
// ParseConfig and CheckOrigins answer starts with it.
const SectionKey = "auth"

// Config is the auth section, immutable. Its zero value is the defaults:
// password sign-in on, no passkeys, no OIDC.
type Config struct{ p *config }

type config struct {
	issuer, clientID, secret, redirect, label string
	// rpID is the host every passkey origin equals or is a subdomain of;
	// empty while passkeys are off.
	rpID          string
	origins       []string
	hosts         []string
	groups        []string
	idle          time.Duration
	absolute      time.Duration
	passwordLogin bool
	breach        bool
	disableAuth   bool
	oidcEnabled   bool
	autoProvision bool
}

var defaultConfig = sync.OnceValue(func() *config {
	c, errs := parse(nil)
	if len(errs) > 0 {
		panic(fmt.Sprintf("accounts: the auth defaults do not parse: %v", errs))
	}
	return c
})

func (c Config) get() *config {
	if c.p == nil {
		return defaultConfig()
	}
	return c.p
}

// ParseConfig reads the auth section, nil when absent, with errors at
// absolute paths (auth.<key>). A section that fails answers the zero Config,
// the defaults, beside its errors, so a caller keeping it never runs a
// partial section.
func ParseConfig(n *yaml.Node) (Config, []formschema.FieldError) {
	c, errs := parse(n)
	if len(errs) > 0 {
		return Config{}, formschema.Prefix(SectionKey, errs)
	}
	return Config{c}, nil
}

func parse(n *yaml.Node) (*config, []formschema.FieldError) {
	vals, errs := Schema.Decode(n)
	c := &config{
		passwordLogin: vals.Bool(keyPasswordLogin),
		idle:          vals.Duration(keyIdleTimeout),
		absolute:      vals.Duration(keyAbsoluteTimeout),
		breach:        vals.Bool(keyBreached),
		disableAuth:   vals.Bool(keyDisableAuth),
		oidcEnabled:   vals.Bool(keyOIDCEnabled),
		issuer:        vals.String(keyOIDCIssuer),
		clientID:      vals.String(keyOIDCClientID),
		secret:        vals.Secret(keyOIDCSecret),
		redirect:      vals.String(keyOIDCRedirect),
		label:         vals.String(keyOIDCLabel),
		autoProvision: vals.Bool(keyAutoProvision),
		groups:        vals.StringList(keyAllowedGroups),
	}
	refused, lerrs := c.checkLoggedOIDC()
	errs = append(errs, lerrs...)
	if c.oidcEnabled {
		errs = append(errs, c.checkOIDC(refused)...)
	}
	var oerrs []formschema.FieldError
	c.origins, c.hosts, c.rpID, oerrs = parseOrigins(vals.StringList(keyPasskeyOrigins))
	errs = append(errs, oerrs...)
	if c.idle > 0 && c.absolute > 0 && c.absolute < c.idle {
		errs = append(errs, formschema.FieldError{Path: keyAbsoluteTimeout, Message: "must be at least " + keyIdleTimeout})
	}
	if utf8.RuneCountInString(c.label) > maxLabelRunes {
		errs = append(errs, formschema.FieldError{Path: keyOIDCLabel, Message: fmt.Sprintf("must be at most %d characters", maxLabelRunes)})
	}
	if c.autoProvision && len(c.groups) == 0 {
		errs = append(errs, formschema.FieldError{Path: keyAutoProvision, Message: "needs at least one " + keyAllowedGroups + " entry, or every identity the provider signs in gets an account"})
	}
	return c, errs
}

// checkLoggedOIDC runs the logged-value rule on the OIDC rows of
// loggedFields whatever oidc_enabled says, answering the keys it refused.
func (c *config) checkLoggedOIDC() (map[string]bool, []formschema.FieldError) {
	var errs []formschema.FieldError
	refused := map[string]bool{}
	for _, f := range loggedFields {
		if f.key == keyPasskeyOrigins {
			continue
		}
		if msg := checkLogged(c.loggedValue(f.key), f.bound); msg != "" {
			errs = append(errs, formschema.FieldError{Path: f.key, Message: msg})
			refused[f.key] = true
		}
	}
	return refused, errs
}

// checkOIDC is an enabled block's presence and URL rules, each on a key the
// logged-value rule did not refuse.
func (c *config) checkOIDC(refused map[string]bool) []formschema.FieldError {
	var errs []formschema.FieldError
	required := []struct{ key, value string }{
		{keyOIDCIssuer, c.issuer}, {keyOIDCClientID, c.clientID}, {keyOIDCSecret, c.secret}, {keyOIDCRedirect, c.redirect},
	}
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			errs = append(errs, formschema.FieldError{Path: r.key, Message: "required while " + keyOIDCEnabled + " is true"})
			refused[r.key] = true
		}
	}
	if !refused[keyOIDCIssuer] {
		if msg := settingURL(c.issuer, true); msg != "" {
			errs = append(errs, formschema.FieldError{Path: keyOIDCIssuer, Message: msg})
		}
	}
	if !refused[keyOIDCRedirect] {
		if msg := redirectURL(c.redirect); msg != "" {
			errs = append(errs, formschema.FieldError{Path: keyOIDCRedirect, Message: msg})
		}
	}
	return errs
}

func redirectURL(raw string) string {
	if msg := settingURL(raw, false); msg != "" {
		return msg
	}
	if u, _ := url.Parse(raw); u.Path != callbackPath {
		return "must end in " + callbackPath
	}
	return ""
}

func (c *config) loggedValue(key string) string {
	switch key {
	case keyOIDCIssuer:
		return c.issuer
	case keyOIDCClientID:
		return c.clientID
	case keyOIDCRedirect:
		return c.redirect
	}
	panic("accounts: no logged value for " + key)
}

// settingURL is an issuer's or a redirect's rule: absolute, https or http on a
// loopback host, with a host and no userinfo or fragment, and for an issuer
// no query (OpenID Connect Discovery 1.0 section 4.1). "" accepts.
func settingURL(raw string, issuer bool) string {
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Host == "" || u.Opaque != "":
		return "must be an absolute URL such as https://idp.example"
	case u.Scheme != "https" && (u.Scheme != "http" || !loopbackHost(u.Hostname())):
		return "must use https, or http on a loopback host"
	case u.User != nil || u.Fragment != "" || u.RawFragment != "":
		return "must not hold a user name or a #fragment"
	case issuer && (u.RawQuery != "" || u.ForceQuery):
		return "must not hold a ?query"
	}
	return ""
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// BypassEnabled reports c's disable_auth; the bypass reads it only from
// Deps.Boot.
func BypassEnabled(c Config) bool { return c.get().disableAuth }

// OIDCLabel is the single sign-on button's text.
func (c Config) OIDCLabel() string { return c.get().label }

// loggedField is one record field the security log carries verbatim, its
// source key and the byte bound its Detail needs.
type loggedField struct {
	field string
	key   string
	bound int
}

// loggedFields is the one source rule for every string field of
// statedb.AuthMethodState; TestLoggedFieldsCoverRecord fails on a field
// added without a row. The OIDC bounds keep the sealed flow cookie near 2 KB
// (access oidc.NewProvider refuses a larger one), a host's is the DNS limit,
// and every bound is under the security log's 256-byte Detail.
var loggedFields = []loggedField{
	{field: "OIDCIssuer", key: keyOIDCIssuer, bound: oidcBound},
	{field: "SignIn.OIDCClientID", key: keyOIDCClientID, bound: oidcBound},
	{field: "SignIn.OIDCRedirectURL", key: keyOIDCRedirect, bound: oidcBound},
	{field: "RPID", key: keyPasskeyOrigins, bound: hostBound},
}

const (
	oidcBound = 255
	hostBound = 253
)

const (
	msgNotUTF8    = "must be valid UTF-8"
	msgUnsafeRune = "must not hold a control, bidi or line-separator character"
)

// checkLogged refuses v, with its message, when it is not valid UTF-8, then
// when it holds a rune textsafe.IsUnsafeSingleLine reports, then when it is
// over bound bytes; "" accepts. UTF-8 runs first: ranging over invalid UTF-8
// yields U+FFFD, which the rune check passes, and ${SUBFLUX_*} expansion
// inserts environment bytes unchecked.
func checkLogged(v string, bound int) string {
	switch {
	case !utf8.ValidString(v):
		return msgNotUTF8
	case strings.ContainsFunc(v, textsafe.IsUnsafeSingleLine):
		return msgUnsafeRune
	case len(v) > bound:
		return fmt.Sprintf("must be at most %d bytes", bound)
	}
	return ""
}
