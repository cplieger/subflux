package accounts

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/cplieger/access/webauthn"
	"github.com/cplieger/subflux/internal/formschema"
)

const msgOrigin = "must be an https origin with a domain host and no path, such as https://subflux.example, or http://localhost with an optional port"

// parseOrigins reads passkey_origins: each entry an origin passkeys run at,
// and together one relying party, the host every other host equals or is a
// subdomain of, so the list's order never changes it. It answers the
// origins as a browser serializes them, their hosts in list order, and the
// RP ID, empty for an empty list. Paths are section-relative.
func parseOrigins(entries []string) (origins, hosts []string, rpID string, errs []formschema.FieldError) {
	for i, e := range entries {
		o, msg := parseOrigin(e)
		if msg != "" {
			errs = append(errs, formschema.FieldError{Path: fmt.Sprintf("%s[%d]", keyPasskeyOrigins, i), Message: msg})
			continue
		}
		origins, hosts = append(origins, o.String()), append(hosts, o.Host())
	}
	if len(errs) > 0 || len(entries) == 0 {
		return nil, nil, "", errs
	}
	rpID, ok := sharedHost(hosts)
	if !ok {
		return nil, nil, "", []formschema.FieldError{{Path: keyPasskeyOrigins, Message: "every origin's host must equal one of them or be its subdomain, such as subflux.example and app.subflux.example"}}
	}
	if _, err := webauthn.New(rpConfig(rpID, origins)); err != nil {
		return nil, nil, "", []formschema.FieldError{{Path: keyPasskeyOrigins, Message: fmt.Sprintf("the shared host %s is not a domain passkeys can use; name one such as subflux.example", rpID)}}
	}
	return origins, hosts, rpID, nil
}

// parseOrigin reads one entry; a non-empty message refuses it. Its host
// passes the logged-value rule as soon as the entry parses as a URL, before
// the origin rules, since that host may become the RP ID the security log
// records.
func parseOrigin(entry string) (o webauthn.Origin, msg string) {
	u, err := url.Parse(entry)
	if err != nil {
		return webauthn.Origin{}, msgOrigin
	}
	if msg = checkLogged(u.Hostname(), hostBound); msg != "" {
		return webauthn.Origin{}, msg
	}
	o, err = webauthn.ParseOrigin(entry)
	if err != nil {
		return webauthn.Origin{}, msgOrigin
	}
	if _, err := netip.ParseAddr(o.Host()); err == nil {
		return webauthn.Origin{}, "must name a domain host: passkeys never run at an IP address"
	}
	if strings.HasPrefix(o.String(), "http:") && o.Host() != "localhost" {
		return webauthn.Origin{}, "must use https: plain http runs passkeys only at localhost"
	}
	return o, ""
}

// sharedHost is the one host of hosts that every other equals or is a
// subdomain of.
func sharedHost(hosts []string) (string, bool) {
	for _, h := range hosts {
		if !slices.ContainsFunc(hosts, func(o string) bool { return o != h && !strings.HasSuffix(o, "."+h) }) {
			return h, true
		}
	}
	return "", false
}

// CheckOrigins refuses each passkey origin whose host is outside a non-empty
// allowedHosts, which holds httpserver.CanonicalHost forms (an origin's host
// is already one: lowercase, no port, never an IP), with paths at
// auth.passkey_origins[i], so the host gate never refuses a page passkeys run
// on. settings runs it once both sections parse, at Load and on every Save.
func CheckOrigins(c Config, allowedHosts []string) []formschema.FieldError {
	if len(allowedHosts) == 0 {
		return nil
	}
	var errs []formschema.FieldError
	for i, h := range c.get().hosts {
		if !slices.Contains(allowedHosts, h) {
			errs = append(errs, formschema.FieldError{
				Path:    fmt.Sprintf("%s.%s[%d]", SectionKey, keyPasskeyOrigins, i),
				Message: "its host " + h + " must be listed in server.allowed_hosts",
			})
		}
	}
	return errs
}
